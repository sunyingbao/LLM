package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	sdkruntime "eino-cli/deepagent/runtime"
)

// LocalRuntime manages the interactive session over its selected runtime client.
type LocalRuntime struct {
	router      *sdkruntime.Router
	sessionID   string
	modelName   string
	runtimeKind sdkruntime.RuntimeKind
	mu          sync.Mutex
	threadRef   sdkruntime.GlobalThreadRef
	planMode    bool
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

func (runtime *LocalRuntime) OpenThread(ctx context.Context, threadID string) (err error) {
	ref := sdkruntime.GlobalThreadRef{
		Runtime:   runtime.RuntimeKind(),
		Namespace: runtime.sessionID,
		ThreadID:  strings.TrimSpace(threadID),
	}
	thread, err := runtime.router.GetThread(ctx, ref)
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	runtime.threadRef = thread.Ref
	runtime.mu.Unlock()
	return nil
}

func (runtime *LocalRuntime) OpenLatestThread(ctx context.Context) (opened bool, err error) {
	result, err := runtime.router.ListThreads(ctx, sdkruntime.ListThreadsQuery{
		Runtime:   runtime.RuntimeKind(),
		Namespace: runtime.sessionID,
		Limit:     1,
	})
	if err != nil {
		return false, err
	}
	if result == nil || len(result.Threads) == 0 {
		return false, nil
	}
	runtime.mu.Lock()
	runtime.threadRef = result.Threads[0].Ref
	runtime.mu.Unlock()
	return true, nil
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
		Workspace: sdkruntime.WorkspaceSpec{Cwd: cwd}, Title: "DeepAgent",
	})
	if err != nil {
		return ref, err
	}
	runtime.threadRef = created.Thread.Ref
	return runtime.threadRef, nil
}

var _ InteractiveRuntime = (*LocalRuntime)(nil)
