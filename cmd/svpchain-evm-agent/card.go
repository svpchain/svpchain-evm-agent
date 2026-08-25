package main

import (
	"github.com/svpchain/svpchain-evm-agent/internal/a2aserver"
	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
)

// identity is this agent's public face: the name, version, and description its
// Agent Card advertises.
//
// It lives here rather than in the core library because it is this agent's
// product identity — changing it is this repo's decision, not a change to the
// shared library every agent depends on.
var identity = a2aserver.CardIdentity{
	Name:    "svpchain-evm-agent",
	Version: "0.1.0",
	Description: "EVM DeFi agent for SVP-Chain: swap quoting and building, bridge " +
		"deposits, ERC-20/ERC-721 transfers and approvals, raw EVM broadcast, " +
		"self-service auth, and faucet. Every state-changing EVM payload is " +
		"signed by the caller locally before broadcast.",
	SkillDescOverrides: map[string]string{
		toolbridge.SkillEVM: "EVM-side operations: list configured stable ERC-20 aliases with list_evm_assets; discover " +
			"current Uniswap V2 pairs from the configured Factory with list_swap_pairs, then quote_swap " +
			"before building a swap; the deployment does not maintain a static Pair list. Also broadcasts raw txs and tracks status, " +
			"builds bridge deposits (outbound and inbound from registered foreign chains), and " +
			"builds ERC-20/ERC-721 transfers and approvals. Served only when the deployment " +
			"configures the EVM endpoints.",
	},
}
