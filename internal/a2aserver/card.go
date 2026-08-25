package a2aserver

import (
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
)

// Skill-ID aliases, so callers of this package need not import toolbridge for
// the two skills its own doc comments and tests name most.
const (
	SkillMarketData = toolbridge.SkillMarketData
)

// skillMeta is the static, human-facing half of a skill; the tool list comes
// from the registry so the card can never advertise an operation the executor
// would not dispatch (a test asserts the two agree).
type skillMeta struct {
	id       string
	name     string
	desc     string
	tags     []string
	examples []string
}

var skillMetas = []skillMeta{
	{
		id:   toolbridge.SkillMarketData,
		name: "SVP-Chain Market Data",
		desc: "Read-only market intelligence: perpetual markets, live orderbooks, candles, " +
			"trades, funding, oracle price, and a batch-auction clearing-price estimate " +
			"for a given order size. Needs no credential and no account.",
		tags: []string{"market-data", "orderbook", "funding", "perpetuals", "read-only"},
		examples: []string{
			`{"skill":"svpchain-market-data","query":"estimate","ticker":"BTC-USD","side":"buy","size":"2.5"}`,
			`{"skill":"svpchain-market-data","tool":"list_markets"}`,
			`{"skill":"svpchain-market-data","tool":"get_orderbook","args":{"ticker":"BTC-USD"}}`,
		},
	},
	{
		id:   toolbridge.SkillAccount,
		name: "SVP-Chain Account & Positions",
		desc: "Owner-scoped reads: subaccounts (indexer, or live from chain gRPC via " +
			"get_live_subaccount, which answers even when the indexer is down), wallet " +
			"balances, orders, fills, transfers, PnL, and funding payments. Requires a bearer from " +
			"the svpchain-auth skill.",
		tags: []string{"account", "positions", "pnl", "orders"},
		examples: []string{
			`{"skill":"svpchain-account","tool":"get_subaccount","args":{"address":"svp1…","subaccount_number":0},"bearer":"…"}`,
			`message.metadata: {"svp.delegation/v1":{"tokens":["<base64 token>", "…"]}} · ` +
				`text: {"skill":"svpchain-account","tool":"get_balance","args":{}}`,
		},
	},
	{
		id:   toolbridge.SkillTrading,
		name: "SVP-Chain Trading (build)",
		desc: "Build unsigned order transactions — limit, market, conditional, cancel, batch " +
			"cancel — as payloads the caller signs with its own key and lands via " +
			"broadcast_signed_tx. The agent never holds the caller's key.",
		tags: []string{"trading", "orders", "unsigned-tx"},
		examples: []string{
			`{"skill":"svpchain-trading","tool":"build_place_limit_order","args":{"owner":"svp1…","subaccount_number":0,"ticker":"BTC-USD","side":"buy","size":"0.1","price":"60000"},"bearer":"…"}`,
		},
	},
	{
		id:   toolbridge.SkillFunds,
		name: "SVP-Chain Funds (build)",
		desc: "Build unsigned funds movements — deposit/withdraw/transfer between subaccounts, " +
			"bank send — plus per-symbol daily transfer-out caps. Movements are size-capped " +
			"by the operator's limits config.",
		tags: []string{"funds", "deposit", "withdraw", "unsigned-tx"},
	},
	{
		id:   toolbridge.SkillBroadcast,
		name: "SVP-Chain Broadcast",
		desc: "Land a signed transaction (the signer must be the authenticated owner) and " +
			"query transaction status by hash.",
		tags: []string{"broadcast", "tx-status"},
	},
	{
		id:   toolbridge.SkillAuth,
		name: "SVP-Chain Self-Service Auth",
		desc: "Wallet-signature authentication: auth_challenge issues a challenge bound to " +
			"an owner address, auth_verify checks the wallet's signature and mints a " +
			"bearer token that authenticates subsequent calls (Authorization header, " +
			"envelope field, or bound to this A2A context).",
		tags: []string{"auth", "bearer", "wallet-signature"},
		examples: []string{
			`{"skill":"svpchain-auth","tool":"auth_challenge","args":{"owner":"svp1…"}}`,
			`{"skill":"svpchain-auth","tool":"auth_verify","args":{"nonce":"…","signature":"…"}}`,
		},
	},
	{
		id:   toolbridge.SkillFaucet,
		name: "SVP-Chain Faucet",
		desc: "Testnet faucet: list claimable tokens and claim them to an address.",
		tags: []string{"faucet", "testnet"},
	},
	{
		id:   toolbridge.SkillEVM,
		name: "SVP-Chain EVM",
		desc: "EVM-side operations: broadcast raw txs and track status, quote and build " +
			"Uniswap-style swaps, build bridge deposits (outbound and inbound from " +
			"registered foreign chains), and build ERC-20/ERC-721 transfers and approvals. " +
			"Served only when the deployment configures the EVM endpoints.",
		tags: []string{"evm", "swap", "bridge", "erc20", "erc721"},
	},
}

// CardIdentity is the per-binary half of the Agent Card: who this agent says
// it is. The skill list still derives from the registry, so a per-category
// binary advertising a subset of operations gets a truthful card for free.
type CardIdentity struct {
	Name        string
	Version     string
	Description string

	// SkillDescOverrides replaces a skill's static description on this
	// binary's card — used when a binary registers a deliberate subset of a
	// family (the lending agent serves only the EVM landing rail, so the EVM
	// skill must not advertise swaps and bridges).
	SkillDescOverrides map[string]string
}

// Each agent declares its own CardIdentity in its own repo. It is that agent's
// product identity, and its bytes are hashed into that agent's on-chain
// registration — so it must be editable without write access to this library,
// and a change to it must not be able to move any other agent's card.
//
// What this package still owns is skillMetas above: the static text for every
// skill. A change there moves EVERY agent's card at once, which is why the
// golden test beside this file pins it.

// BuildAgentCardFor returns the public Agent Card for one binary's identity
// over its registry. The registry supplies each skill's tool list, so a card
// can never advertise an operation the executor would not dispatch.
func BuildAgentCardFor(id CardIdentity, publicURL string, reg *toolbridge.Registry) *a2a.AgentCard {
	bySkill := map[string][]string{}
	if reg != nil {
		bySkill = reg.BySkill()
	}

	var skills []a2a.AgentSkill
	for _, m := range skillMetas {
		tools := bySkill[m.id]
		// A skill this agent registers nothing under is left off its card.
		if len(tools) == 0 {
			continue
		}
		desc := m.desc
		if o, ok := id.SkillDescOverrides[m.id]; ok {
			desc = o
		}
		if len(tools) > 0 {
			desc = fmt.Sprintf("%s Tools: %s.", desc, strings.Join(tools, ", "))
		}
		skills = append(skills, a2a.AgentSkill{
			ID:          m.id,
			Name:        m.name,
			Description: desc,
			Tags:        m.tags,
			Examples:    m.examples,
		})
	}

	return &a2a.AgentCard{
		Name:        id.Name,
		Description: id.Description,
		Version:     id.Version,
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(publicURL+"/invoke", a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json"},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		Provider: &a2a.AgentProvider{
			Org: "svpchain",
			URL: "https://www.svpchain.org",
		},
		Skills: skills,
	}
}
