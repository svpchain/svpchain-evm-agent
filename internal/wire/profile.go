package wire

import (
	"github.com/svpchain/svpchain-evm-agent/internal/mcp/tools"
	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
)

// Profile selects which operation families a binary registers.
//
// This binary ships exactly one — EVMProfile — but the indirection stays
// because it is what keeps registration a named, testable composition rather
// than a hardcoded sequence inside BuildProfile. The coverage test reads it to
// prove nothing left in toolbridge is unreachable.
//
// It used to carry BuildEVM / BuildLendora / RunMarkets flags, gating dependency
// construction as well as registration, so an agent handed the shared full
// config would not dial endpoints for families it never served. With only the
// EVM surface left there is nothing to gate: this binary always builds the EVM
// client, and never the Lendora or perps-markets machinery.
type Profile struct {
	Name string

	// Register composes the binary's operation registry.
	Register func(r *toolbridge.Registry, h *tools.Handlers)
}

// RegisterCallerSignedStack adds the support tools required by a non-custodial
// remote service. The caller authenticates with a wallet signature, then signs
// every returned EVM payload locally before asking this service to broadcast it.
func RegisterCallerSignedStack(r *toolbridge.Registry, h *tools.Handlers) {
	r.RegisterAuth(h)
	// Self-description, so an A2A caller can discover this profile's surface
	// without an MCP connection to the same handlers.
	r.RegisterMeta()
}

// EVMProfile serves caller-signed EVM DeFi: discovery, builds, and the raw
// EVM broadcast rail. It never holds or uses a caller's private key.
var EVMProfile = Profile{
	Name: "evm",
	Register: func(r *toolbridge.Registry, h *tools.Handlers) {
		r.RegisterEVMBroadcast(h)
		RegisterCallerSignedStack(r, h)
	},
}
