package tools

import (
	"bytes"
	"context"
	"math/big"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"

	"github.com/svpchain/svpchain-evm-agent/internal/mcp/builder"
	"github.com/svpchain/svpchain-evm-agent/internal/mcp/policy"
)

var (
	swapWSVP   = common.HexToAddress("0x2222222222222222222222222222222222222222")
	swapTokenA = common.HexToAddress("0xAAaAAAaaAAAAAAAaaAAAAaAAaAAaaAaaAaAAAAaa")
	swapTokenB = common.HexToAddress("0xBbBBBbbBbBbBBbBbBBBBBBbBbBbBBBBBbbBBBbbB")
)

func TestParseSwapToken_Native(t *testing.T) {
	for _, in := range []string{"", "native", "SVP", "svp", " native ", "0x0000000000000000000000000000000000000000"} {
		addr, native, err := parseSwapToken(in)
		require.NoError(t, err, "input %q", in)
		require.True(t, native, "input %q should be native", in)
		require.Equal(t, common.Address{}, addr)
	}
}

func TestParseSwapToken_ERC20(t *testing.T) {
	addr, native, err := parseSwapToken(swapTokenA.Hex())
	require.NoError(t, err)
	require.False(t, native)
	require.Equal(t, swapTokenA, addr)
}

func TestParseSwapToken_KnownSymbol(t *testing.T) {
	cases := map[string]common.Address{
		"usdv": common.HexToAddress("0x013a61E622e6ABFCaB64F52D274C3Fc0aA37f951"),
		"usdc": common.HexToAddress("0x732F6Ea7AfD5EdC02e7ba052075dd0780e285489"),
	}
	for sym, want := range cases {
		for _, in := range []string{sym, strings.ToUpper(sym), " " + sym + " "} {
			addr, native, err := parseSwapToken(in)
			require.NoError(t, err, "input %q", in)
			require.False(t, native, "input %q should resolve to an ERC-20", in)
			require.Equal(t, want, addr, "input %q", in)
		}
	}
}

func TestParseSwapToken_Invalid(t *testing.T) {
	for _, in := range []string{"0x123", "not-an-address", "0xZZZ"} {
		_, _, err := parseSwapToken(in)
		require.Error(t, err, "input %q should be rejected", in)
	}
}

func TestResolveSwapPlan(t *testing.T) {
	t.Run("erc20->erc20", func(t *testing.T) {
		p, err := resolveSwapPlan(swapTokenA, false, swapTokenB, false, swapWSVP)
		require.NoError(t, err)
		require.Equal(t, kindTokensForTokens, p.kind)
		require.Equal(t, []common.Address{swapTokenA, swapTokenB}, p.path)
	})
	t.Run("native->erc20 routes through WSVP", func(t *testing.T) {
		p, err := resolveSwapPlan(common.Address{}, true, swapTokenB, false, swapWSVP)
		require.NoError(t, err)
		require.Equal(t, kindSVPForTokens, p.kind)
		require.Equal(t, []common.Address{swapWSVP, swapTokenB}, p.path)
	})
	t.Run("erc20->native routes through WSVP", func(t *testing.T) {
		p, err := resolveSwapPlan(swapTokenA, false, common.Address{}, true, swapWSVP)
		require.NoError(t, err)
		require.Equal(t, kindTokensForSVP, p.kind)
		require.Equal(t, []common.Address{swapTokenA, swapWSVP}, p.path)
	})
	t.Run("native->native rejected", func(t *testing.T) {
		_, err := resolveSwapPlan(common.Address{}, true, common.Address{}, true, swapWSVP)
		require.Error(t, err)
	})
	t.Run("same token rejected", func(t *testing.T) {
		_, err := resolveSwapPlan(swapTokenA, false, swapTokenA, false, swapWSVP)
		require.Error(t, err)
	})
}

func TestApplySlippage(t *testing.T) {
	out := big.NewInt(1000)

	min, err := applySlippage(out, 50) // 0.5%
	require.NoError(t, err)
	require.Equal(t, "995", min.String())

	min, err = applySlippage(out, 0) // no slippage
	require.NoError(t, err)
	require.Equal(t, "1000", min.String())

	min, err = applySlippage(out, 10000-1) // 99.99%
	require.NoError(t, err)
	require.Equal(t, "0", min.String())

	_, err = applySlippage(out, 10000) // 100% is rejected (would allow zero-out)
	require.Error(t, err)
	_, err = applySlippage(out, -1)
	require.Error(t, err)
}

func TestTokenLabel(t *testing.T) {
	require.Equal(t, "native", tokenLabel(true, common.Address{}))
	require.Equal(t, swapTokenA.Hex(), tokenLabel(false, swapTokenA))
	// A known alias renders as its upper-cased symbol, not the raw address.
	usdv := common.HexToAddress("0x013a61E622e6ABFCaB64F52D274C3Fc0aA37f951")
	require.Equal(t, "USDV", tokenLabel(false, usdv))
	usdc := common.HexToAddress("0x732F6Ea7AfD5EdC02e7ba052075dd0780e285489")
	require.Equal(t, "USDC", tokenLabel(false, usdc))
}

