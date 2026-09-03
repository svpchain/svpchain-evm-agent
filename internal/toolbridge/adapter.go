// Package toolbridge exposes the EVM Agent's own tools and its startup-frozen
// private MCP catalog as A2A operations.
package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
)

// Op is one invokable operation: a tool bound to the A2A skill it is
// advertised under.
type Op struct {
	Skill string
	Tool  string
	Call  func(ctx context.Context, args json.RawMessage) (any, error)

	// InputSchema describes the args object, reflected from the local input
	// type, including jsonschema struct tags. Nil means an object with an
	// unspecified shape.
	InputSchema *jsonschema.Schema
	// RawInputSchema preserves a schema supplied by a private MCP server.
	RawInputSchema any
}

// AddProxy registers a private MCP tool under this agent's public skill. Its
// schema is retained verbatim for list_tools and the LLM; args are decoded as
// an object before the proxy is invoked.
func (r *Registry) AddProxy(skill, tool string, schema any, call func(context.Context, map[string]any) (string, error)) error {
	if _, dup := r.ops[tool]; dup {
		return fmt.Errorf("toolbridge: duplicate tool %q", tool)
	}
	r.ops[tool] = Op{Skill: skill, Tool: tool, RawInputSchema: schema, Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
		args := map[string]any{}
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("decode args: %w", err)
			}
			if args == nil {
				return nil, fmt.Errorf("args must be an object")
			}
		}
		return call(ctx, args)
	}}
	return nil
}

// Bound is a typed local handler plus the schema of its arguments.
type Bound struct {
	Call        func(ctx context.Context, args json.RawMessage) (any, error)
	InputSchema *jsonschema.Schema
}

// schemaFor reflects a local handler input. An unusable schema must not stop
// the agent from serving the tool.
func schemaFor[In any]() *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		return nil
	}
	return s
}

func native[In, Out any](f func(context.Context, In) (Out, error)) Bound {
	return Bound{InputSchema: schemaFor[In](), Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in In
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("decode args: %w", err)
			}
		}
		return f(ctx, in)
	}}
}

// Native exposes a typed local handler to the small relay surface.
func Native[In, Out any](f func(context.Context, In) (Out, error)) Bound { return native(f) }

// Add adds an explicitly constructed operation. Dynamic MCP proxies use
// AddProxy; the agent's own auth and relay tools use Add with Native.
func (r *Registry) Add(skill, tool string, b Bound) error {
	if _, dup := r.ops[tool]; dup {
		return fmt.Errorf("toolbridge: duplicate tool %q", tool)
	}
	r.ops[tool] = Op{Skill: skill, Tool: tool, Call: b.Call, InputSchema: b.InputSchema}
	return nil
}

// Registry maps tool names to operations and groups them by skill for the
// Agent Card.
type Registry struct {
	ops map[string]Op
}

func newRegistry() *Registry { return &Registry{ops: map[string]Op{}} }

// Lookup returns the operation registered under tool.
func (r *Registry) Lookup(tool string) (Op, bool) {
	op, ok := r.ops[tool]
	return op, ok
}

// List returns every registered operation, sorted by tool name. The listing
// surface (list_tools) and the completeness tests read this.
func (r *Registry) List() []Op {
	out := make([]Op, 0, len(r.ops))
	for _, op := range r.ops {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

// BySkill returns tool names grouped by skill, each group sorted — the card
// generator and the completeness tests read this.
func (r *Registry) BySkill() map[string][]string {
	out := map[string][]string{}
	for _, op := range r.ops {
		out[op.Skill] = append(out[op.Skill], op.Tool)
	}
	for _, tools := range out {
		sort.Strings(tools)
	}
	return out
}
