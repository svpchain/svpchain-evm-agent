package defimcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionFailureRecognizesStreamableHTTPFailures(t *testing.T) {
	for _, message := range []string{
		"connection closed: broken session: 409 Conflict",
		"stream ID conflicts with ongoing stream",
		"session is closed",
		"session not found",
		"connection closed: calling \"tools/call\": client is closing: hanging GET: failed to reconnect (session ID: abc): connection failed after 5 attempts: Get \"http://127.0.0.1:8766/\": consumer stopped",
	} {
		if !sessionFailure(fmt.Errorf("%s", message)) {
			t.Errorf("sessionFailure(%q) = false", message)
		}
	}
	if sessionFailure(fmt.Errorf("ERC-20 allowance is insufficient")) {
		t.Fatal("tool errors must not trigger a session reconnect")
	}
}

func TestRetrySafeToolDoesNotRetrySideEffects(t *testing.T) {
	for _, name := range []string{"list_evm_assets", "get_balance", "quote_swap", "build_erc20_transfer", "lendora_build_supply_tx"} {
		if !retrySafeTool(name) {
			t.Errorf("retrySafeTool(%q) = false", name)
		}
	}
	for _, name := range []string{"broadcast_evm_tx", "set_transfer_out_cap", "faucet_claim"} {
		if retrySafeTool(name) {
			t.Errorf("retrySafeTool(%q) = true", name)
		}
	}
}

func TestSameCatalogRejectsChangedSchemas(t *testing.T) {
	base := map[string]Tool{"quote_swap": {Name: "quote_swap", InputSchema: map[string]any{"type": "object"}}}
	require.True(t, sameCatalog(base, map[string]Tool{"quote_swap": {Name: "quote_swap", InputSchema: map[string]any{"type": "object"}}}))
	require.False(t, sameCatalog(base, map[string]Tool{"quote_swap": {Name: "quote_swap", InputSchema: map[string]any{"type": "string"}}}))
}

type captureTransport struct{ req *http.Request }

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.req = req
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
}

func TestTrustedTransportAddsPrivateHeaders(t *testing.T) {
	base := &captureTransport{}
	transport := trustedTransport{token: "private-token", base: base}
	req, err := http.NewRequestWithContext(WithCaller(context.Background(), "svp1caller"), http.MethodPost, "http://mcp.test/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if got := base.req.Header.Get(agentTokenHeader); got != "private-token" {
		t.Fatalf("agent token header = %q", got)
	}
	if got := base.req.Header.Get(callerHeader); got != "svp1caller" {
		t.Fatalf("caller header = %q", got)
	}
	if got := req.Header.Get(agentTokenHeader); got != "" {
		t.Fatalf("original request was mutated: %q", got)
	}
}

func TestStreamableHTTPClientAllowsPersistentHangingGET(t *testing.T) {
	client := newStreamableHTTPClient("private-token")
	if client.Timeout != 0 {
		t.Fatalf("streamable HTTP client timeout = %s, want 0 for persistent hanging GET", client.Timeout)
	}
	transport, ok := client.Transport.(trustedTransport)
	if !ok {
		t.Fatalf("transport = %T, want trustedTransport", client.Transport)
	}
	if transport.token != "private-token" {
		t.Fatalf("transport token = %q", transport.token)
	}
}