func TestAddrsToHex(t *testing.T) {
	require.Equal(t,
		[]string{swapTokenA.Hex(), swapWSVP.Hex()},
		addrsToHex([]common.Address{swapTokenA, swapWSVP}),
	)
}

// mockSwapEVM is a chain.EVMClient stub for the build_swap allowance pre-check:
// CallContract dispatches on the 4-byte selector to answer decimals() and
// allowance() with fixed values; nothing else is exercised on the short-allowance
// path (it returns before quoting/assembling).
type mockSwapEVM struct {
	decimals  uint8
	allowance *big.Int
}

func (m *mockSwapEVM) CallContract(_ context.Context, msg ethereum.CallMsg) ([]byte, error) {
	switch {
	case bytes.Equal(msg.Data[:4], crypto.Keccak256([]byte("decimals()"))[:4]):
		return common.LeftPadBytes(big.NewInt(int64(m.decimals)).Bytes(), 32), nil
	case bytes.Equal(msg.Data[:4], crypto.Keccak256([]byte("allowance(address,address)"))[:4]):
		a := m.allowance
		if a == nil {
			a = big.NewInt(0)
		}
		return common.LeftPadBytes(a.Bytes(), 32), nil
	}
	return nil, nil
}

func (m *mockSwapEVM) PendingNonceAt(context.Context, common.Address) (uint64, error) { return 0, nil }
func (m *mockSwapEVM) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)  { return 0, nil }
func (m *mockSwapEVM) SuggestGasTipCap(context.Context) (*big.Int, error)             { return nil, nil }
func (m *mockSwapEVM) BaseFee(context.Context) (*big.Int, error)                      { return nil, nil }
func (m *mockSwapEVM) ChainID(context.Context) (*big.Int, error)                      { return nil, nil }
func (m *mockSwapEVM) BlockNumber(context.Context) (uint64, error)                    { return 0, nil }
func (m *mockSwapEVM) SendTransaction(context.Context, *ethtypes.Transaction) (string, error) {
	return "", nil
}
func (m *mockSwapEVM) TransactionReceipt(context.Context, common.Hash) (*ethtypes.Receipt, error) {
	return nil, nil
}

// swapHandlers wires a *Handlers authorized for build_swap against the given
// home-chain mock: a real UniswapV2 binding plus policy/rate-limit so authorize
// passes. The assembler is present only so requireSwap's nil-check succeeds — the
// short-allowance path returns before it is used.
func swapHandlers(t *testing.T, evm *mockSwapEVM) (*Handlers, context.Context) {
	t.Helper()
	const tenantID = "t1"
	uni, err := builder.NewUniswapV2(swapWSVP, swapWSVP)
	require.NoError(t, err)
	h := &Handlers{Deps: Deps{
		Chain: ChainDeps{EVM: evm},
		EVM: EVMDeps{
			Uniswap:   uni,
			Assembler: builder.NewEVMAssembler(evm),
		},
		Policy:    policy.NewEngine([]policy.TenantPolicy{{TenantID: tenantID, Owner: testTxOwner}}),
		RateLimit: policy.NewRateLimiter(0, 0),
	}}
	ctx := WithTenant(context.Background(), TenantContext{TenantID: tenantID, Owner: testTxOwner})
	return h, ctx
}

func TestBuildSwap_AllowanceShort(t *testing.T) {
	// A token-input swap whose router allowance is zero. A short allowance is NOT
	// an error: build_swap returns successfully with the structured approval step
	// and no payload, so an agent that halts on tool errors can read it, approve,
	// and retry.
	h, ctx := swapHandlers(t, &mockSwapEVM{decimals: 6, allowance: big.NewInt(0)})

	_, out, err := h.BuildSwap(ctx, nil, BuildSwapInput{
		TokenIn:  "usdv", // ERC-20 input -> allowance is checked
		TokenOut: "native",
		AmountIn: "100",
		ClientID: "cid-swap-1",
	})
	require.NoError(t, err)
	require.Empty(t, out.Payload.EVMChainID) // no ready-to-sign payload when approval is pending
	require.NotNil(t, out.ApprovalRequired)
	require.Equal(t, "build_token_approval", out.ApprovalRequired.Tool)
	require.Equal(t, "build_swap", out.ApprovalRequired.RetryTool)
	require.Equal(t, "100", out.ApprovalRequired.MinAmount)
	usdv := common.HexToAddress("0x013a61E622e6ABFCaB64F52D274C3Fc0aA37f951").Hex()
	require.Equal(t, usdv, out.ApprovalRequired.Token)
	require.Contains(t, out.ApprovalRequired.Message, "build_token_approval")
}
