package subagent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestBuildSubAgentMessages_ForkContextWrapsRawParentMessages(t *testing.T) {
	forkedMessages := []*schema.Message{
		schema.SystemMessage("forked-system"),
		schema.AssistantMessage("forked-assistant", nil),
	}
	mw := New(&SubAgentConfig{
		ContextInjector: &testContextInjector{
			messages: forkedMessages,
		},
	})

	got, err := mw.buildSubAgentMessages(context.Background(), "test_sub", "", "run", true)
	if err != nil {
		t.Fatalf("buildSubAgentMessages() error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(got))
	}
	if got[0].Role != schema.User || got[0].Content != parentAgentContextBegin {
		t.Fatalf("unexpected begin marker: %+v", got[0])
	}
	if got[1] != forkedMessages[0] {
		t.Fatalf("expected original parent system message pointer to be preserved")
	}
	if got[2] != forkedMessages[1] {
		t.Fatalf("expected original parent assistant message pointer to be preserved")
	}
	if got[3].Role != schema.User || got[3].Content != parentAgentContextEnd {
		t.Fatalf("unexpected end marker: %+v", got[3])
	}
	if got[4].Role != schema.User || got[4].Content != "run" {
		t.Fatalf("unexpected task message: %+v", got[4])
	}
}

func TestBuildSubAgentMessages_EmptyForkContextDoesNotAddMarkers(t *testing.T) {
	mw := New(&SubAgentConfig{
		ContextInjector: &testContextInjector{
			messages: []*schema.Message{},
		},
	})

	got, err := mw.buildSubAgentMessages(context.Background(), "test_sub", "extra", "run", true)
	if err != nil {
		t.Fatalf("buildSubAgentMessages() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Content != "上下文信息:\nextra" {
		t.Fatalf("unexpected extra context message: %+v", got[0])
	}
	if got[1].Content != "run" {
		t.Fatalf("unexpected task message: %+v", got[1])
	}
}

func TestBuildSubAgentMessages_ForkContextEndMarkerBeforeTask(t *testing.T) {
	mw := New(&SubAgentConfig{
		ContextInjector: &testContextInjector{
			messages: []*schema.Message{schema.UserMessage("forked-user")},
		},
	})

	got, err := mw.buildSubAgentMessages(context.Background(), "test_sub", "", "run", true)
	if err != nil {
		t.Fatalf("buildSubAgentMessages() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	if got[2].Role != schema.User || got[2].Content != parentAgentContextEnd {
		t.Fatalf("unexpected end marker: %+v", got[2])
	}
	if got[3].Role != schema.User || got[3].Content != "run" {
		t.Fatalf("unexpected task message: %+v", got[3])
	}
}
