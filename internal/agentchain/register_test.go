package agentchain_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"

	"github.com/svpchain/svpchain-evm-agent/internal/agentchain"
	"github.com/svpchain/svpchain-evm-agent/internal/mcp/signer"
	"github.com/svpchain/svpchain-evm-agent/internal/owner"
)

func want() agentchain.Desired {
	return agentchain.Desired{
		Endpoint:       "https://evm-agent.svpchain.org",
		CapabilityHash: make([]byte, 32),
		Capabilities:   []string{"evm.swap", "evm.bridge"},
	}
}

// The registration DID derives from the account that signs the registration.
func TestBuildRegisterPassesChainValidation(t *testing.T) {
	hexKey, addrStr, err := owner.Generate()
	require.NoError(t, err)
	priv, err := signer.ParsePrivKey(hexKey)
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(addrStr)
	require.NoError(t, err)

	bond := sdk.Coin{Denom: "asvp", Amount: sdkmath.NewInt(5_000_000)}
	w := want()
	w.Pricing = &agenttypes.Pricing{
		Amount: "1000000",
		Unit:   "call",
	}
	msg := agentchain.BuildRegister(priv, addr, w, bond)

	require.NoError(t, msg.ValidateBasic())
	require.Equal(t, addrStr, msg.Owner)
	require.Equal(t, agenttypes.DIDPrefix+addrStr, msg.AgentId)
	require.Equal(t, bond, msg.InitialBond)
}

func TestBuildRegisterIncludesConfiguredPricing(t *testing.T) {
	hexKey, addrStr, err := owner.Generate()
	require.NoError(t, err)
	priv, err := signer.ParsePrivKey(hexKey)
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(addrStr)
	require.NoError(t, err)

	w := want()
	w.Pricing = &agenttypes.Pricing{Amount: "1000000", Unit: "call"}
	msg := agentchain.BuildRegister(priv, addr, w, sdk.NewCoin("asvp", sdkmath.NewInt(1)))
	require.Equal(t, w.Pricing, msg.Pricing)
}

func TestBuildUpdateCarriesOwner(t *testing.T) {
	existing := &agenttypes.Agent{
		AgentId:  "did:svp:svp1abc",
		Owner:    "svp1owner",
		Metadata: "kept",
	}
	msg := agentchain.BuildUpdate(existing, want())
	require.Equal(t, existing.AgentId, msg.AgentId)
	require.Equal(t, existing.Owner, msg.Owner)
}

// MsgUpdateAgent overwrites every mutable field, so anything the deploy does
// not know about has to be carried forward or it is silently wiped.
func TestBuildUpdateCarriesForwardUnmanagedFields(t *testing.T) {
	pricing := &agenttypes.Pricing{Unit: "call"}
	existing := &agenttypes.Agent{
		AgentId:  "did:svp:svp1abc",
		Owner:    "svp1owner",
		Pricing:  pricing,
		Metadata: "set-elsewhere",
	}

	// No metadata supplied: the existing value survives.
	msg := agentchain.BuildUpdate(existing, want())
	require.Equal(t, pricing, msg.Pricing, "pricing must not be cleared by an update that never set it")
	require.Equal(t, "set-elsewhere", msg.Metadata)

	// Metadata supplied: it wins.
	w := want()
	w.Metadata = "explicit"
	require.Equal(t, "explicit", agentchain.BuildUpdate(existing, w).Metadata)

	w.Pricing = &agenttypes.Pricing{Amount: "1000000", Unit: "call"}
	require.Equal(t, w.Pricing, agentchain.BuildUpdate(existing, w).Pricing)
}

func TestDrift(t *testing.T) {
	base := want()
	current := &agenttypes.Agent{
		Endpoint:       base.Endpoint,
		CapabilityHash: base.CapabilityHash,
		Capabilities:   []string{"evm.swap", "evm.bridge"},
	}

	t.Run("current record reports nothing", func(t *testing.T) {
		require.Empty(t, agentchain.Drift(current, base))
	})

	t.Run("capability tag order is not drift", func(t *testing.T) {
		reordered := base
		reordered.Capabilities = []string{"evm.bridge", "evm.swap"}
		require.Empty(t, agentchain.Drift(current, reordered))
	})

	t.Run("moved endpoint", func(t *testing.T) {
		moved := base
		moved.Endpoint = "https://elsewhere.example"
		require.Len(t, agentchain.Drift(current, moved), 1)
	})

	t.Run("changed card hash", func(t *testing.T) {
		rehashed := base
		h := make([]byte, 32)
		h[0] = 0xff
		rehashed.CapabilityHash = h
		require.Len(t, agentchain.Drift(current, rehashed), 1)
	})

	t.Run("changed capabilities", func(t *testing.T) {
		retagged := base
		retagged.Capabilities = []string{"evm.swap"}
		require.Len(t, agentchain.Drift(current, retagged), 1)
	})

	t.Run("empty metadata leaves an existing value alone", func(t *testing.T) {
		withMeta := *current
		withMeta.Metadata = "something"
		require.Empty(t, agentchain.Drift(&withMeta, base))
	})

	t.Run("configured price differs", func(t *testing.T) {
		quoted := base
		quoted.Pricing = &agenttypes.Pricing{Amount: "1000000", Unit: "call"}
		require.Contains(t, agentchain.Drift(current, quoted), "pricing changed")
	})
}

func TestAgentIDDerivesFromOwner(t *testing.T) {
	_, addrStr, err := owner.Generate()
	require.NoError(t, err)
	addr, err := sdk.AccAddressFromBech32(addrStr)
	require.NoError(t, err)
	require.Equal(t, agenttypes.DIDPrefix+addrStr, agentchain.AgentID(addr))
	require.NoError(t, agenttypes.ValidateAgentId(agentchain.AgentID(addr)))
}
