package tools

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultSwapPairPageSize uint64 = 20
	maxSwapPairPageSize     uint64 = 100
)

// ListSwapPairsInput pages through the configured Uniswap V2 Factory. Pair
// addresses are chain state, never deployment configuration.
type ListSwapPairsInput struct {
	Offset uint64 `json:"offset,omitempty" jsonschema:"zero-based Factory allPairs offset; defaults to 0"`
	Limit  uint64 `json:"limit,omitempty" jsonschema:"pairs to return (1-100; defaults to 20)"`
}

type SwapPairToken struct {
	Address       string `json:"address"`
	Symbol        string `json:"symbol,omitempty"`
	Decimals      *uint8 `json:"decimals,omitempty"`
	MetadataError string `json:"metadata_error,omitempty"`
}

type SwapPair struct {
	Address           string        `json:"address"`
	Token0            SwapPairToken `json:"token0"`
	Token1            SwapPairToken `json:"token1"`
	Reserve0          string        `json:"reserve0"`
	Reserve1          string        `json:"reserve1"`
	HasLiquidity      bool          `json:"has_liquidity"`
	DirectPath        []string      `json:"direct_path"`
	SupportsNativeSVP bool          `json:"supports_native_svp"`
}

type ListSwapPairsOutput struct {
	Factory    string     `json:"factory"`
	TotalPairs uint64     `json:"total_pairs"`
	Offset     uint64     `json:"offset"`
	Limit      uint64     `json:"limit"`
	NextOffset *uint64    `json:"next_offset,omitempty"`
	Pairs      []SwapPair `json:"pairs"`
}

// ListSwapPairs discovers the configured Factory's current pairs and their
// reserves through eth_call. It is public read-only data: quote_swap still
// performs the final router quote before a user decides to swap.
func (h *Handlers) ListSwapPairs(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in ListSwapPairsInput,
) (*mcp.CallToolResult, ListSwapPairsOutput, error) {
	factory := h.Deps.EVM.UniswapFactory
	if h.Deps.Chain.EVM == nil {
		return nil, ListSwapPairsOutput{}, userErrf("EVM is not enabled on this server (no evm_rpc_url configured)")
	}
	if factory == nil {
		return nil, ListSwapPairsOutput{}, userErrf("swap pair discovery is not enabled on this server (no evm.swap.factory_addr configured)")
	}

	limit := in.Limit
	if limit == 0 {
		limit = defaultSwapPairPageSize
	}
	if limit > maxSwapPairPageSize {
		return nil, ListSwapPairsOutput{}, userErrf("limit must be between 1 and %d", maxSwapPairPageSize)
	}

	lengthCall, err := factory.PackAllPairsLength()
	if err != nil {
		return nil, ListSwapPairsOutput{}, err
	}
	lengthOut, err := h.evmCall(ctx, factory.Address(), lengthCall)
	if err != nil {
		return nil, ListSwapPairsOutput{}, fmt.Errorf("read Uniswap V2 Factory allPairsLength: %w", err)
	}
	total, err := factory.UnpackAllPairsLength(lengthOut)
	if err != nil {
		return nil, ListSwapPairsOutput{}, err
	}

	out := ListSwapPairsOutput{
		Factory:    factory.Address().Hex(),
		TotalPairs: total,
		Offset:     in.Offset,
		Limit:      limit,
		Pairs:      []SwapPair{},
	}
	if in.Offset >= total {
		return nil, out, nil
	}
	end := in.Offset + limit
	if end < in.Offset || end > total {
		end = total
	}

	tokenCache := map[common.Address]SwapPairToken{}
	for index := in.Offset; index < end; index++ {
		pair, err := h.readSwapPair(ctx, factory, index, tokenCache)
		if err != nil {
			return nil, ListSwapPairsOutput{}, err
		}
		out.Pairs = append(out.Pairs, pair)
	}
	if end < total {
		next := end
		out.NextOffset = &next
	}
	return nil, out, nil
}

