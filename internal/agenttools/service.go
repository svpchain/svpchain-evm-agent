// Package agenttools contains the EVM Agent's own small public tool surface.
// DeFi-specific operations live behind the private MCP client.
package agenttools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/svpchain/svpchain-evm-agent/internal/mcp/auth"
)

type ctxKey uint8

const tenantKey ctxKey = 1

type Tenant struct {
	ID    string
	Owner string
}

func WithTenant(ctx context.Context, tenant Tenant) context.Context {
	return context.WithValue(ctx, tenantKey, tenant)
}
func tenantFrom(ctx context.Context) (Tenant, bool) {
	value, ok := ctx.Value(tenantKey).(Tenant)
	return value, ok
}

type Service struct {
	chainID string
	client  *ethclient.Client
	nonces  *auth.NonceStore
	tenants *auth.DynamicTenantStore
	mu      sync.Mutex
	seen    map[string]time.Time
}

func New(chainID, rpcURL string, nonces *auth.NonceStore, tenants *auth.DynamicTenantStore) (*Service, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial EVM RPC: %w", err)
	}
	return &Service{chainID: chainID, client: client, nonces: nonces, tenants: tenants, seen: map[string]time.Time{}}, nil
}

func (s *Service) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

type AuthChallengeInput struct {
	Owner string `json:"owner" jsonschema:"the svp1 address that will sign the challenge"`
}
type AuthChallengeOutput struct {
	Challenge string `json:"challenge"`
	Nonce     string `json:"nonce"`
	ExpiresAt int64  `json:"expires_at"`
}

func (s *Service) AuthChallenge(_ context.Context, in AuthChallengeInput) (AuthChallengeOutput, error) {
	if in.Owner == "" {
		return AuthChallengeOutput{}, fmt.Errorf("owner is required")
	}
	nonce, expiry, err := s.nonces.Issue(in.Owner)
	if err != nil {
		return AuthChallengeOutput{}, fmt.Errorf("issue nonce: %w", err)
	}
	return AuthChallengeOutput{Challenge: auth.BuildChallenge(s.chainID, nonce, expiry), Nonce: nonce, ExpiresAt: expiry.Unix()}, nil
}

type AuthVerifyInput struct {
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}
type AuthVerifyOutput struct {
	BearerToken string `json:"bearer_token"`
	Owner       string `json:"owner"`
	ExpiresAt   int64  `json:"expires_at"`
}

func (s *Service) AuthVerify(_ context.Context, in AuthVerifyInput) (AuthVerifyOutput, error) {
	owner, expiry, err := s.nonces.Consume(in.Nonce)
	if err != nil {
		if errors.Is(err, auth.ErrNonceNotFound) {
			return AuthVerifyOutput{}, fmt.Errorf("nonce not found or already used")
		}
		if errors.Is(err, auth.ErrNonceExpired) {
			return AuthVerifyOutput{}, fmt.Errorf("nonce expired; re-run auth_challenge")
		}
		return AuthVerifyOutput{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(in.Signature)
	if err != nil {
		return AuthVerifyOutput{}, fmt.Errorf("decode signature base64: %w", err)
	}
	recovered, err := auth.RecoverOwner(auth.BuildChallenge(s.chainID, in.Nonce, expiry), sig)
	if err != nil {
		return AuthVerifyOutput{}, fmt.Errorf("recover address from signature: %w", err)
	}
	if recovered != owner {
		return AuthVerifyOutput{}, fmt.Errorf("recovered address does not match the owner this nonce was issued to")
	}
	bearer, _, expiresAt, err := s.tenants.Mint(owner)
	if err != nil {
		return AuthVerifyOutput{}, fmt.Errorf("mint tenant: %w", err)
	}
	return AuthVerifyOutput{BearerToken: bearer, Owner: owner, ExpiresAt: expiresAt.Unix()}, nil
}

type SignedTx struct {
	RawTxHex string `json:"raw_tx_hex" jsonschema:"0x-prefixed signed transaction bytes"`
}
type BroadcastInput struct {
	ClientID string   `json:"client_id"`
	SignedTx SignedTx `json:"signed_tx"`
}
type BroadcastOutput struct {
	TxHash string `json:"tx_hash"`
}

func (s *Service) Broadcast(ctx context.Context, in BroadcastInput) (BroadcastOutput, error) {
	if in.ClientID == "" {
		return BroadcastOutput{}, fmt.Errorf("missing client_id")
	}
	raw, err := hexutil.Decode(in.SignedTx.RawTxHex)
	if err != nil {
		return BroadcastOutput{}, fmt.Errorf("decode raw_tx_hex: %w", err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return BroadcastOutput{}, fmt.Errorf("decode signed evm tx: %w", err)
	}
	if err := s.client.SendTransaction(ctx, &tx); err != nil {
		return BroadcastOutput{}, fmt.Errorf("broadcast evm tx: %w", err)
	}
	return BroadcastOutput{TxHash: tx.Hash().Hex()}, nil
}

type TxStatusInput struct {
	TxHash string `json:"tx_hash" jsonschema:"0x transaction hash"`
}
type TxStatusOutput struct {
	TxHash      string `json:"tx_hash"`
	Status      string `json:"status"`
	BlockNumber int64  `json:"block_number,omitempty"`
	GasUsed     uint64 `json:"gas_used,omitempty"`
}

func (s *Service) TxStatus(ctx context.Context, in TxStatusInput) (TxStatusOutput, error) {
	receipt, err := s.client.TransactionReceipt(ctx, common.HexToHash(in.TxHash))
	if errors.Is(err, ethereum.NotFound) {
		return TxStatusOutput{TxHash: in.TxHash, Status: "pending"}, nil
	}
	if err != nil {
		return TxStatusOutput{}, fmt.Errorf("evm receipt %s: %w", in.TxHash, err)
	}
	status := "failed"
	if receipt.Status == types.ReceiptStatusSuccessful {
		status = "success"
	}
	return TxStatusOutput{TxHash: in.TxHash, Status: status, BlockNumber: receipt.BlockNumber.Int64(), GasUsed: receipt.GasUsed}, nil
}

func (s *Service) claim(tenantID, clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + "|" + clientID
	now := time.Now()
	if expiry, exists := s.seen[key]; exists && expiry.After(now) {
		return fmt.Errorf("duplicate broadcast: client_id %s already used", clientID)
	}
	s.seen[key] = now.Add(10 * time.Minute)
	return nil
}
