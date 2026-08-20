package main

import "github.com/svpchain/svpchain-evm-agent/internal/a2aserver"

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
		"settlement and self-registration. Whitelisted delegated EVM calls and " +
		"native-SVP transfers use " +
		"MsgAgentExecDelegated; all other EVM builds are caller-signed.",
}
