package tools

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/svpchain/svpchain-evm-agent/internal/mcp/payload"
	"github.com/svpchain/svpchain-evm-agent/internal/mcp/policy"
)

// This file holds the EVM tool family. The two tools below are
// contract-agnostic engine tools (written once, shared by every EVM contract);
// per-contract build_* tools (e.g. a future build_swap) live alongside them
// and call EVMAssembler.Assemble. ownerEthAddress is shared with the HTTP
// faucet tools (faucet.go) since both map a bech32 owner to its 0x address.

// ownerEthAddress converts a tenant's bech32 owner (svp1…) to its 0x EVM
// address. Both are the same 20 underlying bytes — the same identity the auth
// handshake recovers (see internal/mcp/auth/recover.go), just rendered as hex.
func ownerEthAddress(owner string) (common.Address, error) {
	acc, err := sdk.AccAddressFromBech32(owner)
	if err != nil {
		return common.Address{}, fmt.Errorf("parse owner %q: %w", owner, err)
	}
	return common.BytesToAddress(acc.Bytes()), nil
}

// -- broadcast_evm_tx --------------------------------------------------

type BroadcastEVMTxInput struct {
	ClientID string              `json:"client_id" jsonschema:"payload-level idempotency uuid (must match the EVMTxPayload.client_id that was signed)"`
	SignedTx payload.EVMSignedTx `json:"signed_tx"`
}

type BroadcastEVMTxOutput struct {
	TxHash string `json:"tx_hash"` // 0x hex
}

func (h *Handlers) BroadcastEVMTx(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BroadcastEVMTxInput,
) (*mcp.CallToolResult, BroadcastEVMTxOutput, error) {
	tp, err := h.authorize(ctx, "broadcast_evm_tx")
	if err != nil {
		return nil, BroadcastEVMTxOutput{}, err
	}
	if h.Deps.Chain.EVM == nil {
		return nil, BroadcastEVMTxOutput{}, userErrf("EVM is not enabled on this server (no evm_rpc_url configured)")
	}
	if err := h.Deps.Idempotency.Claim(tp.TenantID, in.ClientID); err != nil {
		return nil, BroadcastEVMTxOutput{}, err
	}

	rawBytes, err := hexutil.Decode(in.SignedTx.RawTxHex)
	if err != nil {
		return nil, BroadcastEVMTxOutput{}, fmt.Errorf("decode raw_tx_hex: %w", err)
	}
	var tx ethtypes.Transaction
	if err := tx.UnmarshalBinary(rawBytes); err != nil {
		return nil, BroadcastEVMTxOutput{}, fmt.Errorf("decode signed evm tx: %w", err)
	}

	// Recover the sender from the signature and verify it matches the tenant
	// owner — the EVM analog of broadcast_signed_tx's signer/owner check.
	// Without this a tenant could submit a tx signed by some other key.
	from, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(tx.ChainId()), &tx)
	if err != nil {
		return nil, BroadcastEVMTxOutput{}, fmt.Errorf("recover evm sender: %w", err)
	}
	ownerEth, err := ownerEthAddress(tp.Owner)
	if err != nil {
		return nil, BroadcastEVMTxOutput{}, err
	}
	if from != ownerEth {
		return nil, BroadcastEVMTxOutput{}, fmt.Errorf(
			"evm sender %s does not match tenant owner %s", from.Hex(), ownerEth.Hex())
	}

	// Route to the chain the tx was signed for: a configured foreign chain
	// (inbound bridge deposit) goes to its own RPC; everything else goes to the
	// home (svpchain) client, preserving single-chain behavior.
	client := h.Deps.Chain.EVM
	isHome := true
	if fc, ok := h.Deps.EVM.ForeignChains[tx.ChainId().Uint64()]; ok {
		client = fc.Client
		isHome = false
	}

	// Per-symbol daily transfer-out cap (EVM rail): native-value sends share the
	// owner's SVP cap with x/bank sends. The amount is reserved here and released
	// below unless the node accepts the tx, which prevents concurrent sends from
	// exceeding one cap. ERC-20 contracts are dynamic and have no static cap
	// symbol. The router / WSVP addresses exclude swap/wrap legs. Caps apply only
	// to the home chain; an inbound foreign-chain deposit is not an outflow.
	reserved := map[string]*big.Int{}
	releaseTransferOut := func() {
		for sym, amt := range reserved {
			h.Deps.TransferOut.Release(tp.Owner, sym, amt)
		}
	}
	if isHome {
		var router, wsvp common.Address
		if h.Deps.EVM.Uniswap != nil {
			router = h.Deps.EVM.Uniswap.Router()
			wsvp = h.Deps.EVM.Uniswap.WSVP()
		}
		for sym, amt := range decodeTransferOut(tx.To(), tx.Value(), tx.Data(), ownerEth, router, wsvp) {
			if err := h.Deps.TransferOut.Reserve(tp.Owner, sym, amt); err != nil {
				releaseTransferOut()
				return nil, BroadcastEVMTxOutput{}, err
			}
			reserved[sym] = amt
		}
	}
	// Every path from here to a node-accepted tx must hand the reservations
	// back; committed flips only once the node has taken the tx.
	committed := false
	defer func() {
		if !committed {
			releaseTransferOut()
		}
	}()

	txHash, sendErr := client.SendTransaction(ctx, &tx)
	outcome := "broadcast"
	reason := ""
	if sendErr != nil {
		outcome = "chain_reject"
		reason = sendErr.Error()
		txHash = tx.Hash().Hex() // hash is well-defined even if the node rejects it
	}
	_ = h.Deps.Auditor.Append(policy.AuditEntry{
		TenantID: tp.TenantID,
		Owner:    tp.Owner,
		Tool:     "broadcast_evm_tx",
		ClientID: in.ClientID,
		TxHash:   txHash,
		Outcome:  outcome,
		Reason:   reason,
	})
	if sendErr != nil {
		return nil, BroadcastEVMTxOutput{}, fmt.Errorf("broadcast evm tx: %w", sendErr)
	}
	// The reservations become the spend only once the node accepts the tx —
	// the deferred release above returns them on every other path, so a
	// rejected broadcast doesn't eat the tenant's daily cap.
	committed = true
	return nil, BroadcastEVMTxOutput{TxHash: txHash}, nil
}

