package a2aserver

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/svpchain/svpchain-evm-agent/internal/defimcp"
	"github.com/svpchain/svpchain-evm-agent/internal/toolbridge"
)

type IntentRunner interface {
	Run(context.Context, string) (string, error)
}

type Executor struct {
	registry *toolbridge.Registry
	authr    *AuthResolver
	intent   IntentRunner
}

var _ a2asrv.AgentExecutor = (*Executor)(nil)

func NewExecutor(registry *toolbridge.Registry, authr *AuthResolver, intent IntentRunner) *Executor {
	return &Executor{registry: registry, authr: authr, intent: intent}
}

func (e *Executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if execCtx.Message == nil {
			yield(nil, fmt.Errorf("empty message"))
			return
		}
		if execCtx.StoredTask == nil && !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}
		result, err := e.handle(ctx, execCtx)
		if err != nil {
			result = "error: " + err.Error()
		}
		reply := a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewTextPart(result))
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, reply), nil)
	}
}

func (e *Executor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

func (e *Executor) handle(ctx context.Context, execCtx *a2asrv.ExecutorContext) (string, error) {
	raw := messageText(execCtx.Message)
	var req Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		if e.intent == nil {
			return "", fmt.Errorf("request must be JSON naming a skill: %w", err)
		}
		return e.intent.Run(ctx, raw)
	}
	if intent := strings.TrimSpace(req.Intent); intent != "" {
		if e.intent == nil {
			return "", fmt.Errorf("natural-language tasks are not configured")
		}
		return e.intent.Run(ctx, intent)
	}
	if req.Skill == "" || req.Tool == "" {
		return "", fmt.Errorf("request must name a skill and tool")
	}
	op, ok := e.registry.Lookup(req.Tool)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", req.Tool)
	}
	if op.Skill != req.Skill {
		return "", fmt.Errorf("tool %q belongs to skill %q, not %q", req.Tool, op.Skill, req.Skill)
	}
	if e.authr != nil {
		ctx = e.authr.Attach(ctx, execCtx, &req)
	}
	result, err := op.Call(defimcp.WithCaller(ctx, req.Caller), req.Args)
	response := Response{Skill: req.Skill, Tool: req.Tool}
	if err != nil {
		response.Error = err.Error()
	} else {
		response.OK = true
		response.Result = structuredResult(result)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(encoded), nil
}

// structuredResult keeps private-MCP JSON payloads structured when they cross
// the A2A boundary. The MCP SDK exposes tool output as text, but callers need
// the actual payload object to feed the local build -> sign -> broadcast guard.
// Ordinary human-readable strings remain strings.
func structuredResult(result any) any {
	text, ok := result.(string)
	if !ok {
		return result
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return result
	}
	switch decoded.(type) {
	case map[string]any, []any:
		return decoded
	default:
		return result
	}
}

func messageText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	var out strings.Builder
	for _, part := range msg.Parts {
		if part != nil {
			out.WriteString(part.Text())
		}
	}
	return strings.TrimSpace(out.String())
}