func (h *Handlers) readSwapPair(
	ctx context.Context,
	factory interface {
		PackAllPairs(*big.Int) ([]byte, error)
		UnpackAllPairs([]byte) (common.Address, error)
		PackToken0() ([]byte, error)
		PackToken1() ([]byte, error)
		UnpackToken0([]byte) (common.Address, error)
		UnpackToken1([]byte) (common.Address, error)
		PackGetReserves() ([]byte, error)
		UnpackGetReserves([]byte) (*big.Int, *big.Int, error)
		PackTokenSymbol() ([]byte, error)
		PackTokenDecimals() ([]byte, error)
		UnpackTokenSymbol([]byte) (string, error)
		UnpackTokenDecimals([]byte) (uint8, error)
	},
	index uint64,
	tokenCache map[common.Address]SwapPairToken,
) (SwapPair, error) {
	pairCall, err := factory.PackAllPairs(new(big.Int).SetUint64(index))
	if err != nil {
		return SwapPair{}, err
	}
	pairOut, err := h.evmCall(ctx, h.Deps.EVM.UniswapFactory.Address(), pairCall)
	if err != nil {
		return SwapPair{}, fmt.Errorf("read Factory pair %d: %w", index, err)
	}
	pairAddr, err := factory.UnpackAllPairs(pairOut)
	if err != nil {
		return SwapPair{}, err
	}
	if pairAddr == (common.Address{}) {
		return SwapPair{}, fmt.Errorf("Factory pair %d is the zero address", index)
	}

	token0, err := h.readPairToken(ctx, factory, pairAddr, true)
	if err != nil {
		return SwapPair{}, fmt.Errorf("read Pair %s token0: %w", pairAddr.Hex(), err)
	}
	token1, err := h.readPairToken(ctx, factory, pairAddr, false)
	if err != nil {
		return SwapPair{}, fmt.Errorf("read Pair %s token1: %w", pairAddr.Hex(), err)
	}
	reserveCall, err := factory.PackGetReserves()
	if err != nil {
		return SwapPair{}, err
	}
	reserveOut, err := h.evmCall(ctx, pairAddr, reserveCall)
	if err != nil {
		return SwapPair{}, fmt.Errorf("read Pair %s reserves: %w", pairAddr.Hex(), err)
	}
	reserve0, reserve1, err := factory.UnpackGetReserves(reserveOut)
	if err != nil {
		return SwapPair{}, err
	}

	token0Info := h.readSwapPairTokenMetadata(ctx, factory, token0, tokenCache)
	token1Info := h.readSwapPairTokenMetadata(ctx, factory, token1, tokenCache)
	wsvp := common.Address{}
	if h.Deps.EVM.Uniswap != nil {
		wsvp = h.Deps.EVM.Uniswap.WSVP()
	}
	return SwapPair{
		Address:           pairAddr.Hex(),
		Token0:            token0Info,
		Token1:            token1Info,
		Reserve0:          reserve0.String(),
		Reserve1:          reserve1.String(),
		HasLiquidity:      reserve0.Sign() > 0 && reserve1.Sign() > 0,
		DirectPath:        []string{token0.Hex(), token1.Hex()},
		SupportsNativeSVP: token0 == wsvp || token1 == wsvp,
	}, nil
}

func (h *Handlers) readPairToken(
	ctx context.Context,
	factory interface {
		PackToken0() ([]byte, error)
		PackToken1() ([]byte, error)
		UnpackToken0([]byte) (common.Address, error)
		UnpackToken1([]byte) (common.Address, error)
	},
	pair common.Address,
	first bool,
) (common.Address, error) {
	var call []byte
	var err error
	if first {
		call, err = factory.PackToken0()
	} else {
		call, err = factory.PackToken1()
	}
	if err != nil {
		return common.Address{}, err
	}
	output, err := h.evmCall(ctx, pair, call)
	if err != nil {
		return common.Address{}, err
	}
	if first {
		return factory.UnpackToken0(output)
	}
	return factory.UnpackToken1(output)
}

func (h *Handlers) readSwapPairTokenMetadata(
	ctx context.Context,
	factory interface {
		PackTokenSymbol() ([]byte, error)
		PackTokenDecimals() ([]byte, error)
		UnpackTokenSymbol([]byte) (string, error)
		UnpackTokenDecimals([]byte) (uint8, error)
	},
	address common.Address,
	cache map[common.Address]SwapPairToken,
) SwapPairToken {
	if cached, ok := cache[address]; ok {
		return cached
	}
	info := SwapPairToken{Address: address.Hex()}
	var problems []string
	if call, err := factory.PackTokenSymbol(); err != nil {
		problems = append(problems, err.Error())
	} else if output, err := h.evmCall(ctx, address, call); err != nil {
		problems = append(problems, "read symbol: "+err.Error())
	} else if symbol, err := factory.UnpackTokenSymbol(output); err != nil {
		problems = append(problems, err.Error())
	} else {
		info.Symbol = symbol
	}
	if call, err := factory.PackTokenDecimals(); err != nil {
		problems = append(problems, err.Error())
	} else if output, err := h.evmCall(ctx, address, call); err != nil {
		problems = append(problems, "read decimals: "+err.Error())
	} else if decimals, err := factory.UnpackTokenDecimals(output); err != nil {
		problems = append(problems, err.Error())
	} else {
		info.Decimals = &decimals
	}
	if len(problems) > 0 {
		info.MetadataError = strings.Join(problems, "; ")
	}
	cache[address] = info
	return info
}
