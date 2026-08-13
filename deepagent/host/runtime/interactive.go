package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	sdkruntime "eino-cli/deepagent/runtime"
)

type ActionResult struct {
	Success bool
	Message string
	Output  string
}

type InteractiveRuntime interface {
	StartTurn(ctx context.Context, prompt string) (stream *TurnStream, err error)
	Resume(ctx context.Context, ref sdkruntime.GlobalThreadRef, payload protoinput.ResumeTurnPayload) (err error)
	ClearThread()
	ConsolidateMemory(ctx context.Context) (result ActionResult, err error)
	ExportThreadRef() (payload []byte, err error)
	ImportThreadRef(payload []byte) (err error)
	SetPlanMode(ctx context.Context, enabled bool) (result bool, err error)
	Name() (name string)
	RuntimeKind() (kind sdkruntime.RuntimeKind)
}

type TurnStream struct {
	Ref    sdkruntime.GlobalThreadRef
	TurnID string
	Events <-chan timeline.Event

	stop         func(context.Context) error
	subscription sdkruntime.TimelineSubscription
	once         sync.Once
	turnMu       sync.Mutex
}

// AcceptEvent locks a remotely submitted stream to the first observed turn.
// Local runtimes already return TurnID from Submit, while Agent Coordinator
// assigns it asynchronously and exposes it first through TURN_STARTED.
func (stream *TurnStream) AcceptEvent(event timeline.Event) (accepted bool) {
	if stream == nil {
		return false
	}
	stream.turnMu.Lock()
	defer stream.turnMu.Unlock()
	if stream.TurnID == "" {
		if protoevent.EventType(event.EventType) != protoevent.EventTypeTurnStarted || strings.TrimSpace(event.TurnID) == "" {
			return false
		}
		stream.TurnID = event.TurnID
	}
	return event.TurnID == stream.TurnID
}

func (stream *TurnStream) Stop(ctx context.Context) (err error) {
	if stream == nil || stream.stop == nil {
		return nil
	}
	err = stream.stop(ctx)
	return err
}

func (stream *TurnStream) Close() (err error) {
	if stream == nil || stream.subscription == nil {
		return nil
	}
	stream.once.Do(func() { err = stream.subscription.Close() })
	return err
}

func (stream *TurnStream) Err() (err error) {
	if stream == nil || stream.subscription == nil {
		return nil
	}
	err = stream.subscription.Err()
	return err
}

func (runtime *LocalRuntime) StartTurn(ctx context.Context, prompt string) (stream *TurnStream, err error) {
	ref, err := runtime.ensureThread(ctx)
	if err != nil {
		return nil, err
	}
	history, err := runtime.router.ListTimeline(ctx, sdkruntime.TimelineQuery{Ref: ref})
	if err != nil {
		return nil, err
	}
	afterEventID := ""
	if len(history.Events) > 0 {
		afterEventID = history.Events[len(history.Events)-1].EventID
	}
	subscription, err := runtime.router.SubscribeTimeline(ctx, sdkruntime.TimelineQuery{Ref: ref, AfterEventID: afterEventID})
	if err != nil {
		return nil, err
	}
	runtime.mu.Lock()
	planMode := runtime.planMode
	runtime.mu.Unlock()
	input := protoinput.UserMessage{Parts: []protoinput.MessagePart{{Type: protoinput.MessagePartTypeText, Text: prompt}}}
	if planMode {
		input.Mode = protoinput.UserMessageModeImplPlan
	}
	submit, err := runtime.router.Submit(ctx, sdkruntime.SubmitRequest{Ref: ref, Input: input})
	if err != nil {
		_ = subscription.Close()
		return nil, err
	}
	stream = &TurnStream{
		Ref: ref, TurnID: submit.TurnID, Events: subscription.Events(), subscription: subscription,
		stop: func(stopCtx context.Context) (stopErr error) {
			_, stopErr = runtime.router.Stop(stopCtx, sdkruntime.StopRequest{Ref: ref, TurnID: submit.TurnID})
			return stopErr
		},
	}
	return stream, nil
}

func (runtime *LocalRuntime) Resume(ctx context.Context, ref sdkruntime.GlobalThreadRef, payload protoinput.ResumeTurnPayload) (err error) {
	_, err = runtime.router.Resume(ctx, sdkruntime.ResumeRequest{Ref: ref, Payload: payload})
	return err
}

func (runtime *LocalRuntime) ClearThread() {
	runtime.mu.Lock()
	runtime.threadRef = sdkruntime.GlobalThreadRef{}
	runtime.mu.Unlock()
}

func (runtime *LocalRuntime) ConsolidateMemory(ctx context.Context) (result ActionResult, err error) {
	if runtime.RuntimeKind() == sdkruntime.RuntimeRemote {
		return result, &sdkruntime.Error{Code: sdkruntime.ErrorCodeCapabilityUnavailable, Op: "consolidate_memory", Runtime: sdkruntime.RuntimeRemote, Message: "remote memory consolidation is not configured"}
	}
	result, err = runtime.runDream(ctx)
	return result, err
}

func (runtime *LocalRuntime) ExportThreadRef() (payload []byte, err error) {
	runtime.mu.Lock()
	ref := runtime.threadRef
	runtime.mu.Unlock()
	payload, err = json.Marshal(ref)
	return payload, err
}

func (runtime *LocalRuntime) ImportThreadRef(payload []byte) (err error) {
	var ref sdkruntime.GlobalThreadRef
	if len(payload) > 0 {
		if err = json.Unmarshal(payload, &ref); err != nil {
			return fmt.Errorf("decode runtime thread reference: %w", err)
		}
		if err = ref.Validate(); err != nil {
			return err
		}
		if ref.Runtime != runtime.RuntimeKind() {
			return fmt.Errorf("cannot import %s thread into %s runtime", ref.Runtime, runtime.RuntimeKind())
		}
	}
	runtime.mu.Lock()
	runtime.threadRef = ref
	runtime.mu.Unlock()
	return err
}

func (runtime *LocalRuntime) RuntimeKind() (kind sdkruntime.RuntimeKind) {
	if runtime == nil || runtime.runtimeKind == "" {
		return sdkruntime.RuntimeLocal
	}
	return runtime.runtimeKind
}

func (runtime *LocalRuntime) SetPlanMode(ctx context.Context, enabled bool) (result bool, err error) {
	runtime.mu.Lock()
	runtime.planMode = enabled
	runtime.mu.Unlock()
	return enabled, nil
}

func (runtime *LocalRuntime) Name() (name string) {
	name = strings.TrimSpace(runtime.modelName)
	if name == "" {
		name = "unified-local"
	}
	return name
}

func (runtime *LocalRuntime) ensureThread(ctx context.Context) (ref sdkruntime.GlobalThreadRef, err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.threadRef.ThreadID != "" {
		return runtime.threadRef, nil
	}
	cwd, _ := os.Getwd()
	created, err := runtime.router.CreateThread(ctx, sdkruntime.CreateThreadRequest{
		Runtime: runtime.RuntimeKind(), Namespace: runtime.sessionID,
		Definition: runtime.definition, Workspace: sdkruntime.WorkspaceSpec{Cwd: cwd}, Title: "SGADK",
	})
	if err != nil {
		return ref, err
	}
	runtime.threadRef = created.Thread.Ref
	return runtime.threadRef, nil
}

var _ InteractiveRuntime = (*LocalRuntime)(nil)
