//go:build !windows

package thread

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/agentthread"
	"eino-cli/deepagent/core/checkpointer"
	"eino-cli/deepagent/core/middleware/planmode"
	deeptools "eino-cli/deepagent/core/tools"
	agentworker "eino-cli/deepagent/worker"

	modelcomp "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func postMessageError(_ *agentworker.PostMessageResult, err error) error {
	return err
}

func newDeepAgentThreadForTest(
	threadID string,
	cfg *agentthread.TurnConfig,
	contextManager agentthread.ContextManager,
	eventBus chan agentthread.Event,
	opts ...agentthread.Option,
) (thread *agentthread.DeepAgentThread) {
	thread = agentthread.New(
		threadID,
		cfg,
		eventBus,
		agentthread.ThreadOptions{ContextManager: contextManager},
		opts...,
	)
	return thread
}

func TestNewRuntimeValidatesDeps(t *testing.T) {
	_, err := NewRuntime(AdapterConfig{})
	if err == nil || err.Error() != "cloudagent: deep agent thread is required" {
		t.Fatalf("NewRuntime() error=%v", err)
	}
}

func TestRuntimeCallsTurnFinishedObserverOnTurnEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan agentthread.Event, 2)
	observed := make(chan agentthread.Event, 1)

	cfg := &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}
	deepThread := newDeepAgentThreadForTest("thread-1", cfg, newRuntimeCompactContextManager(), events)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Thread:    deepThread,
		EventBus:  events,
		TurnFinishedObserver: func(_ context.Context, ev agentthread.Event) {
			observed <- ev
		},
	})
	require.NoError(t, err)
	output, err := runtime.Init(ctx)
	require.NoError(t, err)

	events <- agentthread.Event{
		ID:       "event-1",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Type:     agentthread.EventTurnEnd,
		Payload:  agentthread.TurnEndPayload{},
		TS:       time.Unix(100, 0),
	}

	select {
	case ev := <-observed:
		require.Equal(t, "turn-1", ev.TurnID)
	case <-time.After(time.Second):
		t.Fatal("turn-finished observer was not called")
	}
	require.NotNil(t, readOutputItem(t, output).Event)
}

func TestRuntimeCallsThreadOutputObserverAfterOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan agentthread.Event, 2)
	observed := make(chan ThreadOutputObservation, 1)

	cfg := &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}
	deepThread := newDeepAgentThreadForTest("thread-1", cfg, newRuntimeCompactContextManager(), events)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Thread:    deepThread,
		EventBus:  events,
		ThreadOutputObserver: func(_ context.Context, obs ThreadOutputObservation) {
			observed <- obs
		},
	})
	require.NoError(t, err)
	output, err := runtime.Init(ctx)
	require.NoError(t, err)

	events <- agentthread.Event{
		ID:       "event-1",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Type:     agentthread.EventTurnEnd,
		Payload:  agentthread.TurnEndPayload{},
		TS:       time.Unix(100, 0),
	}

	item := readOutputItem(t, output)
	require.NotNil(t, item.Event)
	require.Equal(t, "thread-1", item.Event.ThreadID)
	select {
	case obs := <-observed:
		require.Equal(t, "session-1", obs.SessionID)
		require.Equal(t, "thread-1", obs.ThreadID)
		require.NotNil(t, obs.Item.Event)
		require.Equal(t, "thread-1", obs.Item.Event.ThreadID)
		require.Equal(t, item.Event.Type, obs.Item.Event.Type)
	case <-time.After(time.Second):
		t.Fatal("thread output observer was not called")
	}
}

func TestRuntimeEmitAgentEventCallsThreadOutputObserver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan agentthread.Event, 2)
	observed := make(chan ThreadOutputObservation, 1)

	cfg := &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}
	deepThread := newDeepAgentThreadForTest("thread-1", cfg, newRuntimeCompactContextManager(), events)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Thread:    deepThread,
		EventBus:  events,
		ThreadOutputObserver: func(_ context.Context, obs ThreadOutputObservation) {
			observed <- obs
		},
	})
	require.NoError(t, err)
	output, err := runtime.Init(ctx)
	require.NoError(t, err)

	runtime.emitAgentEvent(ctx, agentthread.Event{
		ID:      "event-1",
		TurnID:  "turn-1",
		Type:    agentthread.EventTurnEnd,
		Payload: agentthread.TurnEndPayload{},
	})

	require.Equal(t, "turn-1", readOutputItem(t, output).Event.TurnID)
	select {
	case obs := <-observed:
		require.NotNil(t, obs.Item.Event)
		require.Equal(t, "turn-1", obs.Item.Event.TurnID)
	case <-time.After(time.Second):
		t.Fatal("thread output observer was not called")
	}
}

func TestRuntimeThreadOutputObserverPanicDoesNotStopForwarding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan agentthread.Event, 4)

	cfg := &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}
	deepThread := newDeepAgentThreadForTest("thread-1", cfg, newRuntimeCompactContextManager(), events)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Thread:    deepThread,
		EventBus:  events,
		ThreadOutputObserver: func(context.Context, ThreadOutputObservation) {
			panic("observer failed")
		},
	})
	require.NoError(t, err)
	output, err := runtime.Init(ctx)
	require.NoError(t, err)

	events <- agentthread.Event{ID: "event-1", TurnID: "turn-1", Type: agentthread.EventTurnEnd, Payload: agentthread.TurnEndPayload{}}
	events <- agentthread.Event{ID: "event-2", TurnID: "turn-2", Type: agentthread.EventTurnEnd, Payload: agentthread.TurnEndPayload{}}

	require.Equal(t, "turn-1", readOutputItem(t, output).Event.TurnID)
	require.Equal(t, "turn-2", readOutputItem(t, output).Event.TurnID)
}

