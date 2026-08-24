package tools

import (
	"context"
	"testing"
)

func TestListEVMContractsReturnsConfiguredDirectoryWithoutAuthorization(t *testing.T) {
	h := &Handlers{
		ChainID: "svp-2517-1",
		Deps: Deps{EVM: EVMDeps{Contracts: []ConfiguredEVMContract{{
			ID: "usdc", Address: "0x000000000000000000000000000000000000c07e",
			Kind: "erc20", Symbol: "USDC", Decimals: 6,
			Methods: []string{"transfer(address,uint256)", "approve(address,uint256)"},
		}}}},
	}

	_, out, err := h.ListEVMContracts(context.Background(), nil, ListEVMContractsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.ChainID != "svp-2517-1" || len(out.Contracts) != 1 || out.Contracts[0].ID != "usdc" {
		t.Fatalf("output = %+v", out)
	}
	if out.Contracts[0].Address != "0x000000000000000000000000000000000000c07e" {
		t.Errorf("address = %q", out.Contracts[0].Address)
	}
	if out.Authorization == "" {
		t.Error("authorization boundary must be stated")
	}
}

func TestListEVMContractsAllowsAnEmptyDirectory(t *testing.T) {
	_, out, err := (&Handlers{ChainID: "svp-2517-1"}).ListEVMContracts(context.Background(), nil, ListEVMContractsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Contracts == nil || len(out.Contracts) != 0 {
		t.Errorf("contracts = %#v, want empty array", out.Contracts)
	}
}
