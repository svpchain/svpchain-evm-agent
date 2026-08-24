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

func calldata(signature string, words int) string {
	selector := crypto.Keccak256([]byte(signature))[:wallettypes.EVMSelectorLen]
	data := append(selector, make([]byte, words*32)...)
	return "0x" + hex.EncodeToString(data)
}

func evmProof(t *testing.T, f *fixture, call EVMCallParams) []string {
	t.Helper()
	data, err := hex.DecodeString(strings.TrimPrefix(call.Data, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	task := (&wallettypes.MsgEVMCall{
		Principal: testDelegator,
		Contract:  call.Contract,
		Data:      data,
	}).DelegationMethodTask()
	return f.issue(t, func(p *svpdt.IssueParams) {
		p.Caveats.Actions = svpdt.StringSet{ActionEVMContractCall}
		p.Caveats.Contracts = svpdt.StringSet{delegatedEVMTestContract}
		p.Caveats.Task = task
	})
}

func TestExecuteEVMCallBuildsDelegatedWrapper(t *testing.T) {
	f := newFixture(t)
	data := calldata("transfer(address,uint256)", 2)
	call := EVMCallParams{Contract: delegatedEVMTestContract, Data: data, Value: "25"}

	res, err := f.svc.ExecuteEVMCall(context.Background(), ExecEVMCallInput{
		Proof: evmProof(t, f, call),
		Call:  call,
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
	if inner.Principal != testDelegator || inner.Contract != delegatedEVMTestContract || inner.Value != "25" || "0x"+hex.EncodeToString(inner.Data) != data {
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
			call:  EVMCallParams{Contract: delegatedEVMTestContract, Data: calldata("transfer(address,uint256)", 2)},
			want:  "does not grant action",
		},
		"ungranted contract": {
			proof: func(t *testing.T, f *fixture) []string {
				return f.issue(t, func(p *svpdt.IssueParams) {
					p.Caveats.Actions = svpdt.StringSet{ActionEVMContractCall}
				})
			},
			call: EVMCallParams{Contract: delegatedEVMTestContract, Data: calldata("transfer(address,uint256)", 2)},
			want: "does not grant contract",
		},
		"method task mismatch": {
			proof: func(t *testing.T, f *fixture) []string {
				return evmProof(t, f, EVMCallParams{Contract: delegatedEVMTestContract, Data: calldata("transfer(address,uint256)", 2)})
			},
			call: EVMCallParams{Contract: delegatedEVMTestContract, Data: calldata("approve(address,uint256)", 2)},
			want: "does not bind EVM contract method",
		},
		"malformed calldata": {
			proof: func(t *testing.T, f *fixture) []string { return f.issue(t, nil) },
			call:  EVMCallParams{Contract: delegatedEVMTestContract, Data: "0x1234"},
			want:  "calldata carries no selector",
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

func TestExecuteEVMContractMethodEncodesConfiguredCall(t *testing.T) {
	f := newFixture(t)
	f.svc.contracts = contractsByAddress([]ConfiguredContract{{
		ID: delegatedEVMTestContract, Address: delegatedEVMTestContract,
		Methods: []string{"transfer(address,uint256)"},
	}})
	data := "0xa9059cbb00000000000000000000000000000000000000000000000000000000000000dd0000000000000000000000000000000000000000000000000000000000000019"
	call := EVMCallParams{Contract: delegatedEVMTestContract, Data: data, Value: "25"}
	_, err := f.svc.ExecuteEVMContractMethod(context.Background(), ExecEVMContractMethodInput{
		Proof: evmProof(t, f, call),
		Call: EVMContractMethodCall{
			Contract: delegatedEVMTestContract,
			Method:   "transfer(address,uint256)",
			Args:     []any{delegatedEVMRecipient, "25"},
			Value:    "25",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wrapper wallettypes.MsgAgentExecDelegated
	decodeSoleTxMsg(t, f.broadcast.txBytes, "/dydxprotocol.agentwallet.MsgAgentExecDelegated", &wrapper)
	var inner wallettypes.MsgEVMCall
	if err := proto.Unmarshal(wrapper.InnerMsg.Value, &inner); err != nil {
		t.Fatal(err)
	}
	if got := "0x" + hex.EncodeToString(inner.Data); got != data {
		t.Errorf("calldata = %s, want %s", got, data)
	}
	if inner.Value != "25" {
		t.Errorf("value = %q, want 25", inner.Value)
	}
}

func TestExecuteEVMContractMethodRefusesUnconfiguredMethod(t *testing.T) {
	f := newFixture(t)
	f.svc.contracts = contractsByAddress([]ConfiguredContract{{
		ID: delegatedEVMTestContract, Address: delegatedEVMTestContract,
		Methods: []string{"transfer(address,uint256)"},
	}})
	_, err := f.svc.ExecuteEVMContractMethod(context.Background(), ExecEVMContractMethodInput{
		Call: EVMContractMethodCall{
			Contract: delegatedEVMTestContract,
			Method:   "approve(address,uint256)",
			Args:     []any{delegatedEVMRecipient, "25"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %v, want unconfigured method", err)
	}
	if f.broadcast.txBytes != nil {
		t.Error("a refused typed EVM call must not reach broadcast")
	}
}

func TestEncodeConfiguredMethodSupportsRouterAndBridgeArguments(t *testing.T) {
	const addressA = "0x0000000000000000000000000000000000000001"
	const addressB = "0x0000000000000000000000000000000000000002"
	const bytes32 = "0x00000000000000000000000000000000000000000000000000000000000000dd"
	tests := []struct {
		name   string
		method string
		args   []any
	}{
		{
			name:   "router address array",
			method: "swapExactTokensForTokens(uint256,uint256,address[],address,uint256)",
			args:   []any{"10", "9", []any{addressA, addressB}, delegatedEVMRecipient, "123"},
		},
		{
			name:   "bridge fixed bytes",
			method: "deposit(address,uint256,uint16,bytes32,bytes32)",
			args:   []any{addressA, "10", "2517", bytes32, bytes32},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := encodeConfiguredMethod(ConfiguredContract{
				ID: "test", Address: delegatedEVMTestContract, Methods: []string{tc.method},
			}, tc.method, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) < wallettypes.EVMSelectorLen || hex.EncodeToString(data[:wallettypes.EVMSelectorLen]) != hex.EncodeToString(crypto.Keccak256([]byte(tc.method))[:wallettypes.EVMSelectorLen]) {
				t.Errorf("method %q has wrong selector %x", tc.method, data[:wallettypes.EVMSelectorLen])
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
