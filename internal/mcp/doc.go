// Package mcp is the root of this repo's copy of svpchain-mcp's lib/mcp: the
// MCP tool handlers the A2A tool bridge dispatches into, plus the chain
// clients, tx builders, EVM contract bindings, indexer client, policy engine
// and auth stores they stand on.
//
// ★ This subtree was absorbed from github.com/svpchain/svpchain-mcp at tag
// v0.1.0 (commit a9ef41f), which used to be a module dependency of this repo.
// It follows svpchain-agent-core, absorbed into internal/ by da81cef for the
// same reason: this binary is meant to build from its own source plus tagged
// third-party modules, not from a sibling checkout's moving library.
//
// Unlike agent-core, svpchain-mcp is NOT retired — it still ships
// cmd/mcp-server, and svpchain-lending-agent and svpchain-research-agent still
// require it. So this is a fork, and upstream fixes no longer arrive on their
// own. That is the cost of the absorption; the mitigation is that everything
// here is kept diffable against the tag (see "Re-syncing" below).
//
// # What was pruned
//
// Very little, and for a narrower reason than the sibling perps agent's copy.
// This binary serves the EVM family — swap, bridge, ERC-20/721 and the raw EVM
// broadcast rail — so almost all of upstream is load-bearing here. Two things
// went:
//
//   - The Lendora surface (the lendora/ package, tools/lendora*.go,
//     builder/lendora.go, and the Deps.LendoraMarkets / EVMDeps.Lendora
//     fields). da81cef already removed its config schema and its registrations;
//     these were the last of it.
//   - tools/registry.go and tools/authgate.go — Register(srv *mcp.Server, …)
//     and the auth middleware helpers around it. That is the MCP-server
//     registration path; this binary serves A2A, and internal/toolbridge binds
//     these same handlers to A2A operations instead. Handlers and New, all
//     anything here used registry.go for, live in tools/handlers.go.
//
// Note what did NOT go: the perps tool families (market data, account, trading,
// funds, Cosmos broadcast). This binary does not register them — see
// internal/wire's TestEVMProfileServesExactlyItsFamilies — but internal/
// toolbridge deliberately keeps their Register* functions, because the shared
// dispatch and delegated-read tests exercise the credential machinery against
// them. Pruning the handlers would take those tests with it.
//
// # Files that are not verbatim
//
// Everything here is byte-identical to the tag except its import paths, apart
// from tools/handlers.go (rescued from the deleted registry.go, marked at its
// definition), tools/deps.go (the two Lendora fields), and the comments —
// package docs that described a server this binary is not, and
// cross-references that named the old lib/mcp path.
//
// The tree is also gofmt-clean, which upstream's is not (18 files differ there,
// mostly import ordering and struct-tag alignment). That is deliberate: this
// repo's `make fmt` runs gofmt -w over everything, so leaving them unformatted
// would mean the next fmt run silently rewrote a third of the subtree.
//
// # Re-syncing
//
// To compare a file against its upstream original:
//
//	git -C ../svpchain-mcp show v0.1.0:lib/mcp/<pkg>/<file>.go | gofmt | diff - internal/mcp/<pkg>/<file>.go
//
// The only expected hunks are the import block and the exceptions listed above.
// internal/wire mirrors upstream's cmd/mcp-server wiring by hand; drift between
// the two is a bug in whichever copied last.
//
// # One hazard worth naming
//
// auth/recover.go, mcpcodec/codec.go and signer/signer.go each have an init()
// that calls appconfig.SetAddressPrefixes(), setting the svp bech32 prefix
// process-wide. All three are retained, so it fires. A future prune that drops
// every one of them from a binary's import graph would silently change every
// sdk.AccAddress string in that binary.
package mcp
