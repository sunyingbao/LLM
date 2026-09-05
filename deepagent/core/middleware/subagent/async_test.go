package subagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/mock/mock_model"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/mock/gomock"
)

type testSubAgentRunner struct {
	runFn    func(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
	streamFn func(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error)
}

func (r *testSubAgentRunner) Run(ctx context.Context, messages []*schema.Message, opts ...SubAgentRunOption) (*schema.Message, error) {
	return r.runFn(ctx, messages)
}

func (r *testSubAgentRunner) Stream(ctx context.Context, messages []*schema.Message, opts ...SubAgentRunOption) (*schema.StreamReader[*schema.Message], error) {
	if r.streamFn != nil {
		return r.streamFn(ctx, messages)
	}
	msg, err := r.Run(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (r *testSubAgentRunner) Close(ctx context.Context) error { return nil }

func (r *testSubAgentRunner) Depth() int { return 1 }

type testContextInjector struct {
	messages []*schema.Message
	err      error
	loadFn   func()
}

func (i *testContextInjector) LoadContext(ctx context.Context, agentName string) ([]*schema.Message, error) {
	if i.loadFn != nil {
		i.loadFn()
	}
	return i.messages, i.err
}

type testSkillMiddleware struct {
	middleware.BaseMiddleware
	id int
}

func (m *testSkillMiddleware) Name() string {
	return fmt.Sprintf("test_skill_%d", m.id)
}

func invokeTaskTool(t *testing.T, base tool.BaseTool, payload string) string {
	t.Helper()
	invokable, ok := base.(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool %T is not invokable", base)
	}
	got, err := invokable.InvokableRun(context.Background(), payload)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	return got
}

func streamTaskTool(t *testing.T, base tool.BaseTool, payload string) []string {
	t.Helper()
	streamable, ok := base.(tool.StreamableTool)
	if !ok {
		t.Fatalf("tool %T is not streamable", base)
	}
	stream, err := streamable.StreamableRun(context.Background(), payload)
	if err != nil {
		t.Fatalf("StreamableRun() error = %v", err)
	}
	var chunks []string
	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("stream recv error: %v", recvErr)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func TestNew_DoesNotRegisterPresetSubAgents(t *testing.T) {
	mw := New(&SubAgentConfig{
		SubAgents: []*SubAgent{{Name: "test_sub"}},
	})

	if !mw.HasAgent("test_sub") {
		t.Fatalf("expected configured subagent to be registered")
	}
	if mw.HasAgent(ExplorerSubAgent.Name) {
		t.Fatalf("expected explorer preset not to be auto-registered")
	}
	if mw.HasAgent(ExecutorSubAgent.Name) {
		t.Fatalf("expected executor preset not to be auto-registered")
	}
}

func TestSubAgentTask_ForkContextInjectsMessages(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	var gotMessages []*schema.Message
	mw := New(&SubAgentConfig{
		SubAgents:    []*SubAgent{{Name: "test_sub"}},
		DefaultModel: cm,
		ContextInjector: &testContextInjector{
			messages: []*schema.Message{
				schema.SystemMessage("forked-system"),
				schema.UserMessage("forked-user"),
			},
		},
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					gotMessages = append([]*schema.Message(nil), messages...)
					return schema.AssistantMessage("done", nil), nil
				},
			}, nil
		},
	})

	out := invokeTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"run","context":"extra","fork_context":true,"wait_for_done":true}`)
	if out != "done" {
		t.Fatalf("unexpected task output: %q", out)
	}
	if len(gotMessages) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(gotMessages))
	}
	if gotMessages[0].Content != "上下文信息:\nextra" {
		t.Fatalf("unexpected extra context message: %q", gotMessages[0].Content)
	}
	if gotMessages[1].Role != schema.User {
		t.Fatalf("expected begin marker as user message, got role %s", gotMessages[1].Role)
	}
	if gotMessages[1].Content != parentAgentContextBegin {
		t.Fatalf("unexpected begin marker: %q", gotMessages[1].Content)
	}
	if gotMessages[2].Role != schema.System || gotMessages[2].Content != "forked-system" {
		t.Fatalf("unexpected forked system message: %+v", gotMessages[2])
	}
	if gotMessages[3].Role != schema.User || gotMessages[3].Content != "forked-user" {
		t.Fatalf("unexpected forked user message: %+v", gotMessages[3])
	}
	if gotMessages[4].Role != schema.User || gotMessages[4].Content != parentAgentContextEnd {
		t.Fatalf("unexpected end marker: %+v", gotMessages[4])
	}
	if gotMessages[5].Content != "run" {
		t.Fatalf("unexpected task message: %q", gotMessages[5].Content)
	}
}

func TestSubAgentTask_DefaultIsInvokableOnly(t *testing.T) {
	mw := New(&SubAgentConfig{
		SubAgents: []*SubAgent{{Name: "test_sub"}},
	})

	taskTool := mw.newTaskTool()
	if _, ok := taskTool.(tool.InvokableTool); !ok {
		t.Fatalf("expected default task tool to be invokable")
	}
	if _, ok := taskTool.(tool.StreamableTool); ok {
		t.Fatalf("expected default task tool not to be streamable")
	}
}

func TestSubAgentTask_StreamingOutputsChildChunks(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	runCalled := false
	streamCalled := false
	mw := New(&SubAgentConfig{
		SubAgents:           []*SubAgent{{Name: "test_sub"}},
		DefaultModel:        cm,
		EnableTaskStreaming: true,
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					runCalled = true
					return schema.AssistantMessage("sync", nil), nil
				},
				streamFn: func(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
					streamCalled = true
					return schema.StreamReaderFromArray([]*schema.Message{
						schema.AssistantMessage("hello ", nil),
						schema.AssistantMessage("world", nil),
					}), nil
				},
			}, nil
		},
	})

	taskTool := mw.newTaskTool()
	if _, ok := taskTool.(tool.StreamableTool); !ok {
		t.Fatalf("expected task tool to be streamable")
	}

	chunks := streamTaskTool(t, taskTool, `{"subagent":"test_sub","task":"run","wait_for_done":true}`)
	if len(chunks) != 2 || chunks[0] != "hello " || chunks[1] != "world" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}
	if !streamCalled {
		t.Fatalf("expected SubAgentRunner.Stream to be called")
	}
	if runCalled {
		t.Fatalf("expected SubAgentRunner.Run not to be called")
	}
}

func TestSubAgentTask_StreamingAsyncReturnsStartMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	started := make(chan struct{}, 1)
	mw := New(&SubAgentConfig{
		SubAgents:           []*SubAgent{{Name: "test_sub"}},
		DefaultModel:        cm,
		EnableTaskStreaming: true,
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					started <- struct{}{}
					return schema.AssistantMessage("async done", nil), nil
				},
			}, nil
		},
	})

	chunks := streamTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"run asynchronously","wait_for_done":false}`)
	if len(chunks) != 1 || chunks[0] != "subagent test_sub started asynchronously" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("expected async subagent to start")
	}
}

