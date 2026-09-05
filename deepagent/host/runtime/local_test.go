package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/mock/mock_model"
	sdkruntime "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/runtime/clienttest"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/mock/gomock"

	"eino-cli/deepagent/backend/config"
	memorystore "eino-cli/deepagent/backend/memory/store"
	deepagents "eino-cli/deepagent/core"
)

func TestLocalAgentRegistersCoreAndExtensionToolsOnce(t *testing.T) {
	restore := config.SetRootDirForTest(t.TempDir())
	t.Cleanup(restore)
	cfg := &config.Config{DefaultModel: "fixture", Models: map[string]*config.ModelConfig{
		"fixture": {Name: "fixture", Provider: "openai", Model: "fixture", BaseURL: "http://localhost", APIKey: "test-only"},
	}}
	agentConfig, err := buildLocalAgentConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	agentConfig.SkillLoader = nil
	chatModel := mock_model.NewMockToolCallingChatModel(gomock.NewController(t))
	chatModel.EXPECT().WithTools(gomock.Any()).DoAndReturn(func(infos []*schema.ToolInfo) (bound model.ToolCallingChatModel, err error) {
		names := map[string]bool{}
		for _, info := range infos {
			if names[info.Name] {
				t.Fatalf("duplicate tool %s", info.Name)
			}
			names[info.Name] = true
		}
		for _, name := range []string{"ls", "read_file", "write_file", "edit_file", "glob", "grep", "execute", "shell", "await_shell", "rg", "delete_file", "semantic_search", "read_lints", "ask_clarification"} {
			if !names[name] {
				t.Errorf("missing tool %s", name)
			}
		}
		if names["apply_patch"] || names["upload_files"] || names["download_files"] {
			t.Error("CLI must keep its restricted file tool surface")
		}
		return chatModel, nil
	})
	agentConfig.Model = chatModel
	if _, err = deepagents.New(context.Background(), deepagents.WithConfig(agentConfig)); err != nil {
		t.Fatal(err)
	}
}

func TestBuildLocalAgentConfigLoadsSkills(t *testing.T) {
	root := t.TempDir()
	restore := config.SetRootDirForTest(root)
	t.Cleanup(restore)
	skillDir := filepath.Join(root, "deepagent", "backend", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill.\n---\n# Demo"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	cfg := &config.Config{
		DefaultModel: "fixture",
		Models: map[string]*config.ModelConfig{"fixture": {
			Name: "fixture", Provider: "openai", Model: "fixture", BaseURL: "http://localhost", APIKey: "test-only",
		}},
	}
	agentConfig, err := buildLocalAgentConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("buildLocalAgentConfig() error=%v", err)
	}
	loaded, err := agentConfig.SkillLoader.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("LoadSkills() error=%v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "demo" {
		t.Fatalf("loaded skills=%+v", loaded)
	}
	if agentConfig.Backend == nil {
		t.Fatal("sandbox backend was not configured")
	}
}

func TestMemoryTurnCompletedUpdatesMemoryAsynchronously(t *testing.T) {
	root := t.TempDir()
	restore := config.SetRootDirForTest(root)
	t.Cleanup(restore)
	ctrl := gomock.NewController(t)
	chatModel := mock_model.NewMockToolCallingChatModel(ctrl)
	chatModel.EXPECT().Generate(gomock.Any(), gomock.Any(), gomock.Any()).Return(schema.AssistantMessage(`{"newFacts":[{"content":"prefers concise answers","category":"preference","confidence":0.9}]}`, nil), nil)
	completed := memoryTurnCompleted()
	completed(context.Background(), "thread-1", "turn-1", chatModel, []*schema.Message{schema.UserMessage("be concise")})
	store := memorystore.NewStore()
	deadline := time.Now().Add(2 * time.Second)
	for {
		memory, err := store.Load("default")
		if err == nil && len(memory.Facts) == 1 && memory.Facts[0].Content == "prefers concise answers" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("memory update not observed: memory=%+v error=%v", memory, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var _ model.ToolCallingChatModel = (*mock_model.MockToolCallingChatModel)(nil)

func TestLocalRuntimeHeadlessAcceptanceAndRestartReplay(t *testing.T) {
	t.Parallel()

	client := clienttest.NewFake()
	router := &sdkruntime.Router{Local: client}
	runtime := &LocalRuntime{router: router, sessionID: "session-1", modelName: "fixture"}
	firstOutput, err := runUnifiedTurn(context.Background(), runtime, "first")
	if err != nil || firstOutput != "first" {
		t.Fatalf("runUnifiedTurn(first) output=%q error=%v", firstOutput, err)
	}

	exported, err := runtime.ExportThreadRef()
	if err != nil {
		t.Fatalf("ExportHistory() error = %v", err)
	}
	var ref sdkruntime.GlobalThreadRef
	if err = json.Unmarshal(exported, &ref); err != nil {
		t.Fatalf("decode exported ref: %v", err)
	}
	timeline, err := router.ListTimeline(context.Background(), sdkruntime.TimelineQuery{Ref: ref})
	if err != nil {
		t.Fatalf("ListTimeline() error = %v", err)
	}
	assertTimelineTypes(t, timeline, protoevent.EventTypeToolCallStarted.String(), protoevent.EventTypeToolCallFinished.String(), protoevent.EventTypeAssistantDelta.String(), protoevent.EventTypeTurnFinished.String())

	restarted := &LocalRuntime{router: router, sessionID: "session-1", modelName: "fixture"}
	if err = restarted.ImportThreadRef(exported); err != nil {
		t.Fatalf("ImportThreadRef() error = %v", err)
	}
	secondOutput, err := runUnifiedTurn(context.Background(), restarted, "second")
	if err != nil || secondOutput != "second" {
		t.Fatalf("runUnifiedTurn(second) output=%q error=%v", secondOutput, err)
	}
}

func runUnifiedTurn(ctx context.Context, runtime *LocalRuntime, prompt string) (output string, err error) {
	stream, err := runtime.StartTurn(ctx, prompt)
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()
	var builder strings.Builder
	for event := range stream.Events {
		if event.TurnID != stream.TurnID {
			continue
		}
		switch protoevent.EventType(event.EventType) {
		case protoevent.EventTypeAssistantDelta:
			var payload protoevent.AssistantDeltaEventPayload
			if err = json.Unmarshal(event.Payload, &payload); err != nil {
				return "", err
			}
			builder.WriteString(payload.Delta)
		case protoevent.EventTypeTurnFinished:
			return builder.String(), nil
		case protoevent.EventTypeError:
			var payload protoevent.ErrorEventPayload
			_ = json.Unmarshal(event.Payload, &payload)
			return "", fmt.Errorf("runtime error: %s", payload.Message)
		}
	}
	if err = stream.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("timeline closed before turn completed")
}

func assertTimelineTypes(t *testing.T, timeline *sdkruntime.TimelineResult, expected ...string) {
	t.Helper()
	found := make(map[string]bool, len(timeline.Events))
	for _, event := range timeline.Events {
		found[event.EventType] = true
	}
	for _, eventType := range expected {
		if !found[eventType] {
			t.Fatalf("timeline missing %s: %+v", eventType, timeline.Events)
		}
	}
}