func TestRuntimeThreadOutputObserverDoesNotBlockForwarding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan agentthread.Event, 4)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	cfg := &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}
	deepThread := newDeepAgentThreadForTest("thread-1", cfg, newRuntimeCompactContextManager(), events)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Thread:    deepThread,
		EventBus:  events,
		ThreadOutputObserver: func(context.Context, ThreadOutputObservation) {
			once.Do(func() { close(entered) })
			<-release
		},
	})
	require.NoError(t, err)
	output, err := runtime.Init(ctx)
	require.NoError(t, err)
	defer close(release)

	events <- agentthread.Event{ID: "event-1", TurnID: "turn-1", Type: agentthread.EventTurnEnd, Payload: agentthread.TurnEndPayload{}}
	require.Equal(t, "turn-1", readOutputItem(t, output).Event.TurnID)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("thread output observer was not called")
	}

	events <- agentthread.Event{ID: "event-2", TurnID: "turn-2", Type: agentthread.EventTurnEnd, Payload: agentthread.TurnEndPayload{}}
	require.Equal(t, "turn-2", readOutputItem(t, output).Event.TurnID)
}

func TestRuntimeThreadOutputObserverMatchesOutputOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan agentthread.Event, 16)
	observed := make(chan string, 16)

	cfg := &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}
	deepThread := newDeepAgentThreadForTest("thread-1", cfg, newRuntimeCompactContextManager(), events)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Thread:    deepThread,
		EventBus:  events,
		ThreadOutputObserver: func(_ context.Context, obs ThreadOutputObservation) {
			observed <- obs.Item.Event.TurnID
		},
	})
	require.NoError(t, err)
	output, err := runtime.Init(ctx)
	require.NoError(t, err)

	events <- agentthread.Event{ID: "event-1", TurnID: "turn-1", Type: agentthread.EventTurnEnd, Payload: agentthread.TurnEndPayload{}}
	runtime.emitAgentEvent(ctx, agentthread.Event{ID: "event-2", TurnID: "turn-2", Type: agentthread.EventTurnEnd, Payload: agentthread.TurnEndPayload{}})
	events <- agentthread.Event{ID: "event-3", TurnID: "turn-3", Type: agentthread.EventTurnEnd, Payload: agentthread.TurnEndPayload{}}
	runtime.emitAgentEvent(ctx, agentthread.Event{ID: "event-4", TurnID: "turn-4", Type: agentthread.EventTurnEnd, Payload: agentthread.TurnEndPayload{}})

	outputOrder := make([]string, 0, 4)
	for len(outputOrder) < 4 {
		outputOrder = append(outputOrder, readOutputItem(t, output).Event.TurnID)
	}
	observerOrder := make([]string, 0, 4)
	for len(observerOrder) < 4 {
		select {
		case turnID := <-observed:
			observerOrder = append(observerOrder, turnID)
		case <-time.After(time.Second):
			t.Fatal("thread output observer was not called")
		}
	}
	require.Equal(t, outputOrder, observerOrder)
}

func TestThreadOutputObserverReceivesDefensiveCopy(t *testing.T) {
	persist := true
	fanout := false
	item := agentworker.ThreadOutputItem{
		Event: &agentworker.Event{
			ID:                "event-1",
			ThreadID:          "thread-1",
			TurnID:            "turn-1",
			Type:              "agent_result",
			Payload:           []byte("ok"),
			Metadata:          map[string]string{"k": "v"},
			PersistToEventLog: &persist,
			FanoutToSession:   &fanout,
		},
		Yield: &agentworker.ThreadYield{
			Reason: "approval",
			Block: &agentworker.PendingBlock{
				TurnID:       "turn-1",
				InterruptID:  "interrupt-1",
				CheckpointID: "checkpoint-1",
				Kind:         "approval",
			},
		},
	}
	runtime := &Runtime{
		sessionID: "session-1",
		threadID:  "thread-1",
		threadOutputObserver: func(_ context.Context, obs ThreadOutputObservation) {
			obs.Item.Event.Payload[0] = 'x'
			obs.Item.Event.Metadata["k"] = "changed"
			*obs.Item.Event.PersistToEventLog = false
			*obs.Item.Event.FanoutToSession = true
			obs.Item.Yield.Block.InterruptID = "changed"
		},
		observerQueue: make(chan ThreadOutputObservation, 1),
	}

	obs, ok := runtime.threadOutputObservation(item)
	require.True(t, ok)
	runtime.callThreadOutputObserver(context.Background(), obs)

	require.Equal(t, []byte("ok"), item.Event.Payload)
	require.Equal(t, "v", item.Event.Metadata["k"])
	require.True(t, *item.Event.PersistToEventLog)
	require.False(t, *item.Event.FanoutToSession)
	require.Equal(t, "interrupt-1", item.Yield.Block.InterruptID)
}

func TestApprovalRemembererFunc(t *testing.T) {
	var got protoinput.ResumeTurnPayload
	remember := ApprovalRemembererFunc(func(_ context.Context, payload protoinput.ResumeTurnPayload) {
		got = payload
	})
	remember.RememberApproval(context.Background(), protoinput.ResumeTurnPayload{ToolName: "exec_command"})
	if got.ToolName != "exec_command" {
		t.Fatalf("RememberApproval() payload=%+v", got)
	}
}