func TestSubAgentTask_ForkContextFalseSkipsInjector(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	var gotMessages []*schema.Message
	mw := New(&SubAgentConfig{
		SubAgents:    []*SubAgent{{Name: "test_sub"}},
		DefaultModel: cm,
		ContextInjector: &testContextInjector{
			messages: []*schema.Message{schema.UserMessage("forked-user")},
		},
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					gotMessages = append([]*schema.Message(nil), messages...)
					return schema.AssistantMessage("done", nil), nil
				},
			}, nil
		},
	})

	out := invokeTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"run","wait_for_done":true}`)
	if out != "done" {
		t.Fatalf("unexpected task output: %q", out)
	}
	if len(gotMessages) != 1 {
		t.Fatalf("expected only task message, got %d", len(gotMessages))
	}
	if gotMessages[0].Content != "run" {
		t.Fatalf("unexpected task message: %q", gotMessages[0].Content)
	}
}

func TestSubAgentTask_WaitForDoneDefaultsToTrue(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	runStarted := make(chan struct{}, 1)
	mw := New(&SubAgentConfig{
		SubAgents:    []*SubAgent{{Name: "test_sub"}},
		DefaultModel: cm,
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					runStarted <- struct{}{}
					return schema.AssistantMessage("sync-result", nil), nil
				},
			}, nil
		},
	})

	out := invokeTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"run"}`)
	if out != "sync-result" {
		t.Fatalf("unexpected task output: %q", out)
	}
	select {
	case <-runStarted:
	default:
		t.Fatalf("expected synchronous execution when wait_for_done is omitted")
	}
}

