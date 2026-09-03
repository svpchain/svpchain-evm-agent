package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type echoIn struct {
	Value string `json:"value"`
}
type echoOut struct {
	Echoed string `json:"echoed"`
}

func TestNativeDecodesArgsAndReturnsOutput(t *testing.T) {
	bound := Native(func(_ context.Context, in echoIn) (echoOut, error) { return echoOut{Echoed: in.Value}, nil })

	out, err := bound.Call(context.Background(), json.RawMessage(`{"value":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.(echoOut).Echoed != "hi" {
		t.Errorf("expected echoed input, got %+v", out)
	}
}

func TestNativeEmptyArgsYieldZeroInput(t *testing.T) {
	bound := Native(func(_ context.Context, in echoIn) (echoOut, error) { return echoOut{Echoed: in.Value}, nil })
	out, err := bound.Call(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.(echoOut).Echoed != "" {
		t.Errorf("expected zero-value input, got %+v", out)
	}
}

func TestNativeReportsDecodeAndHandlerErrors(t *testing.T) {
	boom := errors.New("handler refused")
	bound := Native(func(_ context.Context, _ echoIn) (echoOut, error) { return echoOut{}, boom })

	if _, err := bound.Call(context.Background(), json.RawMessage(`{nonsense`)); err == nil {
		t.Error("malformed args must fail to decode")
	}
	if _, err := bound.Call(context.Background(), nil); !errors.Is(err, boom) {
		t.Errorf("handler error must propagate, got %v", err)
	}
}

func TestRegistryRejectsDuplicatesAndUnknownLookups(t *testing.T) {
	r := newRegistry()
	if err := r.Add("skill-a", "tool-1", Bound{Call: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}

	if _, ok := r.Lookup("tool-1"); !ok {
		t.Error("registered tool must resolve")
	}
	if _, ok := r.Lookup("tool-2"); ok {
		t.Error("unregistered tool must not resolve")
	}

	if err := r.Add("skill-b", "tool-1", Bound{Call: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}); err == nil {
		t.Error("duplicate registration must fail")
	}
}
