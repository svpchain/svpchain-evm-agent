// Package defimcp owns the private Streamable HTTP MCP connection. The
// startup snapshot is deliberately immutable: every public and LLM call is
// checked against it before it reaches the private service.
package defimcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool struct {
	Name        string
	Description string
	InputSchema any
}

type Client struct {
	session *mcp.ClientSession
	tools   map[string]Tool
}

func Connect(ctx context.Context, endpoint string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "svpchain-evm-agent", Version: "v0.2.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: strings.TrimSpace(endpoint), HTTPClient: &http.Client{Timeout: timeout},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect private defi mcp: %w", err)
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("list private defi mcp tools: %w", err)
	}
	out := &Client{session: session, tools: make(map[string]Tool, len(listed.Tools))}
	for _, tool := range listed.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" || tool.InputSchema == nil {
			_ = session.Close()
			return nil, fmt.Errorf("private defi mcp returned invalid tool")
		}
		if _, exists := out.tools[name]; exists {
			_ = session.Close()
			return nil, fmt.Errorf("private defi mcp returned duplicate tool %q", name)
		}
		out.tools[name] = Tool{Name: name, Description: tool.Description, InputSchema: tool.InputSchema}
	}
	if len(out.tools) == 0 {
		_ = session.Close()
		return nil, fmt.Errorf("private defi mcp returned no tools")
	}
	return out, nil
}

func (c *Client) Tools() []Tool {
	out := make([]Tool, 0, len(c.tools))
	for _, tool := range c.tools {
		out = append(out, tool)
	}
	return out
}

func (c *Client) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	if _, ok := c.tools[name]; !ok {
		return "", fmt.Errorf("tool %q is not in the startup MCP catalog", name)
	}
	result, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
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

func (c *Client) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	return c.session.Close()
}
