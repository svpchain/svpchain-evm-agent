// Package tools holds the MCP tool handlers. evm.go / swap.go / erc.go /
// bridge.go carry this binary's own surface; market.go / account.go /
// trade.go / funds.go / cross.go carry the perps families it does not register
// but keeps (see internal/mcp/doc.go); the _v021/_v022 files add later
// handlers to the same Handlers type.
//
// Each handler:
//  1. Extracts TenantContext from the request context (set by the caller's
//     auth layer — here internal/a2aserver).
//  2. Calls policy.Engine.Check.
//  3. Dispatches to internal/mcp/chain, internal/mcp/indexer, or
//     internal/mcp/builder.
//  4. Maps backend errors to user-visible MCP errors (policy reject →
//     plain text; chain reject → Code + RawLog).
//
// Upstream also carried registry.go, which registered every handler with an
// MCP server via the mcp.AddTool generic and its reflection-derived JSON
// schemas, plus authgate.go for the middleware around it. Neither came across
// in the absorption: this binary serves A2A, and internal/toolbridge binds
// these same handlers to A2A operations instead. Handlers and New live in
// handlers.go.
package tools
