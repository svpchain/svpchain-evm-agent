package tools

import (
	"strings"

	assettypes "github.com/dydxprotocol/v4-chain/protocol/x/assets/types"
)

// This file is the canonical "transfer out" asset registry. Funds leave a
// wallet through two rails — x/bank sends and EVM transfers — and a
// single end-user-facing token (e.g. usdc, which is both the x/bank denom
// erc20/usdc and an ERC-20) can leave through either. The daily cap
// (limits/transferout.go) is keyed by owner wallet and symbol, so each rail
// resolves its moved asset back to a symbol here and accumulates against one
// shared per-owner total.
//
// Agents set caps in these symbols (svp, usdc), not raw on-chain
// identifiers (asvp, erc20/usdc, 0x…), which end users don't recognise.

// nativeBankDenom is the x/bank denom of the native gas token (atto-SVP). Kept
// next to knownDenoms' "asvp" entry in account.go; named here so the registry
// reads in symbol terms.
const nativeBankDenom = "asvp"

// assetSymbol describes one token and the on-chain identifiers it can leave the
// wallet through. Decimals are hardcoded so cap-config parsing doesn't need EVM
// connectivity at startup; TestTransferOutAssets_DecimalsKnown guards the
// values, and they can be re-confirmed against an on-chain decimals() read.
type assetSymbol struct {
	symbol    string
	bankDenom string
	native    bool // true for native SVP (matches EVM value transfers)
	decimals  int64
}

// transferOutAssets covers the bank-denom assets with stable symbols. ERC-20
// contracts are discovered dynamically, so they have no static cap symbol.
var transferOutAssets = []assetSymbol{
	{symbol: "svp", bankDenom: nativeBankDenom, native: true, decimals: 18},
	{symbol: "usdc", bankDenom: assettypes.UusdcDenom, decimals: 6},
}

// assetForSymbol looks up a registry entry by (case-insensitive) symbol.
func assetForSymbol(symbol string) (assetSymbol, bool) {
	key := strings.ToLower(strings.TrimSpace(symbol))
	for _, a := range transferOutAssets {
		if a.symbol == key {
			return a, true
		}
	}
	return assetSymbol{}, false
}

// symbolForDenom maps an x/bank denom (asvp, erc20/usdc) to its cap symbol.
func symbolForDenom(denom string) (string, bool) {
	for _, a := range transferOutAssets {
		if a.bankDenom != "" && a.bankDenom == denom {
			return a.symbol, true
		}
	}
	return "", false
}

// symbolForNative returns the cap symbol for native-value (SVP) transfers.
func symbolForNative() (string, bool) {
	for _, a := range transferOutAssets {
		if a.native {
			return a.symbol, true
		}
	}
	return "", false
}
