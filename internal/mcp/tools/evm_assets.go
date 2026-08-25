package tools

import (
	"context"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListEVMAssetsInput is intentionally empty. Assets are a small stable alias
// table for common ERC-20s, separate from the Factory's dynamic pair discovery.
type ListEVMAssetsInput struct{}

type EVMAssetDTO struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	Decimals int64  `json:"decimals"`
}

type ListEVMAssetsOutput struct {
	ChainID string        `json:"chain_id"`
	Assets  []EVMAssetDTO `json:"assets"`
}

// ListEVMAssets returns the deployment's stable ERC-20 aliases. They are
// convenience names only: callers can still use any ERC-20 address directly.
func (h *Handlers) ListEVMAssets(
	_ context.Context,
	_ *mcp.CallToolRequest,
	_ ListEVMAssetsInput,
) (*mcp.CallToolResult, ListEVMAssetsOutput, error) {
	ids := make([]string, 0, len(h.Deps.EVM.Assets))
	for id := range h.Deps.EVM.Assets {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	assets := make([]EVMAssetDTO, 0, len(ids))
	for _, id := range ids {
		asset := h.Deps.EVM.Assets[id]
		assets = append(assets, EVMAssetDTO{
			ID:       id,
			Address:  asset.Address,
			Decimals: asset.Decimals,
		})
	}
	return nil, ListEVMAssetsOutput{ChainID: h.ChainID, Assets: assets}, nil
}