// decodeTransferOut inspects a single signed EVM tx and returns the tenant's
// outbound amounts grouped by cap symbol. Only native SVP is tracked here;
// ERC-20 contracts are discovered dynamically and have no static cap symbol.
//
// It tracks plain value sends only. The router/WSVP guard additionally drops
// the native-value leg of a native→token swap or a wrap.
func decodeTransferOut(to *common.Address, value *big.Int, data []byte, owner, router, wsvp common.Address) map[string]*big.Int {
	out := map[string]*big.Int{}
	addOut := func(sym string, amt *big.Int) {
		if amt == nil || amt.Sign() <= 0 {
			return
		}
		if cur := out[sym]; cur != nil {
			out[sym] = new(big.Int).Add(cur, amt)
		} else {
			out[sym] = new(big.Int).Set(amt)
		}
	}

	// Native SVP value transfer — excluding sends to the router / WSVP, which
	// are the swap and wrap legs we intentionally don't cap.
	if value != nil && value.Sign() > 0 && to != nil && *to != router && *to != wsvp {
		if sym, ok := symbolForNative(); ok {
			addOut(sym, value)
		}
	}

	return out
}

// -- evm_tx_status -----------------------------------------------------

type EVMTxStatusInput struct {
	TxHash  string `json:"tx_hash" jsonschema:"0x hex tx hash returned by broadcast_evm_tx"`
	ChainID uint64 `json:"chain_id,omitempty" jsonschema:"EVM chain id the tx was broadcast on; omit for the home (svpchain) chain. For an inbound bridge deposit, pass the source_chain_id returned by build_bridge_deposit_inbound."`
}

type EVMTxStatusOutput struct {
	TxHash      string `json:"tx_hash"`
	Status      string `json:"status"` // "pending" | "success" | "failed"
	BlockNumber int64  `json:"block_number,omitempty"`
	GasUsed     uint64 `json:"gas_used,omitempty"`
}

func (h *Handlers) EVMTxStatus(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in EVMTxStatusInput,
) (*mcp.CallToolResult, EVMTxStatusOutput, error) {
	if _, err := h.authorize(ctx, "evm_tx_status"); err != nil {
		return nil, EVMTxStatusOutput{}, err
	}
	if h.Deps.Chain.EVM == nil {
		return nil, EVMTxStatusOutput{}, userErrf("EVM is not enabled on this server (no evm_rpc_url configured)")
	}
	// Default to the home (svpchain) client; an explicit chain_id routes to a
	// configured foreign chain (e.g. an inbound bridge deposit's source chain).
	client := h.Deps.Chain.EVM
	if in.ChainID != 0 && in.ChainID != h.Deps.EVM.HomeChainID {
		fc, ok := h.Deps.EVM.ForeignChains[in.ChainID]
		if !ok {
			return nil, EVMTxStatusOutput{}, userErrf("chain id %d is not configured on this server", in.ChainID)
		}
		client = fc.Client
	}
	receipt, err := client.TransactionReceipt(ctx, common.HexToHash(in.TxHash))
	if err != nil {
		// Not-yet-included is a legitimate empty result, not an error —
		// mirror the indexer client's NotFound handling.
		if errors.Is(err, ethereum.NotFound) {
			return nil, EVMTxStatusOutput{TxHash: in.TxHash, Status: "pending"}, nil
		}
		return nil, EVMTxStatusOutput{}, fmt.Errorf("evm receipt %s: %w", in.TxHash, err)
	}
	status := "failed"
	if receipt.Status == ethtypes.ReceiptStatusSuccessful {
		status = "success"
	}
	return nil, EVMTxStatusOutput{
		TxHash:      in.TxHash,
		Status:      status,
		BlockNumber: receipt.BlockNumber.Int64(),
		GasUsed:     receipt.GasUsed,
	}, nil
}
