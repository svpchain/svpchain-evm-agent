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
//
// ★ These bytes are load-bearing. The served card is hashed and published on
// chain by agent_self_register, and a verifier fetches the card and recomputes
// that hash. Editing anything here changes the card, so the deployment must run
// agent_self_update afterwards or the agent reads as unverified. The golden
// test beside this file is what makes such a change deliberate.
var identity = a2aserver.CardIdentity{
	Name:    "svpchain-evm-agent",
	Version: "0.1.0",
	Description: "EVM DeFi agent for SVP-Chain: swap quoting and building, bridge " +
		"deposits, ERC-20/ERC-721 transfers and approvals, raw EVM broadcast, " +
		"self-service auth, faucet, agent registry, delegations, and SVP-DT " +
		"settlement and self-registration. Delegated EVM contract calls and native-SVP transfers use " +
		"MsgAgentExecDelegated; all other EVM builds are caller-signed.",
	SkillDescOverrides: map[string]string{
		toolbridge.SkillEVM: "EVM-side operations: discover current Uniswap V2 pairs from the configured " +
			"Factory with list_swap_pairs, then quote_swap before building a swap; the deployment " +
			"does not maintain a static Pair list. Also broadcasts raw txs and tracks status, " +
			"builds bridge deposits (outbound and inbound from registered foreign chains), and " +
			"builds ERC-20/ERC-721 transfers and approvals. Served only when the deployment " +
			"configures the EVM endpoints.",
	},
}
