// Package defimcp owns the private Streamable HTTP MCP connection. The
// startup snapshot is deliberately immutable: every public and LLM call is
// checked against it before it reaches the private service.
package defimcp

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	agentTokenHeader = "X-Svpchain-Evm-Agent-Token"
	callerHeader     = "X-Svpchain-Caller"
)

type callerKey struct{}

// WithCaller attaches an unverified caller address to a proxied request. The
// private MCP trusts the EVM relay, not this address; it is used only to build
// a transaction with the local signer's nonce and sender address.
func WithCaller(ctx context.Context, caller string) context.Context {
	caller = strings.TrimSpace(caller)
	if caller == "" {
		return ctx
	}
	return context.WithValue(ctx, callerKey{}, caller)
}

func callerFrom(ctx context.Context) string {
	caller, _ := ctx.Value(callerKey{}).(string)
	return caller
}

type trustedTransport struct {
	token string
	base  http.RoundTripper
}

func (t trustedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Set(agentTokenHeader, t.token)
	if caller := callerFrom(req.Context()); caller != "" {
		cloned.Header.Set(callerHeader, caller)
	}
	return t.base.RoundTrip(cloned)
}

type Tool struct {
	Name        string
	Description string
	InputSchema any
}

type Client struct {
	mu      sync.Mutex
	session *mcp.ClientSession
	tools   map[string]Tool

	serviceCtx context.Context
	endpoint   string
	authToken  string
	timeout    time.Duration
}

func Connect(ctx context.Context, endpoint, authToken string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	authToken = strings.TrimSpace(authToken)
	if authToken == "" {
		return nil, fmt.Errorf("private defi mcp auth token is required")
	}
	endpoint = strings.TrimSpace(endpoint)
	session, tools, err := connectSession(ctx, endpoint, authToken, timeout)
	if err != nil {
		return nil, err
	}
	return &Client{session: session, tools: tools, serviceCtx: ctx, endpoint: endpoint, authToken: authToken, timeout: timeout}, nil
}

func connectSession(ctx context.Context, endpoint, authToken string, timeout time.Duration) (*mcp.ClientSession, map[string]Tool, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "svpchain-evm-agent", Version: "v0.2.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: newStreamableHTTPClient(authToken),
	}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connect private defi mcp: %w", err)
	}
	listCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	listed, err := session.ListTools(listCtx, nil)
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("list private defi mcp tools: %w", err)
	}
	tools := make(map[string]Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" || tool.InputSchema == nil {
			_ = session.Close()
			return nil, nil, fmt.Errorf("private defi mcp returned invalid tool")
		}
		if _, exists := tools[name]; exists {
			_ = session.Close()
			return nil, nil, fmt.Errorf("private defi mcp returned duplicate tool %q", name)
		}
		tools[name] = Tool{Name: name, Description: tool.Description, InputSchema: tool.InputSchema}
	}
	if len(tools) == 0 {
		_ = session.Close()
		return nil, nil, fmt.Errorf("private defi mcp returned no tools")
	}
	return session, tools, nil
}

func newStreamableHTTPClient(authToken string) *http.Client {
	// A Streamable HTTP session owns a persistent hanging GET. http.Client.Timeout
	// applies to the entire response body, so using it here would periodically
	// cancel that GET and tear down an otherwise healthy session.
	return &http.Client{Transport: trustedTransport{token: authToken, base: http.DefaultTransport}}
}

func (c *Client) Tools() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Tool, 0, len(c.tools))
	for _, tool := range c.tools {
		out = append(out, tool)
	}
	return out
}

func (c *Client) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.tools[name]; !ok {
		return "", fmt.Errorf("tool %q is not in the startup MCP catalog", name)
	}
	result, err := c.callTool(ctx, name, args)
	if err != nil && sessionFailure(err) {
		if reconnectErr := c.reconnect(ctx); reconnectErr != nil {
			return "", fmt.Errorf("private defi mcp session failed: %w (reconnect failed: %v)", err, reconnectErr)
		}
		if retrySafeTool(name) {
			result, err = c.callTool(ctx, name, args)
		}
	}
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, content := range result.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text.WriteString(item.Text)
		}
	}
	if result.IsError {
		return text.String(), fmt.Errorf("%s", strings.TrimSpace(text.String()))
	}
	return text.String(), nil
}

func (c *Client) callTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if c.session == nil {
		return nil, fmt.Errorf("private defi mcp session is not connected")
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.session.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: args})
}

// reconnect replaces a broken Streamable HTTP session without changing the
// startup-frozen catalog. A changed catalog requires an EVM Agent restart so
// the public Agent Card and list_tools output stay consistent with dispatch.
func (c *Client) reconnect(ctx context.Context) error {
	old := c.session
	c.session = nil
	if old != nil {
		_ = old.Close()
	}
	session, tools, err := connectSession(c.serviceCtx, c.endpoint, c.authToken, c.timeout)
	if err != nil {
		return err
	}
	if !sameCatalog(c.tools, tools) {
		_ = session.Close()
		return fmt.Errorf("private defi mcp catalog changed; restart the EVM Agent to synchronize it")
	}
	c.session = session
	return nil
}

func sameCatalog(want, got map[string]Tool) bool {
	if len(want) != len(got) {
		return false
	}
	for name, expected := range want {
		actual, ok := got[name]
		if !ok || !reflect.DeepEqual(expected.InputSchema, actual.InputSchema) {
			return false
		}
	}
	return true
}

func sessionFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "409 conflict") ||
		strings.Contains(message, "stream id conflicts") ||
		strings.Contains(message, "connection closed") ||
		strings.Contains(message, "broken session") ||
		strings.Contains(message, "client is closing") ||
		strings.Contains(message, "hanging get") ||
		strings.Contains(message, "failed to reconnect") ||
		strings.Contains(message, "consumer stopped") ||
		strings.Contains(message, "session is closed") ||
		strings.Contains(message, "session not found")
}

// Retrying a completed write after a transport failure could submit it twice.
// Builders only calculate an unsigned payload, so they are safe to retry.
func retrySafeTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "get_") ||
		strings.HasPrefix(name, "list_") ||
		strings.HasPrefix(name, "quote_") ||
		strings.HasPrefix(name, "build_") ||
		(strings.Contains(name, "_build_") && strings.HasSuffix(name, "_tx")) ||
		name == "whoami"
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil
	}
	err := c.session.Close()
	c.session = nil
	return err
}
