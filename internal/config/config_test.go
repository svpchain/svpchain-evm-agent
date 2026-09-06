package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const minimal = `
listen_addr = ":8083"
[dex_chain]
id = "svp-2517-1"
evm_rpc_url = "http://127.0.0.1:8545"
[defi_mcp]
url = "http://127.0.0.1:18081/mcp"
auth_token = "test-private-token"
[llm]
api_key_env = "EVM_AGENT_LLM_API_KEY"
`

func TestLoadMinimal(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "http://localhost:8083" {
		t.Fatalf("public URL = %q", cfg.PublicURL)
	}
}
func TestLoadRequiresPrivateServices(t *testing.T) {
	for _, key := range []string{"evm_rpc_url", "defi_mcp", "auth_token", "api_key_env"} {
		body := strings.Replace(minimal, key, "removed_"+key, 1)
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Errorf("%s must be required", key)
		}
	}
}
