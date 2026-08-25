package wire

import (
	"testing"

	"github.com/svpchain/svpchain-evm-agent/internal/mcp/tools"
	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
)

// ★ This binary's whole surface, pinned by skill.
//
// It used to compare the union of four profiles against the full registry, back
// when core was shared and no single agent served everything. With only the EVM
// profile left the useful assertion inverts: state exactly which families this
// binary serves, so adding or dropping one is a deliberate edit here rather than
// a silent change to what the agent card advertises.
//
// The perps families (market data, account, trading, funds, Cosmos broadcast)
// remain available in toolbridge but are deliberately NOT registered — they are
// another binary's surface.
func TestEVMProfileServesExactlyItsFamilies(t *testing.T) {
	h := &tools.Handlers{}

	r := toolbridge.NewEmpty()
	EVMProfile.Register(r, h)

	want := map[string]bool{
		toolbridge.SkillEVM:    true,
		toolbridge.SkillAuth:   true,
		toolbridge.SkillFaucet: true,
	}
	got := r.BySkill()
	for skill := range want {
		if len(got[skill]) == 0 {
			t.Errorf("the evm profile registers nothing under %s", skill)
		}
	}
	for skill := range got {
		if !want[skill] {
			t.Errorf("the evm profile registers %s, which is not part of this binary's surface", skill)
		}
	}
}

func TestEVMProfileServesTheCallerSignedSupportStack(t *testing.T) {
	h := &tools.Handlers{}

	r := toolbridge.NewEmpty()
	EVMProfile.Register(r, h)
	for _, tool := range []string{
		"auth_challenge", "auth_verify",
		"list_faucet_tokens", "faucet_claim",
	} {
		if _, ok := r.Lookup(tool); !ok {
			t.Errorf("profile %s missing delegation-stack tool %q", EVMProfile.Name, tool)
		}
	}
}

// The EVM family is served whole: the DeFi surface plus the landing rail its
// builds settle through. A binary serving only half would advertise builds it
// could not land.
func TestEVMProfileServesTheWholeEVMFamily(t *testing.T) {
	h := &tools.Handlers{}

	r := toolbridge.NewEmpty()
	EVMProfile.Register(r, h)

	full := toolbridge.NewEmpty()
	full.RegisterEVM(h)

	if got, want := len(r.BySkill()[toolbridge.SkillEVM]), len(full.BySkill()[toolbridge.SkillEVM]); got != want {
		t.Errorf("the evm profile serves %d EVM tools, the family has %d", got, want)
	}
}
