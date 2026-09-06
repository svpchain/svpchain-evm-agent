// Package wire builds the intentionally small public EVM Agent surface.
package wire

import (
	"context"

	"github.com/svpchain/svpchain-evm-agent/internal/agenttools"
	"github.com/svpchain/svpchain-evm-agent/internal/config"
	"github.com/svpchain/svpchain-evm-agent/internal/mcp/auth"
	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
)

type App struct {
	Registry *toolbridge.Registry
	Tools    *agenttools.Service
	Tenants  *auth.DynamicTenantStore
}

func (a *App) Close() {
	if a.Tools != nil {
		a.Tools.Close()
	}
}
func (a *App) Run(ctx context.Context) error { <-ctx.Done(); return nil }

func Build(ctx context.Context, cfg *config.Config) (*App, error) {
	if err := cfg.RequireEVM(); err != nil {
		return nil, err
	}
	nonces := auth.NewNonceStore(auth.DefaultChallengeTTL, nil)
	tenants := auth.NewDynamicTenantStore(auth.DynamicTenantStoreConfig{BearerTTL: auth.DefaultBearerTTL}, nil)
	service, err := agenttools.New(cfg.DEXChain.ID, cfg.DEXChain.EVMRPCURL, nonces, tenants)
	if err != nil {
		return nil, err
	}
	registry := toolbridge.NewEmpty()
	for _, item := range []struct {
		skill, name string
		bound       toolbridge.Bound
	}{
		// Legacy direct-A2A auth handlers remain on Service for now, but are no
		// longer public operations. Private MCP access is authenticated solely by
		// the EVM relay's shared token.
		{toolbridge.SkillEVM, "broadcast_evm_tx", toolbridge.Native(service.Broadcast)},
		{toolbridge.SkillEVM, "evm_tx_status", toolbridge.Native(service.TxStatus)},
	} {
		if err := registry.Add(item.skill, item.name, item.bound); err != nil {
			service.Close()
			return nil, err
		}
	}
	registry.RegisterMeta()
	return &App{Registry: registry, Tools: service, Tenants: tenants}, nil
}
