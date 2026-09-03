package a2aserver

import (
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
)

type CardIdentity struct {
	Name               string
	Version            string
	Description        string
	SkillDescOverrides map[string]string
}

var skillMetas = []struct {
	id, name, desc string
	tags           []string
}{
	{toolbridge.SkillAuth, "SVP-Chain Self-Service Auth", "Wallet-signature authentication for EVM Agent operations.", []string{"auth", "bearer", "wallet-signature"}},
	{toolbridge.SkillMeta, "SVP-Chain Agent Self-Description", "Discovery of the current startup-synchronized tool catalog and argument schemas.", []string{"discovery", "schema", "read-only"}},
	{toolbridge.SkillEVM, "SVP-Chain EVM DeFi", "Private DeFi MCP tools synchronized at startup, plus raw transaction broadcast and status.", []string{"evm", "defi", "mcp"}},
}

func BuildAgentCardFor(id CardIdentity, publicURL string, reg *toolbridge.Registry) *a2a.AgentCard {
	bySkill := reg.BySkill()
	skills := make([]a2a.AgentSkill, 0, len(bySkill))
	for _, meta := range skillMetas {
		tools := bySkill[meta.id]
		if len(tools) == 0 {
			continue
		}
		desc := meta.desc
		if override := id.SkillDescOverrides[meta.id]; override != "" {
			desc = override
		}
		skills = append(skills, a2a.AgentSkill{ID: meta.id, Name: meta.name, Description: fmt.Sprintf("%s Tools: %s.", desc, strings.Join(tools, ", ")), Tags: meta.tags})
	}
	return &a2a.AgentCard{Name: id.Name, Description: id.Description, Version: id.Version,
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(publicURL+"/invoke", a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"application/json", "text/plain"}, DefaultOutputModes: []string{"application/json"},
		Capabilities: a2a.AgentCapabilities{Streaming: true}, Provider: &a2a.AgentProvider{Org: "svpchain", URL: "https://www.svpchain.org"}, Skills: skills}
}
