package main

import (
	"context"
	"testing"

	"github.com/svpchain/svpchain-evm-agent/internal/a2aserver"
	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
)

func TestAgentCardListsOnlyRelaySkills(t *testing.T) {
	registry := toolbridge.NewEmpty()
	for _, entry := range []struct{ skill, tool string }{
		{toolbridge.SkillEVM, "broadcast_evm_tx"},
		{toolbridge.SkillEVM, "evm_tx_status"},
	} {
		if err := registry.Add(entry.skill, entry.tool, toolbridge.Native(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })); err != nil {
			t.Fatal(err)
		}
	}
	registry.RegisterMeta()

	card := a2aserver.BuildAgentCardFor(identity, "https://agents.example.test", registry)
	if len(card.Skills) != 2 {
		t.Fatalf("expected evm and meta skills, got %d", len(card.Skills))
	}
	for _, skill := range card.Skills {
		if skill.ID == toolbridge.SkillEVM && (contains(skill.Tags, "swap") || contains(skill.Tags, "bridge")) {
			t.Fatalf("relay card advertises legacy DeFi tags: %v", skill.Tags)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
