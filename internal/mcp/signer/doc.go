// Package signer turns a TxPayload produced by the remote MCP server's
// build_* tools into a SignedTx ready for broadcast_signed_tx. It owns
// the eth_secp256k1 + SIGN_MODE_DIRECT signing path and the cross-checks
// (signer address matches the loaded key, payload version matches the
// supported one).
//
// The production stdio signer MCP server lives in its own repo
// (svpchain-signer-mcp), so remote callers sign their own payloads. This
// package is retained for internal/operator, which uses ParsePrivKey and
// DeriveAddress to load the operator key, and because signer_test.go is the
// only executable spec of the sign-byte layout both sides agree on.
//
// The package's init() sets the svp bech32 prefix so every sdk.AccAddress
// stringification (notably in DeriveAddress and the signer-address
// cross-check) matches the chain. Importing this package is sufficient —
// no caller needs its own blank import of app/config.
package signer
