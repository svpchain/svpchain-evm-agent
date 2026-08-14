// Command svpchain-evm-agent is the EVM DeFi A2A agent for SVP-Chain: swap
// quoting and building, bridge deposits, ERC-20/721 transfers and approvals,
// raw EVM broadcast, self-service auth, faucet, the chain's agent/agentwallet
// modules, and the SVP-DT execution core (identity, self-registration,
// settlement) when an operator key is configured. Delegated EVM writes are
// future work; all builds are caller-signed.
//
// Everything it serves is implemented under internal/, which was the shared
// svpchain-agent-core library until that repo was retired. The perps and
// Lendora families live in their own binaries.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/svpchain/svpchain-evm-agent/internal/a2aserver"
	"github.com/svpchain/svpchain-evm-agent/internal/config"
	"github.com/svpchain/svpchain-evm-agent/internal/wire"
)

func main() {
	configPath := flag.String("config", "", "TOML config (see internal/config)")
	flag.Parse()

	// Stop cleanly on SIGINT/SIGTERM so a container orchestrator gets a prompt
	// exit rather than a killed process.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *configPath); err != nil {
		fmt.Fprintf(os.Stderr, "svpchain-evm-agent: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string) error {
	if configPath == "" {
		return fmt.Errorf("-config is required")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	// This binary exists to serve the EVM family — an unconfigured EVM
	// endpoint is a boot failure here, not a call-time refusal.
	if err := cfg.RequireEVM(); err != nil {
		return err
	}
	app, err := wire.BuildProfile(ctx, cfg, wire.EVMProfile)
	if err != nil {
		return err
	}
	defer app.Close()
	return a2aserver.StartFullFor(ctx, cfg, app, identity)
}
