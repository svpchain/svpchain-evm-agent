package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListEVMAssetsReturnsConfiguredAliasesInOrder(t *testing.T) {
	h := &Handlers{
		ChainID: "svp-2517-1",
		Deps: Deps{EVM: EVMDeps{Assets: map[string]ConfiguredEVMAsset{
			"usdc": {Address: "0x0000000000000000000000000000000000000002", Decimals: 6},
			"weth": {Address: "0x0000000000000000000000000000000000000001", Decimals: 18},
		}}},
	}

	_, out, err := h.ListEVMAssets(context.Background(), nil, ListEVMAssetsInput{})
	require.NoError(t, err)
	require.Equal(t, "svp-2517-1", out.ChainID)
	require.Equal(t, []EVMAssetDTO{
		{ID: "usdc", Address: "0x0000000000000000000000000000000000000002", Decimals: 6},
		{ID: "weth", Address: "0x0000000000000000000000000000000000000001", Decimals: 18},
	}, out.Assets)
}
