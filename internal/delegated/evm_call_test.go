package delegated

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/svpchain/svpdt"

	wallettypes "github.com/dydxprotocol/v4-chain/protocol/x/agentwallet/types"
)

const delegatedEVMTestContract = "0x000000000000000000000000000000000000c07e"
const delegatedEVMRecipient = "0x00000000000000000000000000000000000000dd"

func calldata(signature string) string {
	selector := crypto.Keccak256([]byte(signature))[:wallettypes.EVMSelectorLen]
	data := append(selector, make([]byte, 32)...)
	return "0x" + hex.EncodeToString(data)
}

func evmProof(t *testing.T, f *fixture) []string {
	t.Helper()
	return f.issue(t, func(p *svpdt.IssueParams) {
		p.Caveats.Actions = svpdt.StringSet{ActionLendoraSupply}
		p.Caveats.Contracts = svpdt.StringSet{delegatedEVMTestContract}
	})
}

func TestExecuteEVMCallBuildsDelegatedWrapper(t *testing.T) {
	f := newFixture(t)
	data := calldata("mint(uint256)")

	res, err := f.svc.ExecuteEVMCall(context.Background(), ExecEVMCallInput{
		Proof: evmProof(t, f),
		Call:  EVMCallParams{Contract: delegatedEVMTestContract, Data: data},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TxHash != "CAFEBABE" || res.Principal != testDelegator || res.AgentID != f.agentID {
		t.Errorf("result = %+v", res)
	}

	var wrapper wallettypes.MsgAgentExecDelegated
	decodeSoleTxMsg(t, f.broadcast.txBytes, "/dydxprotocol.agentwallet.MsgAgentExecDelegated", &wrapper)
	if wrapper.Executor != f.svc.cfg.Operator || wrapper.AgentId != f.agentID {
		t.Errorf("wrapper executor/agent = %s/%s", wrapper.Executor, wrapper.AgentId)
	}

	var inner wallettypes.MsgEVMCall
	if err := proto.Unmarshal(wrapper.InnerMsg.Value, &inner); err != nil {
		t.Fatal(err)
	}
	if wrapper.InnerMsg.TypeUrl != "/dydxprotocol.agentwallet.MsgEVMCall" {
		t.Errorf("inner type URL = %q", wrapper.InnerMsg.TypeUrl)
	}
	if inner.Principal != testDelegator || inner.Contract != delegatedEVMTestContract || "0x"+hex.EncodeToString(inner.Data) != data {
		t.Errorf("inner EVM call = %+v", inner)
	}
}

func TestExecuteEVMCallRefusals(t *testing.T) {
	tests := map[string]struct {
		proof func(t *testing.T, f *fixture) []string
		call  EVMCallParams
		want  string
	}{
		"ungranted action": {
			proof: func(t *testing.T, f *fixture) []string { return f.issue(t, nil) },
			call:  EVMCallParams{Contract: delegatedEVMTestContract, Data: calldata("mint(uint256)")},
			want:  "does not grant action",
		},
		"ungranted contract": {
			proof: func(t *testing.T, f *fixture) []string {
				return f.issue(t, func(p *svpdt.IssueParams) {
					p.Caveats.Actions = svpdt.StringSet{ActionLendoraSupply}
				})
			},
			call: EVMCallParams{Contract: delegatedEVMTestContract, Data: calldata("mint(uint256)")},
			want: "does not grant contract",
		},
		"unknown selector": {
			proof: evmProof,
			call:  EVMCallParams{Contract: delegatedEVMTestContract, Data: "0xdeadbeef" + strings.Repeat("00", 32)},
			want:  "is not delegated",
		},
		"malformed calldata": {
			proof: evmProof,
			call:  EVMCallParams{Contract: delegatedEVMTestContract, Data: "0x" + hex.EncodeToString(crypto.Keccak256([]byte("mint(uint256)"))[:4])},
			want:  "one uint256",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.svc.ExecuteEVMCall(context.Background(), ExecEVMCallInput{
				Proof: tc.proof(t, f),
				Call:  tc.call,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
			if f.broadcast.txBytes != nil {
				t.Error("a refused delegated EVM call must not reach broadcast")
			}
		})
	}
}

func nativeTransferProof(t *testing.T, f *fixture) []string {
	t.Helper()
	return f.issue(t, func(p *svpdt.IssueParams) {
		p.Caveats.Actions = svpdt.StringSet{ActionEVMNativeTransfer}
		p.Caveats.Contracts = svpdt.StringSet{delegatedEVMRecipient}
	})
}

func TestExecuteEVMNativeTransferBuildsDelegatedWrapper(t *testing.T) {
	f := newFixture(t)

	res, err := f.svc.ExecuteEVMNativeTransfer(context.Background(), ExecEVMNativeTransferInput{
		Proof: nativeTransferProof(t, f),
		Transfer: EVMNativeTransferParams{
			Recipient: delegatedEVMRecipient,
			Value:     "2500000000000000000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TxHash != "CAFEBABE" || res.Principal != testDelegator || res.AgentID != f.agentID {
		t.Errorf("result = %+v", res)
	}

	var wrapper wallettypes.MsgAgentExecDelegated
	decodeSoleTxMsg(t, f.broadcast.txBytes, "/dydxprotocol.agentwallet.MsgAgentExecDelegated", &wrapper)
	var inner wallettypes.MsgEVMCall
	if err := proto.Unmarshal(wrapper.InnerMsg.Value, &inner); err != nil {
		t.Fatal(err)
	}
	if inner.Principal != testDelegator || inner.Contract != delegatedEVMRecipient || inner.Value != "2500000000000000000" || len(inner.Data) != 0 {
		t.Errorf("inner native transfer = %+v", inner)
	}
}

func TestExecuteEVMNativeTransferRefusals(t *testing.T) {
	tests := map[string]struct {
		proof     func(t *testing.T, f *fixture) []string
		transfer  EVMNativeTransferParams
		wantError string
	}{
		"ungranted action": {
			proof:     func(t *testing.T, f *fixture) []string { return f.issue(t, nil) },
			transfer:  EVMNativeTransferParams{Recipient: delegatedEVMRecipient, Value: "1"},
			wantError: "does not grant action",
		},
		"ungranted recipient": {
			proof: nativeTransferProof,
			transfer: EVMNativeTransferParams{
				Recipient: delegatedEVMTestContract,
				Value:     "1",
			},
			wantError: "does not grant recipient",
		},
		"zero value": {
			proof:     nativeTransferProof,
			transfer:  EVMNativeTransferParams{Recipient: delegatedEVMRecipient, Value: "0"},
			wantError: "calldata carries no selector",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.svc.ExecuteEVMNativeTransfer(context.Background(), ExecEVMNativeTransferInput{
				Proof:    tc.proof(t, f),
				Transfer: tc.transfer,
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("error = %v, want %q", err, tc.wantError)
			}
			if f.broadcast.txBytes != nil {
				t.Error("a refused delegated native transfer must not reach broadcast")
			}
		})
	}
}