func TestSubAgentTask_WaitForDoneFalseRunsAsync(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	runStarted := make(chan struct{}, 1)
	releaseRun := make(chan struct{})
	runFinished := make(chan struct{}, 1)

	mw := New(&SubAgentConfig{
		SubAgents:    []*SubAgent{{Name: "test_sub"}},
		DefaultModel: cm,
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					runStarted <- struct{}{}
					<-releaseRun
					runFinished <- struct{}{}
					return schema.AssistantMessage("async-result", nil), nil
				},
			}, nil
		},
	})

	out := invokeTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"run asynchronously","wait_for_done":false}`)
	if out != "subagent test_sub started asynchronously" {
		t.Fatalf("unexpected async task output: %q", out)
	}

	select {
	case <-runStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected background execution to start")
	}

	select {
	case <-runFinished:
		t.Fatalf("task returned only after background execution completed")
	default:
	}

	close(releaseRun)

	select {
	case <-runFinished:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected background execution to finish")
	}
}

func TestSubAgentTask_WaitForDoneFalseRunsFailingTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	runFinished := make(chan struct{})
	mw := New(&SubAgentConfig{
		SubAgents:    []*SubAgent{{Name: "test_sub"}},
		DefaultModel: cm,
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					defer close(runFinished)
					return nil, errors.New("boom")
				},
			}, nil
		},
	})

	out := invokeTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"fail asynchronously","wait_for_done":false}`)
	if out != "subagent test_sub started asynchronously" {
		t.Fatalf("unexpected async task output: %q", out)
	}

	select {
	case <-runFinished:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected failing background execution to finish")
	}
}

func TestSubAgentTask_WaitForDoneFalseLoadsContextBeforeReturn(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	loadCalled := make(chan struct{}, 1)
	runStarted := make(chan struct{}, 1)
	releaseRun := make(chan struct{})

	mw := New(&SubAgentConfig{
		SubAgents:    []*SubAgent{{Name: "test_sub"}},
		DefaultModel: cm,
		ContextInjector: &testContextInjector{
			messages: []*schema.Message{schema.UserMessage("forked-user")},
			loadFn: func() {
				loadCalled <- struct{}{}
			},
		},
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					runStarted <- struct{}{}
					<-releaseRun
					return schema.AssistantMessage("async-result", nil), nil
				},
			}, nil
		},
	})

	out := invokeTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"run asynchronously","fork_context":true,"wait_for_done":false}`)
	if out != "subagent test_sub started asynchronously" {
		t.Fatalf("unexpected async task output: %q", out)
	}

	select {
	case <-loadCalled:
	default:
		t.Fatalf("expected context to be loaded before the async task returns")
	}

	select {
	case <-runStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected background execution to start")
	}

	close(releaseRun)
}

func TestSubAgentTask_EnableSkillInjectsFreshMiddleware(t *testing.T) {
	ctrl := gomock.NewController(t)
	cm := mock_model.NewMockToolCallingChatModel(ctrl)

	var created []middleware.Middleware
	var captured [][]middleware.Middleware
	nextID := 0

	mw := New(&SubAgentConfig{
		SubAgents:    []*SubAgent{{Name: "test_sub", EnableSkill: true}},
		DefaultModel: cm,
		SubAgentSkillMiddlewareFactory: func() middleware.Middleware {
			nextID++
			instance := &testSkillMiddleware{id: nextID}
			created = append(created, instance)
			return instance
		},
		Factory: func(ctx context.Context, chatModel model.ToolCallingChatModel, subAgent *SubAgent, defaultTools []tool.BaseTool, defaultMiddleware []middleware.Middleware) (SubAgentRunner, error) {
			snapshot := append([]middleware.Middleware(nil), defaultMiddleware...)
			captured = append(captured, snapshot)
			return &testSubAgentRunner{
				runFn: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					return schema.AssistantMessage("done", nil), nil
				},
			}, nil
		},
	})

	invokeTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"run-1","wait_for_done":true}`)
	invokeTaskTool(t, mw.newTaskTool(), `{"subagent":"test_sub","task":"run-2","wait_for_done":true}`)

	if len(created) != 2 {
		t.Fatalf("expected 2 skill middleware instances, got %d", len(created))
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 factory invocations, got %d", len(captured))
	}
	for i, middlewares := range captured {
		if len(middlewares) != 1 {
			t.Fatalf("factory invocation %d expected 1 default middleware, got %d", i, len(middlewares))
		}
		if middlewares[0] != created[i] {
			t.Fatalf("factory invocation %d did not receive the freshly created skill middleware", i)
		}
	}
	if captured[0][0] == captured[1][0] {
		t.Fatalf("expected a fresh skill middleware per subagent execution")
	}
}
