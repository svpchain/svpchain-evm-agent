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
	Description: "EVM DeFi agent for SVP-Chain. It synchronizes its private DeFi MCP " +
		"tool catalog at startup and can plan read-only DeFi tasks with an LLM. " +
		"Every state-changing EVM payload is signed by the caller locally before broadcast.",
	SkillDescOverrides: map[string]string{
		toolbridge.SkillEVM: "EVM transaction broadcast and status. Additional private MCP tools " +
			"are synchronized at startup and published here only when the private service is reachable.",
	},
}
