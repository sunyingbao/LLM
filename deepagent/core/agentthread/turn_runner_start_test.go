package agentthread

import (
	"context"
	"testing"

	"eino-cli/deepagent/core/checkpointer"
	"github.com/cloudwego/eino/schema"
)

type turnRunnerStartCtxKey struct{}

func turnRunnerStartCtxValue(ctx context.Context) string {
	v, _ := ctx.Value(turnRunnerStartCtxKey{}).(string)
	return v
}

func TestDeepAgentThread_SubmitInputCallsTurnRunnerStartHookBeforeResolverAndModel(t *testing.T) {
	ctx := context.Background()
	resolverSawHookValue := ""
	modelSawHookValue := ""
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				modelSawHookValue = turnRunnerStartCtxValue(ctx)
				return schema.AssistantMessage("done", nil), nil
			},
		},
	}
	th := New("thread-hook", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		return "turn-hook"
	}))

	hookCalls := 0
	result, err := th.SubmitInput(ctx, schema.UserMessage("hi"),
		WithTurnRunnerStartHook(func(ctx context.Context, req TurnRunnerStartRequest) context.Context {
			hookCalls++
			if req.ThreadID != "thread-hook" || req.TurnID != "turn-hook" {
				t.Fatalf("hook ids thread=%q turn=%q", req.ThreadID, req.TurnID)
			}
			if req.Trigger != TurnRunnerConfigForSubmit {
				t.Fatalf("hook trigger=%q, want submit", req.Trigger)
			}
			if req.Input == nil || req.Input.Content != "hi" {
				t.Fatalf("hook input=%+v", req.Input)
			}
			if req.Resume != nil {
				t.Fatalf("hook resume=%+v, want nil", req.Resume)
			}
			return context.WithValue(ctx, turnRunnerStartCtxKey{}, "from-hook")
		}),
		WithTurnRunnerConfigResolver(func(ctx context.Context, req TurnRunnerConfigRequest) (*TurnRunnerConfig, error) {
			resolverSawHookValue = turnRunnerStartCtxValue(ctx)
			return nil, nil
		}),
	)
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("hook calls=%d, want 1", hookCalls)
	}
	if err := result.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if resolverSawHookValue != "from-hook" {
		t.Fatalf("resolver hook value=%q, want from-hook (hook must run before resolver)", resolverSawHookValue)
	}
	if modelSawHookValue != "from-hook" {
		t.Fatalf("model hook value=%q, want from-hook (hook ctx must enter run ctx)", modelSawHookValue)
	}
}

func TestDeepAgentThread_SubmitInputDoesNotCallTurnRunnerStartHookForQueuedInput(t *testing.T) {
	ctx := context.Background()
	firstModelEntered := make(chan struct{})
	releaseFirstModel := make(chan struct{})
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(firstModelEntered)
				select {
				case <-releaseFirstModel:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return schema.AssistantMessage("first", nil), nil
			},
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("queued", nil), nil
			},
		},
	}
	th := New("thread-hook-queued", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		return "turn-hook-queued"
	}))

	hookCalls := 0
	first, err := th.SubmitInput(ctx, schema.UserMessage("first"),
		WithTurnRunnerStartHook(func(ctx context.Context, req TurnRunnerStartRequest) context.Context {
			hookCalls++
			return ctx
		}),
	)
	if err != nil {
		t.Fatalf("first SubmitInput() error = %v", err)
	}
	<-firstModelEntered

	queued, err := th.SubmitInput(ctx, schema.UserMessage("queued"),
		WithTurnRunnerStartHook(func(ctx context.Context, req TurnRunnerStartRequest) context.Context {
			hookCalls++
			return ctx
		}),
	)
	if err != nil {
		t.Fatalf("queued SubmitInput() error = %v", err)
	}
	if !queued.QueuedToActiveTurn {
		t.Fatalf("queued result=%+v, want queued into active turn", queued)
	}

	close(releaseFirstModel)
	if err := first.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("hook calls=%d, want 1 (queued input must not trigger the hook)", hookCalls)
	}
}

func TestDeepAgentThread_ResumeTurnCallsTurnRunnerStartHook(t *testing.T) {
	ctx := context.Background()
	modelSawHookValue := ""
	model := &threadScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("first", nil), nil
			},
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				modelSawHookValue = turnRunnerStartCtxValue(ctx)
				return schema.AssistantMessage("resumed", nil), nil
			},
		},
	}
	th := New("thread-hook-resume", &TurnRunnerConfig{
		ChatModel:       model,
		CheckpointStore: checkpointer.NewInMemoryStore(),
	}, NewSimpleTestContextManager(), make(chan Event, 16), WithTurnIDProvider(func(ctx context.Context, threadID string, input *Message) string {
		return "turn-hook-resume"
	}))

	first, err := th.SubmitInput(ctx, schema.UserMessage("first"))
	if err != nil {
		t.Fatalf("SubmitInput() error = %v", err)
	}
	if err := first.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}

	hookCalls := 0
	resumed, err := th.ResumeTurn(ctx, "turn-hook-resume", ResumeTurnOptions{
		CheckpointID:       "thread-hook-resume:turn-hook-resume",
		ResumeInterruptIDs: []string{"interrupt-1"},
		ResumeData:         map[string]any{"interrupt-1": "approved"},
		OnTurnRunnerStart: func(ctx context.Context, req TurnRunnerStartRequest) context.Context {
			hookCalls++
			if req.TurnID != "turn-hook-resume" {
				t.Fatalf("hook turn=%q", req.TurnID)
			}
			if req.Trigger != TurnRunnerConfigForResume {
				t.Fatalf("hook trigger=%q, want resume", req.Trigger)
			}
			if req.Resume == nil || req.Resume.CheckpointID != "thread-hook-resume:turn-hook-resume" {
				t.Fatalf("hook resume=%+v", req.Resume)
			}
			return context.WithValue(ctx, turnRunnerStartCtxKey{}, "from-resume-hook")
		},
	})
	if err != nil {
		t.Fatalf("ResumeTurn() error = %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("hook calls=%d, want 1", hookCalls)
	}
	if err := resumed.Wait(ctx); err != nil {
		t.Fatalf("resume Wait() error = %v", err)
	}
	if modelSawHookValue != "from-resume-hook" {
		t.Fatalf("model hook value=%q, want from-resume-hook", modelSawHookValue)
	}
}
