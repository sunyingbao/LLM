package tasktool

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestCollabMiddlewareToolsReturnsTaskToolTools(t *testing.T) {
	mw := NewCollabMiddleware(CollabMiddlewareConfig{
		TaskTool: &TaskTool{Host: fakeNoopHost{}},
	})
	tools, err := mw.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 4 {
		t.Fatalf("len(tools) = %d, want 4", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("tool Info() error = %v", err)
		}
		names[info.Name] = true
	}
	for _, name := range []string{ToolSendMessage, ToolSpawnTask, ToolWaitMessage, ToolCloseTask} {
		if !names[name] {
			t.Fatalf("tool %q missing from %+v", name, names)
		}
	}
}

func TestCollabMiddlewarePromptWithoutRolesOmitsRoleGuide(t *testing.T) {
	mw := NewCollabMiddleware(CollabMiddlewareConfig{TaskTool: &TaskTool{Host: fakeNoopHost{}}})
	msgs, err := mw.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != schema.System {
		t.Fatalf("messages = %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "Omit the role field") {
		t.Fatalf("prompt = %s", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "Host-defined role guide for spawn_task:") {
		t.Fatalf("prompt should not contain empty role guide: %s", msgs[0].Content)
	}
}

func TestCollabMiddlewarePromptDefinesGenericCollaborationDiscipline(t *testing.T) {
	mw := NewCollabMiddleware(CollabMiddlewareConfig{TaskTool: &TaskTool{Host: fakeNoopHost{}}})
	msgs, err := mw.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	prompt := msgs[0].Content
	for _, want := range []string{
		"not a synchronous function call",
		"immediate blockers",
		"self-contained, and independently actionable",
		"Do not issue another spawn_task for the same unresolved task",
		"Use wait_message only when you need the task result",
		"Prefer waiting for multiple independent targets in one wait_message call",
		"Do not repeatedly wait with short timeouts",
		"Use close_task once a task thread is completed",
		"completed means the task produced a terminal result",
		"closed means the task thread has been shut down",
		"approval_required, followup_required, and interrupted are blocked states",
		"Do not expose raw thread IDs, message IDs",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	for _, unwanted := range []string{
		"files/modules",
		"patch",
		"tests",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("prompt contains coding-specific guidance %q: %s", unwanted, prompt)
		}
	}
}

func TestCollabMiddlewarePromptWithRolesAndExtraInstructions(t *testing.T) {
	mw := NewCollabMiddleware(CollabMiddlewareConfig{
		TaskTool:          &TaskTool{Host: fakeNoopHost{}},
		RolesDescription:  "- reviewer: inspect implementation",
		ExtraInstructions: "Always report blocked child threads.",
	})
	msgs, err := mw.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	prompt := msgs[0].Content
	for _, want := range []string{
		"Host-defined role guide for spawn_task:",
		"- reviewer: inspect implementation",
		"Do not invent role names or role semantics.",
		"Always report blocked child threads.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestCollabMiddlewarePromptWithCustomBasePrompt(t *testing.T) {
	mw := NewCollabMiddleware(CollabMiddlewareConfig{
		TaskTool:          &TaskTool{Host: fakeNoopHost{}},
		BasePrompt:        "Use business-specific collaboration policy.",
		RolesDescription:  "- reviewer: inspect implementation",
		ExtraInstructions: "Always report blocked child threads.",
	})
	msgs, err := mw.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	prompt := msgs[0].Content
	for _, want := range []string{
		"Use business-specific collaboration policy.",
		"Host-defined role guide for spawn_task:",
		"- reviewer: inspect implementation",
		"Always report blocked child threads.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "not a synchronous function call") {
		t.Fatalf("custom base prompt should replace default base prompt: %s", prompt)
	}
}
