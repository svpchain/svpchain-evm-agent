// Command agent-deregister removes this agent's registration from x/agent.
//
// It intentionally uses the same locally-held owner key as agent-register.
// The deployed EVM agent has no lifecycle key and is not contacted here. By
// default this command only prints the chain record; -confirm is required
// before it signs and broadcasts MsgDeregisterAgent.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"

	"github.com/svpchain/svpchain-evm-agent/internal/agentchain"
	"github.com/svpchain/svpchain-evm-agent/internal/owner"
)

const (
	defaultFeeDenom    = "asvp"
	defaultFeeAmount   = "25000000000000000"
	defaultFeeGasLimit = uint64(1_000_000)
)

type opts struct {
	chainID   string
	grpcAddr  string
	rpcURL    string
	agentID   string
	keyFile   string
	feeDenom  string
	feeAmount string
	gasLimit  uint64
	confirm   bool
	timeout   time.Duration
}

func main() {
	var o opts
	flag.StringVar(&o.chainID, "chain-id", "", "chain id of the chain carrying x/agent")
	flag.StringVar(&o.grpcAddr, "grpc", "", "gRPC address of that chain")
	flag.StringVar(&o.rpcURL, "rpc", "", "CometBFT RPC URL of that chain; uses ABCI queries and broadcast_tx_sync")
	flag.StringVar(&o.agentID, "agent-id", "", "registered agent DID; defaults to the legacy DID derived from the owner key")
	flag.StringVar(&o.keyFile, "key-file", "", "owner key file, when "+owner.KeyEnvVar+" is not set")
	flag.StringVar(&o.feeDenom, "fee-denom", defaultFeeDenom, "fee denom")
	flag.StringVar(&o.feeAmount, "fee-amount", defaultFeeAmount, "fee amount")
	flag.Uint64Var(&o.gasLimit, "gas-limit", defaultFeeGasLimit, "gas limit")
	flag.BoolVar(&o.confirm, "confirm", false, "sign and broadcast the deregistration; omitted means preview only")
	flag.DurationVar(&o.timeout, "timeout", 90*time.Second, "deadline for the whole exchange")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()

	if err := run(ctx, o, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "agent-deregister: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o opts, w io.Writer) error {
	if strings.TrimSpace(o.chainID) == "" || (strings.TrimSpace(o.grpcAddr) == "" && strings.TrimSpace(o.rpcURL) == "") {
		return fmt.Errorf("-chain-id and one of -grpc or -rpc are required")
	}
	if strings.TrimSpace(o.grpcAddr) != "" && strings.TrimSpace(o.rpcURL) != "" {
		return fmt.Errorf("-grpc and -rpc are mutually exclusive")
	}

	priv, addrStr, err := owner.Load(o.keyFile)
	if err != nil {
		return err
	}
	if priv == nil {
		return fmt.Errorf("no owner key in %s or -key-file — only the registered owner can deregister this agent", owner.KeyEnvVar)
	}
	ownerAddr, err := sdk.AccAddressFromBech32(addrStr)
	if err != nil {
		return fmt.Errorf("derive owner address: %w", err)
	}

	var client *agentchain.Client
	if o.rpcURL != "" {
		client, err = agentchain.DialRPC(ctx, o.rpcURL)
	} else {
		client, err = agentchain.Dial(ctx, o.grpcAddr)
	}
	if err != nil {
		return fmt.Errorf("dial chain: %w", err)
	}
	defer client.Close()

	agentID := strings.TrimSpace(o.agentID)
	if agentID == "" {
		agentID = agentchain.AgentID(ownerAddr)
	} else {
		agentOwner, _, err := agenttypes.ParseAgentId(agentID)
		if err != nil {
			return fmt.Errorf("-agent-id: %w", err)
		}
		if !agentOwner.Equals(ownerAddr) {
			return fmt.Errorf("-agent-id belongs to %s, not the configured owner %s", agentOwner, addrStr)
		}
	}
	existing, found, err := client.AgentByID(ctx, agentID)
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(w, "not registered — no deregistration transaction will be submitted\n")
		fmt.Fprintf(w, "  agent %s\n", agentID)
		fmt.Fprintf(w, "  owner %s\n", addrStr)
		return nil
	}
	if existing.Owner != addrStr {
		return fmt.Errorf("chain record owner %s does not match the configured owner %s; refusing to deregister", existing.Owner, addrStr)
	}

	fmt.Fprintf(w, "registered agent found\n")
	fmt.Fprintf(w, "  agent    %s\n", existing.AgentId)
	fmt.Fprintf(w, "  owner    %s\n", existing.Owner)
	fmt.Fprintf(w, "  endpoint %s\n", existing.Endpoint)
	fmt.Fprintf(w, "  status   %s\n", existing.Status)
	fmt.Fprintf(w, "  bond     %s\n", existing.Bond)
	if existing.UnbondingCompleteHeight != 0 {
		fmt.Fprintf(w, "  unbonding complete height %d\n", existing.UnbondingCompleteHeight)
	}
	if existing.Status == agenttypes.AgentStatus_AGENT_STATUS_DEREGISTERING {
		fmt.Fprintln(w, "already deregistering — no transaction will be submitted")
		return nil
	}
	fmt.Fprintln(w, "warning: deregistration removes this agent from chain discovery and starts bond unbonding.")
	fmt.Fprintln(w, "warning: it does not stop the deployed container or remove its nginx/DNS configuration.")

	msg := &agenttypes.MsgDeregisterAgent{Owner: addrStr, AgentId: agentID}
	if err := msg.ValidateBasic(); err != nil {
		return fmt.Errorf("deregister message is invalid: %w", err)
	}
	if !o.confirm {
		fmt.Fprintln(w, "preview only — rerun with -confirm to sign and broadcast")
		return nil
	}

	acct, err := client.Account(ctx, addrStr)
	if err != nil {
		return fmt.Errorf("query owner account: %w", err)
	}
	txBytes, err := owner.SignTx(priv, o.chainID, acct, []sdk.Msg{msg}, owner.FeeSpec{
		Denom:    o.feeDenom,
		Amount:   o.feeAmount,
		GasLimit: o.gasLimit,
	})
	if err != nil {
		return err
	}
	res, err := client.BroadcastSync(ctx, txBytes)
	if err != nil {
		return fmt.Errorf("deregistration failed: %w", err)
	}
	fmt.Fprintf(w, "deregistration submitted — tx %s\n", res.TxHash)
	return nil
}
