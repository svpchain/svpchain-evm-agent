package tools

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-evm-agent/internal/mcp/builder"
)

var (
	discoveryFactory = common.HexToAddress("0x000000000000000000000000000000000000faca")
	discoveryPairA   = common.HexToAddress("0x0000000000000000000000000000000000000a01")
	discoveryPairB   = common.HexToAddress("0x0000000000000000000000000000000000000b01")
	discoveryTokenA  = common.HexToAddress("0x00000000000000000000000000000000000000a1")
	discoveryTokenB  = common.HexToAddress("0x00000000000000000000000000000000000000b1")
	discoveryTokenC  = common.HexToAddress("0x00000000000000000000000000000000000000c1")
)

type pairDiscoveryEVM struct{}

func (pairDiscoveryEVM) CallContract(_ context.Context, msg ethereum.CallMsg) ([]byte, error) {
	if msg.To == nil || len(msg.Data) < 4 {
		return nil, errors.New("malformed eth_call")
	}
	selector := msg.Data[:4]
	sig := func(method string) []byte { return crypto.Keccak256([]byte(method))[:4] }
	addressWord := func(address common.Address) []byte { return common.LeftPadBytes(address.Bytes(), 32) }
	uintWord := func(value int64) []byte { return common.LeftPadBytes(big.NewInt(value).Bytes(), 32) }
	if *msg.To == discoveryFactory {
		switch {
		case bytes.Equal(selector, sig("allPairsLength()")):
			return uintWord(2), nil
		case bytes.Equal(selector, sig("allPairs(uint256)")):
			index := new(big.Int).SetBytes(msg.Data[len(msg.Data)-32:]).Uint64()
			if index == 0 {
				return addressWord(discoveryPairA), nil
			}
			if index == 1 {
				return addressWord(discoveryPairB), nil
			}
		}
	}
	pairToken0 := common.Address{}
	pairToken1 := common.Address{}
	reserve0, reserve1 := int64(0), int64(0)
	switch *msg.To {
	case discoveryPairA:
		pairToken0, pairToken1, reserve0, reserve1 = discoveryTokenA, discoveryTokenB, 1200, 3400
	case discoveryPairB:
		pairToken0, pairToken1 = discoveryTokenB, discoveryTokenC
	default:
		return pairDiscoveryTokenRead(*msg.To, selector, sig)
	}
	switch {
	case bytes.Equal(selector, sig("token0()")):
		return addressWord(pairToken0), nil
	case bytes.Equal(selector, sig("token1()")):
		return addressWord(pairToken1), nil
	case bytes.Equal(selector, sig("getReserves()")):
		return append(append(uintWord(reserve0), uintWord(reserve1)...), uintWord(1)...), nil
	default:
		return nil, errors.New("unknown Pair method")
	}
}

func pairDiscoveryTokenRead(address common.Address, selector []byte, sig func(string) []byte) ([]byte, error) {
	var symbol string
	var decimals int64
	switch address {
	case discoveryTokenA:
		symbol, decimals = "AAA", 6
	case discoveryTokenB:
		symbol, decimals = "BBB", 18
	case discoveryTokenC:
		symbol, decimals = "CCC", 8
	default:
		return nil, errors.New("unknown token")
	}
	if bytes.Equal(selector, sig("decimals()")) {
		return common.LeftPadBytes(big.NewInt(decimals).Bytes(), 32), nil
	}
	if bytes.Equal(selector, sig("symbol()")) {
		typ, err := abi.NewType("string", "", nil)
		if err != nil {
			return nil, err
		}
		return abi.Arguments{{Type: typ}}.Pack(symbol)
	}
	return nil, errors.New("unknown token method")
}

func (pairDiscoveryEVM) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	return 0, nil
}
func (pairDiscoveryEVM) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error) { return 0, nil }
func (pairDiscoveryEVM) SuggestGasTipCap(context.Context) (*big.Int, error)            { return nil, nil }
func (pairDiscoveryEVM) BaseFee(context.Context) (*big.Int, error)                     { return nil, nil }
func (pairDiscoveryEVM) ChainID(context.Context) (*big.Int, error)                     { return big.NewInt(2517), nil }
func (pairDiscoveryEVM) BlockNumber(context.Context) (uint64, error)                   { return 0, nil }
func (pairDiscoveryEVM) SendTransaction(context.Context, *ethtypes.Transaction) (string, error) {
	return "", nil
}
func (pairDiscoveryEVM) TransactionReceipt(context.Context, common.Hash) (*ethtypes.Receipt, error) {
	return nil, nil
}

func pairDiscoveryHandlers(t *testing.T) *Handlers {
	t.Helper()
	uni, err := builder.NewUniswapV2(discoveryTokenB, discoveryTokenB)
	require.NoError(t, err)
	factory, err := builder.NewUniswapV2Factory(discoveryFactory)
	require.NoError(t, err)
	return &Handlers{Deps: Deps{
		Chain: ChainDeps{EVM: pairDiscoveryEVM{}},
		EVM:   EVMDeps{Uniswap: uni, UniswapFactory: factory},
	}}
}

func TestListSwapPairsReadsFactoryStateInPages(t *testing.T) {
	h := pairDiscoveryHandlers(t)

	_, first, err := h.ListSwapPairs(context.Background(), nil, ListSwapPairsInput{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, discoveryFactory.Hex(), first.Factory)
	require.Equal(t, uint64(2), first.TotalPairs)
	require.Equal(t, uint64(1), *first.NextOffset)
	require.Len(t, first.Pairs, 1)
	require.Equal(t, discoveryPairA.Hex(), first.Pairs[0].Address)
	require.Equal(t, "AAA", first.Pairs[0].Token0.Symbol)
	require.Equal(t, "BBB", first.Pairs[0].Token1.Symbol)
	require.Equal(t, uint8(6), *first.Pairs[0].Token0.Decimals)
	require.True(t, first.Pairs[0].HasLiquidity)
	require.True(t, first.Pairs[0].SupportsNativeSVP)

	_, second, err := h.ListSwapPairs(context.Background(), nil, ListSwapPairsInput{Offset: 1, Limit: 10})
	require.NoError(t, err)
	require.Nil(t, second.NextOffset)
	require.Len(t, second.Pairs, 1)
	require.Equal(t, discoveryPairB.Hex(), second.Pairs[0].Address)
	require.Equal(t, "BBB", second.Pairs[0].Token0.Symbol)
	require.Equal(t, "CCC", second.Pairs[0].Token1.Symbol)
	require.False(t, second.Pairs[0].HasLiquidity)
}

func TestListSwapPairsRejectsOversizedPages(t *testing.T) {
	_, _, err := pairDiscoveryHandlers(t).ListSwapPairs(context.Background(), nil, ListSwapPairsInput{Limit: maxSwapPairPageSize + 1})
	require.ErrorContains(t, err, "limit must be between")
}
