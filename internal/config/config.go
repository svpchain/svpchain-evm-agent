package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config contains only the EVM relay and private-agent dependencies. Contract
// addresses and DeFi protocol configuration belong to the private DeFi MCP.
type Config struct {
	ListenAddr string         `toml:"listen_addr"`
	PublicURL  string         `toml:"public_url"`
	DEXChain   DEXChainConfig `toml:"dex_chain"`
	LLM        LLMConfig      `toml:"llm"`
	DeFiMCP    DeFiMCPConfig  `toml:"defi_mcp"`
}

type DEXChainConfig struct {
	ID        string `toml:"id"`
	EVMRPCURL string `toml:"evm_rpc_url"`
}
type LLMConfig struct {
	Provider  string `toml:"provider"`
	BaseURL   string `toml:"base_url"`
	Model     string `toml:"model"`
	APIKeyEnv string `toml:"api_key_env"`
}
type DeFiMCPConfig struct {
	URL     string   `toml:"url"`
	Timeout Duration `toml:"timeout"`
}
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	value, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(value)
	return nil
}

func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("decode TOML %s: %w", path, err)
	}
	if cfg.PublicURL == "" && cfg.ListenAddr != "" {
		cfg.PublicURL = "http://localhost" + cfg.ListenAddr
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.ListenAddr) == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if strings.TrimSpace(c.DEXChain.ID) == "" {
		return fmt.Errorf("dex_chain.id is required")
	}
	if err := c.RequireEVM(); err != nil {
		return err
	}
	if strings.TrimSpace(c.DeFiMCP.URL) == "" {
		return fmt.Errorf("defi_mcp.url is required")
	}
	if strings.TrimSpace(c.LLM.APIKeyEnv) == "" {
		return fmt.Errorf("llm.api_key_env is required")
	}
	return nil
}

func (c *Config) RequireEVM() error {
	if strings.TrimSpace(c.DEXChain.EVMRPCURL) == "" {
		return fmt.Errorf("dex_chain.evm_rpc_url is required")
	}
	return nil
}
