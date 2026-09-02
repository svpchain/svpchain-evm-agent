// Command svpchain-evm-agent is the EVM DeFi A2A agent for SVP-Chain: swap
// quoting and building, bridge deposits, ERC-20/721 transfers and approvals,
// raw EVM broadcast, self-service auth, and faucet. It is non-custodial:
// callers sign every built transaction with their own local signer.
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
	"time"

	"github.com/svpchain/svpchain-evm-agent/internal/a2aserver"
	"github.com/svpchain/svpchain-evm-agent/internal/agentrunner"
	"github.com/svpchain/svpchain-evm-agent/internal/config"
	"github.com/svpchain/svpchain-evm-agent/internal/defimcp"
	"github.com/svpchain/svpchain-evm-agent/internal/llm"
	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
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
	mcpClient, err := defimcp.Connect(ctx, cfg.DeFiMCP.URL, time.Duration(cfg.DeFiMCP.Timeout))
	if err != nil {
		return err
	}
	defer mcpClient.Close()
	for _, tool := range mcpClient.Tools() {
		if reservedMCPTool(tool.Name) {
			continue
		}
		name := tool.Name
		if err := app.Registry.AddProxy(toolbridge.SkillEVM, name, tool.InputSchema, func(callCtx context.Context, args map[string]any) (string, error) {
			return mcpClient.Call(callCtx, name, args)
		}); err != nil {
			return err
		}
	}
	key := os.Getenv(cfg.LLM.APIKeyEnv)
	runner := agentrunner.New(llm.Config{Provider: cfg.LLM.Provider, BaseURL: cfg.LLM.BaseURL, Model: cfg.LLM.Model, APIKey: key}, mcpClient)
	return a2aserver.StartFullFor(ctx, cfg, app, identity, runner)
}

func reservedMCPTool(name string) bool {
	switch name {
	case "auth_challenge", "auth_verify", "broadcast_evm_tx", "evm_tx_status", "list_tools":
		return true
	default:
		return false
	}
}
