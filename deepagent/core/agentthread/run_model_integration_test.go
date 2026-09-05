package agentthread

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/checkpointer"

	"github.com/cloudwego/eino-ext/components/model/ark"
	"github.com/cloudwego/eino/schema"
)

func TestIntegrationRunLLMEndWithRealModel(t *testing.T) {
	if os.Getenv("DEEPAGENT_MODEL_INTEGRATION") != "1" {
		t.Skip("set DEEPAGENT_MODEL_INTEGRATION=1 to run real model integration")
	}
	loadTestEnvFile(t, filepath.Join("..", "..", "cmd", "deepagent", "key.txt"))

	modelID := strings.TrimSpace(os.Getenv("ARK_MODEL"))
	apiKey := strings.TrimSpace(os.Getenv("ARK_API_KEY"))
	accessKey := strings.TrimSpace(os.Getenv("ARK_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("ARK_SECRET_KEY"))
	if modelID == "" {
		t.Fatal("ARK_MODEL is required")
	}
	if apiKey == "" && (accessKey == "" || secretKey == "") {
		t.Fatal("ARK_API_KEY or ARK_ACCESS_KEY+ARK_SECRET_KEY is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cm, err := ark.NewChatModel(ctx, &ark.ChatModelConfig{
		Model:     modelID,
		APIKey:    apiKey,
		AccessKey: accessKey,
		SecretKey: secretKey,
	})
	if err != nil {
		t.Fatalf("new ark model: %v", err)
	}

	evBus := make(chan Event, 128)
	runner := newTestRun(t, &TurnConfig{
		Agent: deepagents.Config{
			Model:           cm,
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}, "thread-real-model", "turn-real-model", NewNoopTestContextManager(), evBus)

	if err := executeTestRun(ctx, runner, schema.UserMessage("只回复四个字：测试通过"), &ResumeTurnOptions{
		CheckpointID: "thread-real-model:turn-real-model",
	}); err != nil {
		t.Fatalf("run turn: %v", err)
	}

	events := drainEvents(evBus, 200*time.Millisecond)
	var llmEnd *LLMEnd
	tokenCount := 0
	for i := range events {
		switch events[i].Type {
		case EventLLMToken:
			tokenCount++
		case EventLLMEnd:
			payload, ok := events[i].Payload.(LLMEnd)
			if !ok {
				t.Fatalf("llm_end payload type = %T", events[i].Payload)
			}
			llmEnd = &payload
		}
	}
	if llmEnd == nil {
		t.Fatalf("missing llm_end event, events=%v", eventTypes(events))
	}
	if llmEnd.Message == nil || strings.TrimSpace(llmEnd.Message.Content) == "" {
		t.Fatalf("llm_end missing merged message: %+v", llmEnd.Message)
	}
	if tokenCount == 0 {
		t.Fatalf("missing streaming token events, events=%v", eventTypes(events))
	}
	t.Logf("llm_end content=%q token_usage=%+v token_events=%d", llmEnd.Message.Content, llmEnd.TokenUsage, tokenCount)
}

func TestIntegrationArkStreamChunkUsage(t *testing.T) {
	if os.Getenv("DEEPAGENT_MODEL_INTEGRATION") != "1" {
		t.Skip("set DEEPAGENT_MODEL_INTEGRATION=1 to run real model integration")
	}
	cm := newIntegrationArkModel(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stream, err := cm.Stream(ctx, []*schema.Message{
		schema.UserMessage("只回复四个字：测试通过"),
	})
	if err != nil {
		t.Fatalf("stream model: %v", err)
	}
	defer stream.Close()

	for idx := 0; ; idx++ {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return
			}
			t.Fatalf("recv stream chunk %d: %v", idx, err)
		}
		if msg == nil {
			t.Logf("chunk[%d] nil", idx)
			continue
		}
		var usage any
		if msg.ResponseMeta != nil {
			usage = msg.ResponseMeta.Usage
		}
		t.Logf("chunk[%d] content=%q role=%q usage=%+v", idx, msg.Content, msg.Role, usage)
	}
}

func newIntegrationArkModel(t *testing.T) *ark.ChatModel {
	t.Helper()
	loadTestEnvFile(t, filepath.Join("..", "..", "cmd", "deepagent", "key.txt"))

	modelID := strings.TrimSpace(os.Getenv("ARK_MODEL"))
	apiKey := strings.TrimSpace(os.Getenv("ARK_API_KEY"))
	accessKey := strings.TrimSpace(os.Getenv("ARK_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("ARK_SECRET_KEY"))
	if modelID == "" {
		t.Fatal("ARK_MODEL is required")
	}
	if apiKey == "" && (accessKey == "" || secretKey == "") {
		t.Fatal("ARK_API_KEY or ARK_ACCESS_KEY+ARK_SECRET_KEY is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cm, err := ark.NewChatModel(ctx, &ark.ChatModelConfig{
		Model:     modelID,
		APIKey:    apiKey,
		AccessKey: accessKey,
		SecretKey: secretKey,
	})
	if err != nil {
		t.Fatalf("new ark model: %v", err)
	}
	return cm
}

func loadTestEnvFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read env file: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		for _, field := range strings.Fields(line) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if key == "" || os.Getenv(key) != "" {
				continue
			}
			t.Setenv(key, value)
		}
	}
}

func eventTypes(events []Event) []EventType {
	types := make([]EventType, 0, len(events))
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	return types
}
