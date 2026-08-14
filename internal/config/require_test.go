package config

import (
	"strings"
	"testing"
)

// This binary hard-requires the family it exists to serve: main.go calls
// RequireEVM before wiring, so a missing endpoint fails at boot with a named
// key rather than turning every EVM tool into a call-time refusal.
//
// The Lendora half of this file went with the Lendora surface.
func TestRequireEVMNeedsTheEVMEndpoint(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.RequireEVM(); err == nil || !strings.Contains(err.Error(), "evm_rpc_url") {
		t.Errorf("expected an error naming evm_rpc_url, got %v", err)
	}

	cfg, err = Load(writeConfig(t, minimal+`dex_chain.evm_rpc_url = "http://127.0.0.1:8545"`+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.RequireEVM(); err != nil {
		t.Errorf("expected the EVM requirement satisfied, got %v", err)
	}
}
