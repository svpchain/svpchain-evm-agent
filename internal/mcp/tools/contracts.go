package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListEVMContractsInput is intentionally empty. The directory is bounded by
// [[evm.contract]] configuration and never scans or enumerates chain state.
type ListEVMContractsInput struct{}

// EVMContractDTO is one operator-curated contract directory entry. Its methods
// describe the typed-call whitelist, but the entry is not a delegation grant.
type EVMContractDTO struct {
	ID          string   `json:"id"`
	Address     string   `json:"address"`
	Kind        string   `json:"kind,omitempty"`
	Symbol      string   `json:"symbol,omitempty"`
	Decimals    int64    `json:"decimals,omitempty"`
	Methods     []string `json:"methods,omitempty"`
	Description string   `json:"description,omitempty"`
}

type ListEVMContractsOutput struct {
	ChainID       string           `json:"chain_id"`
	Contracts     []EVMContractDTO `json:"contracts"`
	Authorization string           `json:"authorization"`
}

// ListEVMContracts returns only the deployment's configured contract aliases.
// A listed address still needs to appear in the user's root delegation and
// SVP-DT task credential before execute_evm_call may invoke it.
func (h *Handlers) ListEVMContracts(
	_ context.Context,
	_ *mcp.CallToolRequest,
	_ ListEVMContractsInput,
) (*mcp.CallToolResult, ListEVMContractsOutput, error) {
	contracts := make([]EVMContractDTO, 0, len(h.Deps.EVM.Contracts))
	for _, contract := range h.Deps.EVM.Contracts {
		contracts = append(contracts, EVMContractDTO{
			ID:          contract.ID,
			Address:     contract.Address,
			Kind:        contract.Kind,
			Symbol:      contract.Symbol,
			Decimals:    contract.Decimals,
			Methods:     append([]string(nil), contract.Methods...),
			Description: contract.Description,
		})
	}
	return nil, ListEVMContractsOutput{
		ChainID:       h.ChainID,
		Contracts:     contracts,
		Authorization: "Discovery only: a listed contract is not delegated until its lowercase address is included in both the root delegation and task credential contracts limits.",
	}, nil
}
