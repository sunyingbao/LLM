package fornax

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"code.byted.org/flowdevops/fornax_sdk/consts"
	"code.byted.org/flowdevops/fornax_sdk/infra/ctxmeta"
	"eino-cli/deepagent/worker/thread/runtimectx"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
)

func TestNewDirectHTTPClientBypassesProxy(t *testing.T) {
	client := newDirectHTTPClient(1234)
	if client.Timeout != 1234*time.Millisecond {
		t.Fatalf("timeout=%s", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type=%T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatalf("proxy should be disabled")
	}
}

func TestWithCorrelationAddsCloudAgentIdentity(t *testing.T) {
	ctx := runtimectx.ContextWithThreadIdentity(context.Background(), runtimectx.ThreadIdentity{
		ThreadID:  "thread-1",
		SessionID: "session-1",
		UserID:    42,
		Namespace: "dreamina",
		Env:       "local",
	})
	ctx = runtimectx.ContextWithTurnIdentity(ctx, runtimectx.TurnIdentity{
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		MessageID: "message-1",
	})

	got := ctxmeta.GetAllExtras(withCorrelation(ctx))
	want := map[string]string{
		tagSessionID:           "session-1",
		consts.FornaxThreadID:  "thread-1",
		consts.FornaxUserID:    "42",
		tagNamespace:           "dreamina",
		tagCloudAgentEnv:       "local",
		tagTurnID:              "turn-1",
		consts.FornaxMessageID: "message-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extras=%v, want %v", got, want)
	}
}

func TestWithCorrelationUsesTurnThreadIDFallback(t *testing.T) {
	ctx := runtimectx.ContextWithTurnIdentity(context.Background(), runtimectx.TurnIdentity{
		ThreadID: "thread-from-turn",
		TurnID:   "turn-1",
	})
	got := ctxmeta.GetAllExtras(withCorrelation(ctx))
	want := map[string]string{
		consts.FornaxThreadID: "thread-from-turn",
		tagTurnID:             "turn-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extras=%v, want %v", got, want)
	}
}

func TestWithCorrelationLeavesUnidentifiedContextUnchanged(t *testing.T) {
	ctx := context.Background()
	if got := withCorrelation(ctx); got != ctx {
		t.Fatal("context without CloudAgent identity should be returned unchanged")
	}
}

func TestNormalizeRunInfoForFornaxMapsToolsNodeWithoutMutatingInput(t *testing.T) {
	input := &callbacks.RunInfo{
		Name:      "tools",
		Type:      "ToolsNode",
		Component: compose.ComponentOfToolsNode,
	}

	got := normalizeRunInfoForFornax(input)
	if got == input {
		t.Fatal("ToolsNode RunInfo should be copied before normalization")
	}
	if got.Component != compose.ComponentOfGraph {
		t.Fatalf("component=%q, want %q", got.Component, compose.ComponentOfGraph)
	}
	if got.Name != input.Name || got.Type != input.Type {
		t.Fatalf("normalization changed name/type: got=%+v input=%+v", got, input)
	}
	if input.Component != compose.ComponentOfToolsNode {
		t.Fatalf("input component was mutated: %q", input.Component)
	}
}

func TestNormalizeRunInfoForFornaxLeavesOtherComponentsUnchanged(t *testing.T) {
	input := &callbacks.RunInfo{Component: compose.ComponentOfGraph}
	if got := normalizeRunInfoForFornax(input); got != input {
		t.Fatal("non-ToolsNode RunInfo should be returned unchanged")
	}
	if got := normalizeRunInfoForFornax(nil); got != nil {
		t.Fatalf("nil RunInfo normalized to %+v", got)
	}
}
