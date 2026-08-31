package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"

	"eino-cli/backend/config"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/definition"
	runtimecontext "eino-cli/deepagent/host/executioncontext"
	"eino-cli/deepagent/memory/autodream"
	sdkruntime "eino-cli/deepagent/runtime"
)

// LocalRuntime is the standard local implementation of InteractiveRuntime.
// Its methods own thread lifecycle, streaming submission, memory actions, and
// runtime state import/export for one local session.
type LocalRuntime struct {
	cfg         *config.Config
	router      *sdkruntime.Router
	sessionID   string
	definition  agentdefinition.Definition
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

func (runtime *LocalRuntime) runDream(ctx context.Context) (result ActionResult, err error) {
	if runtime == nil || runtime.cfg == nil {
		return result, fmt.Errorf("runtime configuration is required")
	}
	memoryRoot := config.DreamMemoryDir()
	lastConsolidatedAt, err := autodream.ReadLastConsolidatedAt(memoryRoot)
	if err != nil {
		return result, fmt.Errorf("read dream lock: %w", err)
	}
	candidates, err := autodream.ListJSONLSessionCandidates(config.TranscriptDir())
	if err != nil {
		return result, fmt.Errorf("list dream sessions: %w", err)
	}
	sessionIDs := autodream.FilterSessionsTouchedSince(candidates, lastConsolidatedAt, "")
	if len(sessionIDs) == 0 {
		return ActionResult{Success: true, Output: "dream: no transcript sessions to consolidate"}, nil
	}
	lock, err := autodream.TryAcquireConsolidationLock(memoryRoot)
	if err != nil {
		return result, fmt.Errorf("acquire dream lock: %w", err)
	}
	if lock == nil {
		return ActionResult{Success: true, Output: "dream: another consolidation is already running"}, nil
	}
	prompt := autodream.BuildConsolidationPrompt(memoryRoot, config.TranscriptDir(), sessionIDs)
	forkResult, err := runtime.runDreamFork(ctx, prompt)
	if err != nil {
		autodream.RollbackConsolidationLock(lock)
		return result, err
	}
	if len(forkResult.FilesTouched) == 0 {
		return ActionResult{Success: true, Output: "dream: completed; no memory files changed"}, nil
	}
	result = ActionResult{Success: true, Output: fmt.Sprintf("dream: improved %d memory files: %s", len(forkResult.FilesTouched), strings.Join(forkResult.FilesTouched, ", "))}
	return result, nil
}

func (runtime *LocalRuntime) runDreamFork(ctx context.Context, prompt string) (result autodream.ForkedAgentResult, err error) {
	ctx = runtimecontext.WithQuerySource(ctx, runtimecontext.QuerySourceAutoDream)
	dreamAgent, err := buildAutoDreamAgent(ctx, runtime.cfg)
	if err != nil {
		return result, err
	}
	stream, err := dreamAgent.Stream(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return result, err
	}
	defer stream.Close()
	var outputs []string
	var filesTouched []string
	for {
		message, receiveErr := stream.Recv()
		if receiveErr == io.EOF {
			break
		}
		if receiveErr != nil {
			return result, receiveErr
		}
		filesTouched = append(filesTouched, dreamTouchedFiles(message)...)
		if message.Role == schema.Assistant && len(message.ToolCalls) == 0 {
			if output := strings.TrimSpace(message.Content); output != "" {
				outputs = append(outputs, output)
			}
		}
	}
	result = autodream.ForkedAgentResult{FilesTouched: uniqueStrings(filesTouched), Output: strings.Join(outputs, "\n")}
	return result, nil
}

var _ InteractiveRuntime = (*LocalRuntime)(nil)