func TestParseUserMessageUsesStructuredProtocol(t *testing.T) {
	payload, err := json.Marshal(protoinput.UserMessage{
		Mode: protoinput.UserMessageModeImplPlan,
		Parts: []protoinput.MessagePart{
			{Type: protoinput.MessagePartTypeText, Text: "implement it"},
			{Type: protoinput.MessagePartTypeImage, Base64Data: "image-data", MIMEType: "image/png"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error=%v", err)
	}
	got, err := parseUserMessage(&agentworker.Message{
		Type:    MessageTypeInput,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("parseUserMessage() error=%v", err)
	}
	if got.Mode != protoinput.UserMessageModeImplPlan || len(got.Parts) != 2 || got.Parts[0].Text != "implement it" {
		t.Fatalf("message=%+v", got)
	}
}

func TestUserInputMessageBuildsPlainTextContent(t *testing.T) {
	got, err := cloudUserMessageToSchemaMessage(protoinput.UserMessage{
		Parts: []protoinput.MessagePart{
			{Type: protoinput.MessagePartTypeText, Text: "first"},
			{Type: protoinput.MessagePartTypeText, Text: "second"},
		},
	})
	if err != nil {
		t.Fatalf("cloudUserMessageToSchemaMessage() error=%v", err)
	}
	if got.Content != "first\nsecond" || len(got.UserInputMultiContent) != 0 {
		t.Fatalf("message=%+v", got)
	}
}

func TestUserInputMessageBuildsEinoMultimodalMessage(t *testing.T) {
	got, err := cloudUserMessageToSchemaMessage(protoinput.UserMessage{
		Parts: []protoinput.MessagePart{
			{Type: protoinput.MessagePartTypeText, Text: "describe this"},
			{Type: protoinput.MessagePartTypeImage, URL: "https://example.com/a.png", MIMEType: "image/png"},
		},
	})
	if err != nil {
		t.Fatalf("cloudUserMessageToSchemaMessage() error=%v", err)
	}
	if got.Content != "" || len(got.UserInputMultiContent) != 2 {
		t.Fatalf("message=%+v", got)
	}
	if got.UserInputMultiContent[0].Text != "describe this" {
		t.Fatalf("text part=%+v", got.UserInputMultiContent[0])
	}
	if got.UserInputMultiContent[1].Image == nil || got.UserInputMultiContent[1].Image.URL == nil {
		t.Fatalf("image part=%+v", got.UserInputMultiContent[1])
	}
}

func TestUserInputMessagePreservesExtra(t *testing.T) {
	got, err := cloudUserMessageToSchemaMessage(protoinput.UserMessage{
		Extra: map[string]json.RawMessage{
			"adapter": json.RawMessage(`{"message_id":"msg_1"}`),
		},
		Parts: []protoinput.MessagePart{
			{
				Type: protoinput.MessagePartTypeText,
				Text: "describe this",
				Extra: map[string]json.RawMessage{
					"adapter": json.RawMessage(`{"part_id":"text_1"}`),
				},
			},
			{
				Type:     protoinput.MessagePartTypeImage,
				URL:      "https://example.com/a.png",
				MIMEType: "image/png",
				Extra: map[string]json.RawMessage{
					"adapter": json.RawMessage(`{"resource_id":"res_1"}`),
				},
			},
		},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"message_id":"msg_1"}`, string(got.Extra["adapter"].(json.RawMessage)))
	require.Len(t, got.UserInputMultiContent, 2)
	require.JSONEq(t, `{"part_id":"text_1"}`, string(got.UserInputMultiContent[0].Extra["adapter"].(json.RawMessage)))
	require.NotNil(t, got.UserInputMultiContent[1].Image)
	require.JSONEq(t, `{"resource_id":"res_1"}`, string(got.UserInputMultiContent[1].Image.Extra["adapter"].(json.RawMessage)))
}

func TestResumeData(t *testing.T) {
	approval, err := resumeData(context.Background(), protoinput.ResumeTurnPayload{
		InterruptID: "i1",
		Approval:    &protoinput.ApprovalDecision{Approved: false, Reason: "try another way"},
	}, nil)
	if err != nil {
		t.Fatalf("resumeData(approval) error=%v", err)
	}
	if approval["i1"] == nil {
		t.Fatalf("resumeData(approval) missing interrupt data")
	}

	planInput := &planmode.RequestUserInputResponse{Answers: map[string]planmode.RequestUserInputAnswer{
		"q1": {Answers: []string{"yes"}},
	}}
	input, err := resumeData(context.Background(), protoinput.ResumeTurnPayload{
		InterruptID: "i2",
		RequestUserInput: &protoinput.RequestUserInputResponse{Answers: map[string]protoinput.RequestUserInputAnswer{
			"q1": {Answers: []string{"yes"}},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("resumeData(request_user_input) error=%v", err)
	}
	if !reflect.DeepEqual(input["i2"], planInput) {
		t.Fatalf("resumeData(request_user_input)=%#v, want %+v", input["i2"], planInput)
	}

	followUp, err := resumeData(context.Background(), protoinput.ResumeTurnPayload{
		InterruptID: "i3",
		Interrupt: &protoinput.InterruptResumePayload{
			Kind: "follow_up",
			Data: json.RawMessage(`{"user_answer":"ship it"}`),
		},
	}, nil)
	if err != nil {
		t.Fatalf("resumeData(follow_up) error=%v", err)
	}
	followUpInfo, ok := followUp["i3"].(*deeptools.FollowUpInfo)
	if !ok || followUpInfo.UserAnswer != "ship it" {
		t.Fatalf("resumeData(follow_up)=%#v", followUp["i3"])
	}

	type customResume struct {
		Choice string
	}
	custom, err := resumeData(context.Background(), protoinput.ResumeTurnPayload{
		InterruptID: "i4",
		Interrupt: &protoinput.InterruptResumePayload{
			Kind:     "custom",
			InfoType: "*example.CustomInfo",
			Data:     json.RawMessage(`{"choice":"continue"}`),
		},
	}, func(_ context.Context, payload protoinput.ResumeTurnPayload) (any, error) {
		var body struct {
			Choice string `json:"choice"`
		}
		if err := json.Unmarshal(payload.Interrupt.Data, &body); err != nil {
			return nil, err
		}
		return &customResume{Choice: body.Choice}, nil
	})
	if err != nil {
		t.Fatalf("resumeData(custom) error=%v", err)
	}
	if got, ok := custom["i4"].(*customResume); !ok || got.Choice != "continue" {
		t.Fatalf("resumeData(custom)=%#v", custom["i4"])
	}

	_, err = resumeData(context.Background(), protoinput.ResumeTurnPayload{
		InterruptID: "i5",
		Interrupt: &protoinput.InterruptResumePayload{
			Kind:     "custom",
			InfoType: "*example.CustomInfo",
			Data:     json.RawMessage(`{"choice":"continue"}`),
		},
	}, nil)
	if err == nil {
		t.Fatal("resumeData(custom without decoder) error=nil, want error")
	}

	_, err = resumeData(context.Background(), protoinput.ResumeTurnPayload{InterruptID: "i6"}, nil)
	if err == nil {
		t.Fatal("resumeData(empty) error=nil, want error")
	}
}

func TestConsumedMessageIDs(t *testing.T) {
	msg := schema.UserMessage("hello")
	attachAttribute(msg, MessageAttribute{MessageID: "m1", SenderID: "u1", SenderType: "user"})

	got := ConsumedMessageIDs([]*schema.Message{msg})
	if !reflect.DeepEqual(got, []string{"m1"}) {
		t.Fatalf("ConsumedMessageIDs()=%v", got)
	}
}

func TestResumeTurnPayloadUsesPlanModeSchema(t *testing.T) {
	raw := []byte(`{"turn_id":"t1","checkpoint_id":"c1","interrupt_id":"i1","request_user_input":{"answers":{"q1":{"answers":["yes"]}}}}`)
	var payload protoinput.ResumeTurnPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal() error=%v", err)
	}
	if err := payload.Validate(); err != nil {
		t.Fatalf("Validate() error=%v", err)
	}
	if payload.RequestUserInput == nil || payload.RequestUserInput.Answers["q1"].Answers[0] != "yes" {
		t.Fatalf("RequestUserInput=%+v", payload.RequestUserInput)
	}
}

func TestRuntimeCompactExposesActiveTurn(t *testing.T) {
	ctx := context.Background()
	cm := newRuntimeCompactContextManager()
	runtime, output := newTestCompactRuntime(t, cm, nil)

	done := make(chan error, 1)
	go func() {
		done <- postMessageError(runtime.PostMessage(ctx, &agentworker.Message{
			ID:       "m-compact",
			Type:     MessageTypeCompact,
			Metadata: map[string]string{"trace_id": "compact-1"},
		}))
	}()

	select {
	case <-cm.compactEntered:
	case <-time.After(time.Second):
		t.Fatal("compact did not start")
	}
	active := runtime.ActiveTurn()
	if active == nil || active.TurnID != "compact_m-compact" || !reflect.DeepEqual(active.ConsumedMessageIDs, []string{"m-compact"}) {
		t.Fatalf("ActiveTurn()=%+v", active)
	}

	started := readOutputItem(t, output)
	if started.Event == nil || started.Event.Type != agentworker.EventType(protoevent.EventTypeCompactStarted.String()) || started.Event.TurnID != "compact_m-compact" {
		t.Fatalf("compact started output=%+v", started)
	}
	startMeta := decodeConsumedInputsMeta(t, started.Event)
	if len(startMeta) != 1 || startMeta[0]["trace_id"] != "compact-1" {
		t.Fatalf("compact started consumed_inputs_meta = %+v", startMeta)
	}

	close(cm.releaseCompact)
	if err := <-done; err != nil {
		t.Fatalf("PostMessage(compact) error=%v", err)
	}
	item := readOutputItem(t, output)
	if item.Event == nil || item.Event.Type != "CONTEXT_COMPACTED" || item.Event.TurnID != "compact_m-compact" {
		t.Fatalf("compact output=%+v", item)
	}
	meta := decodeConsumedInputsMeta(t, item.Event)
	if len(meta) != 1 || meta[0]["trace_id"] != "compact-1" {
		t.Fatalf("compact consumed_inputs_meta = %+v", meta)
	}
	if runtime.ActiveTurn() != nil {
		t.Fatalf("ActiveTurn() after compact=%+v", runtime.ActiveTurn())
	}
}

func TestPostResumeCancelTurnEmitsTerminalEventsWithoutResuming(t *testing.T) {
	ctx := context.Background()
	runtime, output := newTestCompactRuntime(t, newRuntimeCompactContextManager(), nil)
	payload := protoinput.ResumeTurnPayload{
		TurnID:             "turn-1",
		CheckpointID:       "checkpoint-1",
		InterruptID:        "interrupt-1",
		ConsumedMessageIDs: []string{"message-1"},
		Approval:           &protoinput.ApprovalDecision{Reason: "user_stop", CancelTurn: true},
	}

	if _, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:      "resume-1",
		Type:    MessageTypeResumeTurn,
		Payload: mustJSON(t, payload),
	}); err != nil {
		t.Fatalf("PostMessage(resume cancel) error=%v", err)
	}

	interrupted := readOutputEventType(t, output, "TURN_INTERRUPTED")
	if interrupted.Event.TurnID != "turn-1" {
		t.Fatalf("interrupted event=%+v", interrupted.Event)
	}
	var errorPayload struct {
		Message            string   `json:"message"`
		ConsumedMessageIDs []string `json:"consumed_message_ids"`
	}
	if err := json.Unmarshal(interrupted.Event.Payload, &errorPayload); err != nil {
		t.Fatalf("unmarshal interrupted payload: %v", err)
	}
	if !reflect.DeepEqual(errorPayload.ConsumedMessageIDs, []string{"message-1"}) {
		t.Fatalf("interrupted consumed ids=%v", errorPayload.ConsumedMessageIDs)
	}
	finished := readOutputEventType(t, output, "TURN_FINISHED")
	if finished.Yield == nil || finished.Event.TurnID != "turn-1" {
		t.Fatalf("finished item=%+v", finished)
	}
	if runtime.ActiveTurn() != nil {
		t.Fatalf("ActiveTurn()=%+v, want nil", runtime.ActiveTurn())
	}
}

func TestRunnerConfigMarksSubmitAndResume(t *testing.T) {
	ctx := context.Background()
	var requests []TurnStartRequest
	runtime := &Runtime{
		turnConfig: func(_ context.Context, req TurnStartRequest) (*agentthread.TurnConfig, error) {
			requests = append(requests, req)
			return &agentthread.TurnConfig{
				Agent: deepagents.Config{
					MaxSteps: 99,
				},
			}, nil
		},
	}

	submitMsg := &agentworker.Message{ID: "m-submit"}
	got, err := runtime.runConfig(ctx, "turn-submit", submitMsg, protoinput.UserMessageModeImplPlan, false)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Agent.MaxSteps != 99 {
		t.Fatalf("submit runner config=%+v, want MaxSteps=99", got)
	}

	resumeMsg := &agentworker.Message{ID: "m-resume"}
	got, err = runtime.runConfig(ctx, "turn-resume", resumeMsg, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Agent.MaxSteps != 99 {
		t.Fatalf("resume runner config=%+v, want MaxSteps=99", got)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%+v, want 2", requests)
	}
	if requests[0].TurnID != "turn-submit" || requests[0].Mode != protoinput.UserMessageModeImplPlan || requests[0].Message != submitMsg || requests[0].Resume {
		t.Fatalf("submit request=%+v", requests[0])
	}
	if requests[1].TurnID != "turn-resume" || requests[1].Mode != "" || requests[1].Message != resumeMsg || !requests[1].Resume {
		t.Fatalf("resume request=%+v", requests[1])
	}
}

func TestRuntimePostResumeTurnResolvesConfigWithoutStoredSnapshot(t *testing.T) {
	ctx := context.Background()
	resumedModel := make(chan struct{})
	model := &runtimeScriptedModel{
		handlers: []func(context.Context, []*schema.Message) (*schema.Message, error){
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("first", nil), nil
			},
			func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				close(resumedModel)
				return schema.AssistantMessage("resumed", nil), nil
			},
		},
	}
	checkpoints := checkpointer.NewInMemoryStore()
	events := make(chan agentthread.Event, 16)
	deepThread := newDeepAgentThreadForTest("thread-reclaim", &agentthread.TurnConfig{
		Agent: deepagents.Config{
			Model:           model,
			CheckpointStore: checkpoints,
		},
	}, newRuntimeCompactContextManager(), events, agentthread.WithTurnIDProvider(func(ctx context.Context, threadID string, input *agentthread.Message) string {
		return "turn-reclaim"
	}))
	first, err := deepThread.SubmitInput(ctx, schema.UserMessage("first"))
	if err != nil {
		t.Fatalf("SubmitInput() error=%v", err)
	}
	if err := first.TurnHandle.Wait(ctx); err != nil {
		t.Fatalf("first Wait() error=%v", err)
	}

	resumeMsg := &agentworker.Message{ID: "resume-message"}
	resolverCalls := 0
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "session-reclaim",
		ThreadID:  "thread-reclaim",
		Thread:    deepThread,
		EventBus:  events,
		TurnConfig: func(_ context.Context, req TurnStartRequest) (*agentthread.TurnConfig, error) {
			resolverCalls++
			if req.TurnID != "turn-reclaim" || req.Mode != "" || req.Message != resumeMsg || !req.Resume {
				t.Fatalf("resume resolver request=%+v", req)
			}
			return &agentthread.TurnConfig{
				Agent: deepagents.Config{
					Model:           model,
					CheckpointStore: checkpoints,
					MaxSteps:        33,
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRuntime() error=%v", err)
	}

	result, err := runtime.postResumeTurn(ctx, resumeTurnCommand{
		message: resumeMsg,
		payload: protoinput.ResumeTurnPayload{
			TurnID:       "turn-reclaim",
			CheckpointID: "thread-reclaim:turn-reclaim",
			InterruptID:  "interrupt-reclaim",
			Approval:     &protoinput.ApprovalDecision{Approved: true},
		},
	})
	if err != nil {
		t.Fatalf("postResumeTurn() error=%v", err)
	}
	if result == nil || result.TurnID != "turn-reclaim" {
		t.Fatalf("postResumeTurn() result=%+v", result)
	}
	if resolverCalls != 1 {
		t.Fatalf("resolver calls=%d, want 1", resolverCalls)
	}
	select {
	case <-resumedModel:
	case <-time.After(time.Second):
		t.Fatal("resume model was not called")
	}
}

func TestRuntimePostMessageBuildsTurnConfigOnlyForNewTurn(t *testing.T) {
	ctx := context.Background()
	modelEntered := make(chan struct{})
	releaseModel := make(chan struct{})
	model := &runtimeBlockingModel{entered: modelEntered, release: releaseModel}
	eventBus := make(chan agentthread.Event, 16)
	cm := newRuntimeCompactContextManager()
	thread := newDeepAgentThreadForTest("1", &agentthread.TurnConfig{
		Agent: deepagents.Config{
			Model:           model,
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}, cm, eventBus, agentthread.WithTurnIDProvider(func(ctx context.Context, threadID string, input *agentthread.Message) string {
		return "turn-config"
	}))
	resolveCalls := 0
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "100",
		ThreadID:  "1",
		Thread:    thread,
		EventBus:  eventBus,
		TurnConfig: func(context.Context, TurnStartRequest) (*agentthread.TurnConfig, error) {
			resolveCalls++
			return &agentthread.TurnConfig{
				Agent: deepagents.Config{
					Model:           model,
					CheckpointStore: checkpointer.NewInMemoryStore(),
					MaxSteps:        55,
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRuntime() error=%v", err)
	}
	if _, err := runtime.Init(ctx); err != nil {
		t.Fatalf("Init() error=%v", err)
	}

	firstPayload := mustJSON(t, protoinput.UserMessage{
		Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "first"}},
	})
	if _, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:      "m-first",
		Type:    MessageTypeInput,
		Payload: firstPayload,
	}); err != nil {
		t.Fatalf("first PostMessage() error=%v", err)
	}
	select {
	case <-modelEntered:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}
	if resolveCalls != 1 {
		t.Fatalf("resolver calls after first input=%d, want 1", resolveCalls)
	}

	queuedPayload := mustJSON(t, protoinput.UserMessage{
		Mode:  protoinput.UserMessageModeImplPlan,
		Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "queued"}},
	})
	if _, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:      "m-queued",
		Type:    MessageTypeInput,
		Payload: queuedPayload,
	}); err != nil {
		t.Fatalf("queued PostMessage() error=%v", err)
	}
	if resolveCalls != 1 {
		t.Fatalf("resolver calls after queued input=%d, want 1", resolveCalls)
	}

	close(releaseModel)
	waitUntilRuntimeInactive(t, runtime, time.Second)
}

func TestRuntimeCompactRejectsActiveTurn(t *testing.T) {
	ctx := context.Background()
	modelEntered := make(chan struct{})
	releaseModel := make(chan struct{})
	model := &runtimeBlockingModel{entered: modelEntered, release: releaseModel}
	cm := newRuntimeCompactContextManager()
	runtime, output := newTestCompactRuntime(t, cm, model)

	payload := mustJSON(t, protoinput.UserMessage{
		Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "work"}},
	})
	if _, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:      "m-input",
		Type:    MessageTypeInput,
		Payload: payload,
	}); err != nil {
		t.Fatalf("PostMessage(input) error=%v", err)
	}
	select {
	case <-modelEntered:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}

	if _, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:   "m-compact",
		Type: MessageTypeCompact,
	}); err != nil {
		t.Fatalf("PostMessage(compact) error=%v", err)
	}
	item := readOutputEventType(t, output, "ERROR")
	if item.Event == nil || item.Event.Type != "ERROR" || item.Event.TurnID != "compact_m-compact" {
		t.Fatalf("compact reject output=%+v", item)
	}
	if cm.compactCalls() != 0 {
		t.Fatalf("compact calls=%d, want 0", cm.compactCalls())
	}

	close(releaseModel)
}

func TestRuntimePostMessagePropagatesMessageMetadataToOutputEvents(t *testing.T) {
	ctx := context.Background()
	modelEntered := make(chan struct{})
	releaseModel := make(chan struct{})
	model := &runtimeBlockingModel{entered: modelEntered, release: releaseModel}
	runtime, output := newTestCompactRuntime(t, newRuntimeCompactContextManager(), model)
	defer close(releaseModel)

	payload := mustJSON(t, protoinput.UserMessage{
		Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "work"}},
	})
	if _, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:       "m-input",
		Type:     MessageTypeInput,
		Payload:  payload,
		Metadata: map[string]string{"trace_id": "trace-1", "biz_id": "biz-1"},
	}); err != nil {
		t.Fatalf("PostMessage(input) error=%v", err)
	}

	item := readOutputEventType(t, output, agentworker.EventType(protoevent.EventTypeTurnStarted.String()))
	var eventPayload struct {
		ConsumedInputsMeta []map[string]string `json:"consumed_inputs_meta"`
	}
	if err := json.Unmarshal(item.Event.Payload, &eventPayload); err != nil {
		t.Fatalf("unmarshal turn started payload: %v", err)
	}
	if len(eventPayload.ConsumedInputsMeta) != 1 ||
		eventPayload.ConsumedInputsMeta[0]["trace_id"] != "trace-1" ||
		eventPayload.ConsumedInputsMeta[0]["biz_id"] != "biz-1" {
		t.Fatalf("consumed_inputs_meta = %+v", eventPayload.ConsumedInputsMeta)
	}

	select {
	case <-modelEntered:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}
}

func TestRuntimeInterruptPassesTimeoutToDeepAgentThread(t *testing.T) {
	ctx := context.Background()
	modelEntered := make(chan struct{})
	releaseModel := make(chan struct{})
	model := &runtimeBlockingReturnModel{entered: modelEntered, release: releaseModel}
	cm := newRuntimeCompactContextManager()
	runtime, output := newTestCompactRuntime(t, cm, model)

	payload := mustJSON(t, protoinput.UserMessage{
		Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: "work"}},
	})
	if _, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:      "m-input",
		Type:    MessageTypeInput,
		Payload: payload,
	}); err != nil {
		t.Fatalf("PostMessage(input) error=%v", err)
	}
	select {
	case <-modelEntered:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}

	timeout := 30 * time.Millisecond
	if err := runtime.Interrupt(ctx, agentworker.ThreadInterruptRequest{
		Kind:    agentworker.ThreadInterruptKindCancelInput,
		Reason:  "test interrupt",
		Timeout: &timeout,
	}); err != nil {
		t.Fatalf("Interrupt() error=%v", err)
	}
	interrupted := readOutputEventType(t, output, "TURN_INTERRUPTED")
	if interrupted.Event == nil || interrupted.Event.TurnID == "" {
		t.Fatalf("interrupted output=%+v", interrupted)
	}
	waitUntilRuntimeInactive(t, runtime, time.Second)
	close(releaseModel)
}

func TestRuntimeInterruptCancelsCompact(t *testing.T) {
	ctx := context.Background()
	cm := newRuntimeCompactContextManager()
	runtime, output := newTestCompactRuntime(t, cm, nil)

	done := make(chan error, 1)
	go func() {
		done <- postMessageError(runtime.PostMessage(ctx, &agentworker.Message{
			ID:       "m-compact",
			Type:     MessageTypeCompact,
			Metadata: map[string]string{"trace_id": "compact-interrupt"},
		}))
	}()
	select {
	case <-cm.compactEntered:
	case <-time.After(time.Second):
		t.Fatal("compact did not start")
	}
	started := readOutputItem(t, output)
	if started.Event == nil || started.Event.Type != agentworker.EventType(protoevent.EventTypeCompactStarted.String()) || started.Event.TurnID != "compact_m-compact" {
		t.Fatalf("compact started output=%+v", started)
	}
	startMeta := decodeConsumedInputsMeta(t, started.Event)
	if len(startMeta) != 1 || startMeta[0]["trace_id"] != "compact-interrupt" {
		t.Fatalf("compact started consumed_inputs_meta = %+v", startMeta)
	}

	if err := runtime.Interrupt(ctx, agentworker.ThreadInterruptRequest{
		Kind:             agentworker.ThreadInterruptKindWorkerShutdownTimeout,
		ControlMessageID: "ctrl-1",
		Reason:           "test interrupt",
	}); err != nil {
		t.Fatalf("Interrupt() error=%v", err)
	}
	item := readOutputItem(t, output)
	if item.Event == nil || item.Event.Type != "COMPACT_INTERRUPTED" || item.Event.TurnID != "compact_m-compact" {
		t.Fatalf("interrupt output=%+v", item)
	}
	meta := decodeConsumedInputsMeta(t, item.Event)
	if len(meta) != 1 || meta[0]["trace_id"] != "compact-interrupt" {
		t.Fatalf("compact interrupted consumed_inputs_meta = %+v", meta)
	}
	if err := <-done; err != nil {
		t.Fatalf("PostMessage(compact) error=%v", err)
	}
	if runtime.ActiveTurn() != nil {
		t.Fatalf("ActiveTurn() after interrupt=%+v", runtime.ActiveTurn())
	}
}

func TestRuntimeRejectsPostMessageAfterClose(t *testing.T) {
	ctx := context.Background()
	cm := newRuntimeCompactContextManager()
	runtime, _ := newTestCompactRuntime(t, cm, nil)

	if err := runtime.Close(ctx); err != nil {
		t.Fatalf("Close() error=%v", err)
	}
	_, err := runtime.PostMessage(ctx, &agentworker.Message{
		ID:   "m-closed",
		Type: MessageTypeCompact,
	})
	if !errors.Is(err, agentworker.ErrThreadClosed) {
		t.Fatalf("PostMessage() error=%v, want ErrThreadClosed", err)
	}
}

func TestRuntimeDropsLateEventAfterClose(t *testing.T) {
	ctx := context.Background()
	cm := newRuntimeCompactContextManager()
	runtime, _ := newTestCompactRuntime(t, cm, nil)

	require.NoError(t, runtime.Close(ctx))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.emitAgentEvent(ctx, agentthread.Event{
			ID:       "late-event",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Type:     agentthread.EventTurnEnd,
			Payload:  agentthread.TurnEndPayload{},
			TS:       time.Unix(100, 0),
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("late event blocked after runtime close")
	}
}

type runtimeCompactContextManager struct {
	mu sync.Mutex

	compactEntered chan struct{}
	releaseCompact chan struct{}
	calls          int
}

func newRuntimeCompactContextManager() *runtimeCompactContextManager {
	return &runtimeCompactContextManager{
		compactEntered: make(chan struct{}),
		releaseCompact: make(chan struct{}),
	}
}

func (m *runtimeCompactContextManager) ReloadHistory(ctx context.Context) error {
	_ = ctx
	return nil
}

func (m *runtimeCompactContextManager) AddHistory(ctx context.Context, turnID string, msg ...*schema.Message) error {
	_, _ = ctx, turnID
	_ = msg
	return nil
}

func (m *runtimeCompactContextManager) History(ctx context.Context) []*schema.Message {
	_ = ctx
	return nil
}

func (m *runtimeCompactContextManager) ContextUsage() agentthread.ContextUsageSnapshot {
	return agentthread.ContextUsageSnapshot{}
}

func (m *runtimeCompactContextManager) RecordModelUsage(ctx context.Context, usage *modelcomp.TokenUsage) {
	_, _ = ctx, usage
}

func (m *runtimeCompactContextManager) Compact(ctx context.Context, turnID string) (*agentthread.ContextCompactedPayload, error) {
	_ = turnID
	m.mu.Lock()
	m.calls++
	firstCall := m.calls == 1
	m.mu.Unlock()
	if firstCall {
		close(m.compactEntered)
	}
	select {
	case <-m.releaseCompact:
		return &agentthread.ContextCompactedPayload{StrategyID: "test"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *runtimeCompactContextManager) CompactNeeded(ctx context.Context) bool {
	_ = ctx
	return false
}

func (m *runtimeCompactContextManager) compactCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type runtimeBlockingModel struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *runtimeBlockingModel) Generate(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.Message, error) {
	_, _ = input, opts
	m.once.Do(func() { close(m.entered) })
	select {
	case <-m.release:
		return schema.AssistantMessage("done", nil), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *runtimeBlockingModel) Stream(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.StreamReader[*schema.Message], error) {
	_, _ = input, opts
	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		msg, err := m.Generate(ctx, input, opts...)
		if err != nil {
			writer.Send(nil, err)
			return
		}
		writer.Send(msg, nil)
	}()
	return reader, nil
}

func (m *runtimeBlockingModel) BindTools(tools []*schema.ToolInfo) error {
	_ = tools
	return nil
}

func (m *runtimeBlockingModel) WithTools(tools []*schema.ToolInfo) (modelcomp.ToolCallingChatModel, error) {
	_ = tools
	return m, nil
}

var _ modelcomp.ToolCallingChatModel = (*runtimeBlockingModel)(nil)

type runtimeBlockingReturnModel struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *runtimeBlockingReturnModel) Generate(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.Message, error) {
	_, _ = input, opts
	m.once.Do(func() { close(m.entered) })
	select {
	case <-m.release:
		return schema.AssistantMessage("done", nil), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *runtimeBlockingReturnModel) Stream(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *runtimeBlockingReturnModel) BindTools(tools []*schema.ToolInfo) error {
	_ = tools
	return nil
}

func (m *runtimeBlockingReturnModel) WithTools(tools []*schema.ToolInfo) (modelcomp.ToolCallingChatModel, error) {
	_ = tools
	return m, nil
}

var _ modelcomp.ToolCallingChatModel = (*runtimeBlockingReturnModel)(nil)

type runtimeScriptedModel struct {
	mu       sync.Mutex
	handlers []func(context.Context, []*schema.Message) (*schema.Message, error)
}

func (m *runtimeScriptedModel) Generate(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.Message, error) {
	_ = opts
	m.mu.Lock()
	var handler func(context.Context, []*schema.Message) (*schema.Message, error)
	if len(m.handlers) > 0 {
		handler = m.handlers[0]
		m.handlers = m.handlers[1:]
	}
	m.mu.Unlock()
	if handler == nil {
		return schema.AssistantMessage("done", nil), nil
	}
	return handler(ctx, input)
}

func (m *runtimeScriptedModel) Stream(ctx context.Context, input []*schema.Message, opts ...modelcomp.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *runtimeScriptedModel) BindTools(tools []*schema.ToolInfo) error {
	_ = tools
	return nil
}

func (m *runtimeScriptedModel) WithTools(tools []*schema.ToolInfo) (modelcomp.ToolCallingChatModel, error) {
	_ = tools
	return m, nil
}

var _ modelcomp.ToolCallingChatModel = (*runtimeScriptedModel)(nil)

func newTestCompactRuntime(t *testing.T, cm *runtimeCompactContextManager, model modelcomp.ToolCallingChatModel) (*Runtime, *agentworker.ThreadOutput) {
	t.Helper()
	eventBus := make(chan agentthread.Event, 16)
	cfg := &agentthread.TurnConfig{
		Agent: deepagents.Config{
			CheckpointStore: checkpointer.NewInMemoryStore(),
		},
	}
	if model != nil {
		cfg.Agent.Model = model
	}
	thread := newDeepAgentThreadForTest("1", cfg, cm, eventBus)
	runtime, err := NewRuntime(AdapterConfig{
		SessionID: "100",
		ThreadID:  "1",
		Thread:    thread,
		EventBus:  eventBus,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error=%v", err)
	}
	output, err := runtime.Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	return runtime, output
}

func waitUntilRuntimeInactive(t *testing.T, runtime *Runtime, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if runtime.ActiveTurn() == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runtime ActiveTurn()=%+v, want nil", runtime.ActiveTurn())
}

func readOutputItem(t *testing.T, output *agentworker.ThreadOutput) agentworker.ThreadOutputItem {
	t.Helper()
	select {
	case item := <-output.Items:
		return item
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for output item")
		return agentworker.ThreadOutputItem{}
	}
}

func readOutputEventType(t *testing.T, output *agentworker.ThreadOutput, eventType agentworker.EventType) agentworker.ThreadOutputItem {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case item := <-output.Items:
			if item.Event != nil && item.Event.Type == eventType {
				return item
			}
		case <-deadline:
			t.Fatalf("timed out waiting for output event type %s", eventType)
			return agentworker.ThreadOutputItem{}
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal() error=%v", err)
	}
	return data
}
