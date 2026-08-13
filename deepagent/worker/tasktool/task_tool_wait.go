package tasktool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const (
	defaultTaskToolWaitTimeout  = 60 * time.Second
	maxTaskToolWaitTimeout      = 10 * time.Minute
	defaultTaskToolPollInterval = time.Second
	defaultTaskToolEventLimit   = 100
)

type WaitMessageState string

const (
	WaitMessageStateWaiting          WaitMessageState = "waiting"
	WaitMessageStateCompleted        WaitMessageState = "completed"
	WaitMessageStateApprovalRequired WaitMessageState = "approval_required"
	WaitMessageStateFollowupRequired WaitMessageState = "followup_required"
	WaitMessageStateInterrupted      WaitMessageState = "interrupted"
	WaitMessageStateFailed           WaitMessageState = "failed"
)

type MessageWaitObserver func(events []*Event, messageID string) MessageWaitResult

// MessageWaitResult is the observer result for one polled message.
type MessageWaitResult struct {
	// Result is the observer-defined completion payload.
	Result string
	// Done reports whether the waited message has reached a terminal state.
	Done bool
	// State is the observed message state. Empty means waiting unless Done or
	// SysError implies a stronger state.
	State WaitMessageState
	// SysError contains host- or wait-level failures.
	SysError string
}

// TaskToolWaitMessageInput is the model-provided input for wait_message.
type TaskToolWaitMessageInput struct {
	Target    string                      `json:"target,omitempty" jsonschema:"description=Target thread reference for a single wait target. Use targets for multiple waits."`
	MessageID string                      `json:"message_id,omitempty" jsonschema:"description=Message identifier for a single wait target. Use targets for multiple waits."`
	Targets   []TaskToolWaitMessageTarget `json:"targets,omitempty" jsonschema:"description=Multiple wait targets. Each item must contain target and message_id."`
	TimeoutMS int64                       `json:"timeout_ms,omitempty" jsonschema:"description=Wait timeout in milliseconds. Default is 60000 and maximum is 600000."`
}

// TaskToolWaitMessageTarget is one target/message pair in wait_message input.
type TaskToolWaitMessageTarget struct {
	Target    string `json:"target" jsonschema:"description=Target thread reference."`
	MessageID string `json:"message_id" jsonschema:"description=Message identifier to wait for."`
}

type taskToolWaitMessageResult struct {
	// Res is keyed by target/message_id.
	Res map[string]taskToolWaitMessageItem `json:"res"`
	// Warning contains optional host hints, such as low concurrency warnings.
	Warning string `json:"warning,omitempty"`
}

type taskToolWaitMessageItem struct {
	// Result is the observer-defined completion payload.
	Result string `json:"result,omitempty"`
	// Done reports whether the waited message has reached a terminal state.
	Done bool `json:"done"`
	// TimedOut reports whether this wait call returned before the message
	// reached a terminal state.
	TimedOut bool `json:"timed_out"`
	// State is the observed message state.
	State WaitMessageState `json:"state"`
	// SysError contains host- or wait-level failures.
	SysError string `json:"sys_error,omitempty"`
}

type taskToolResolvedWaitMessageTarget struct {
	Target    string
	ThreadID  string
	MessageID string
}

type taskToolWaitMessageState struct {
	target taskToolResolvedWaitMessageTarget
	done   bool
}

func (t *TaskTool) newWaitMessageTool() tool.BaseTool {
	tool, _ := utils.InferTool(
		ToolWaitMessage,
		t.waitMessageDescription(),
		func(ctx context.Context, input *TaskToolWaitMessageInput) (string, error) {
			if input == nil {
				return taskToolErrorResult("input is required"), nil
			}
			if t.MessageWaitObserver == nil {
				return taskToolErrorResult("message wait observer is required"), nil
			}
			if err := input.validate(); err != nil {
				return taskToolErrorResult(err.Error()), nil
			}
			if t.InputValidator.WaitMessage != nil {
				if err := t.InputValidator.WaitMessage(ctx, input); err != nil {
					return taskToolErrorResult(err.Error()), nil
				}
			}
			result, err := t.waitMessage(ctx, input)
			if err != nil {
				return taskToolErrorResult(err.Error()), nil
			}
			return taskToolDataResult(result), nil
		},
	)
	return tool
}

func (t *TaskTool) waitMessageDescription() string {
	if t != nil {
		if desc := strings.TrimSpace(t.Descriptions.WaitMessage); desc != "" {
			return desc
		}
	}
	return defaultWaitMessageDescription
}

func (t *TaskTool) waitMessage(ctx context.Context, input *TaskToolWaitMessageInput) (*taskToolWaitMessageResult, error) {
	waitCtx, cancel := context.WithTimeout(ctx, taskToolWaitTimeout(input.TimeoutMS))
	defer cancel()

	states, result, err := t.buildWaitMessageStates(ctx, input.normalizedTargets())
	if err != nil {
		return nil, err
	}

	ticker := time.NewTicker(defaultTaskToolPollInterval)
	defer ticker.Stop()

	for {
		done, err := t.observeWaitMessageStates(waitCtx, states, result)
		if err != nil {
			return nil, err
		}
		if done {
			return result, nil
		}
		select {
		case <-waitCtx.Done():
			markTaskToolMessageTimeouts(result, states)
			return result, nil
		case <-ticker.C:
		}
	}
}

func (t *TaskTool) buildWaitMessageStates(ctx context.Context, targets []TaskToolWaitMessageTarget) ([]taskToolWaitMessageState, *taskToolWaitMessageResult, error) {
	resolved, err := t.resolveWaitMessageTargets(ctx, targets)
	if err != nil {
		return nil, nil, err
	}
	result := &taskToolWaitMessageResult{Res: make(map[string]taskToolWaitMessageItem, len(resolved))}
	if t.WorkerConcurrency <= 1 {
		result.Warning = "Worker concurrency is 1; waiting for newly started child tasks may take longer or time out."
	}
	states := make([]taskToolWaitMessageState, 0, len(resolved))
	for _, target := range resolved {
		states = append(states, taskToolWaitMessageState{target: target})
		result.Res[taskToolWaitMessageKey(target)] = taskToolWaitMessageItem{State: WaitMessageStateWaiting}
	}
	return states, result, nil
}

func (t *TaskTool) observeWaitMessageStates(ctx context.Context, states []taskToolWaitMessageState, result *taskToolWaitMessageResult) (bool, error) {
	allDone := true
	for i := range states {
		if states[i].done {
			continue
		}
		allDone = false
		events, err := t.listRecentThreadEvents(ctx, states[i].target.ThreadID, t.waitEventLimit())
		if err != nil {
			if ctx.Err() != nil {
				markTaskToolMessageTimeouts(result, states)
				return true, nil
			}
			return false, err
		}
		observed := t.MessageWaitObserver(events, states[i].target.MessageID)
		result.Res[taskToolWaitMessageKey(states[i].target)] = taskToolWaitMessageItemFromObserved(observed)
		if observed.Done {
			states[i].done = true
		}
	}
	return allDone || allTaskToolMessageStatesDone(states), nil
}

func (input *TaskToolWaitMessageInput) validate() error {
	if len(input.Targets) == 0 {
		if strings.TrimSpace(input.Target) == "" {
			return fmt.Errorf("target is required")
		}
		if strings.TrimSpace(input.MessageID) == "" {
			return fmt.Errorf("message_id is required")
		}
		return nil
	}
	for i, target := range input.Targets {
		if strings.TrimSpace(target.Target) == "" {
			return fmt.Errorf("targets[%d].target is required", i)
		}
		if strings.TrimSpace(target.MessageID) == "" {
			return fmt.Errorf("targets[%d].message_id is required", i)
		}
	}
	return nil
}

func (input *TaskToolWaitMessageInput) normalizedTargets() []TaskToolWaitMessageTarget {
	if len(input.Targets) > 0 {
		return input.Targets
	}
	return []TaskToolWaitMessageTarget{{Target: input.Target, MessageID: input.MessageID}}
}

func (t *TaskTool) resolveWaitMessageTargets(ctx context.Context, targets []TaskToolWaitMessageTarget) ([]taskToolResolvedWaitMessageTarget, error) {
	resolved := make([]taskToolResolvedWaitMessageTarget, 0, len(targets))
	for _, target := range targets {
		threadID, err := t.resolveTarget(ctx, target.Target)
		if err != nil {
			return nil, err
		}
		if threadID == "" {
			return nil, fmt.Errorf("target %q is invalid", target.Target)
		}
		resolved = append(resolved, taskToolResolvedWaitMessageTarget{
			Target:    strings.TrimSpace(target.Target),
			ThreadID:  threadID,
			MessageID: strings.TrimSpace(target.MessageID),
		})
	}
	return resolved, nil
}

func (t *TaskTool) waitEventLimit() int {
	if t.WaitEventLimit > 0 {
		return t.WaitEventLimit
	}
	return defaultTaskToolEventLimit
}

func allTaskToolMessageStatesDone(states []taskToolWaitMessageState) bool {
	for _, state := range states {
		if !state.done {
			return false
		}
	}
	return true
}

func markTaskToolMessageTimeouts(result *taskToolWaitMessageResult, states []taskToolWaitMessageState) {
	for _, state := range states {
		if state.done {
			continue
		}
		item := result.Res[taskToolWaitMessageKey(state.target)]
		item.TimedOut = true
		if item.State == "" {
			item.State = WaitMessageStateWaiting
		}
		result.Res[taskToolWaitMessageKey(state.target)] = item
	}
}

func taskToolWaitMessageItemFromObserved(observed MessageWaitResult) taskToolWaitMessageItem {
	state := observed.State
	if state == "" {
		switch {
		case observed.SysError != "":
			state = WaitMessageStateFailed
		case observed.Done:
			state = WaitMessageStateCompleted
		default:
			state = WaitMessageStateWaiting
		}
	}
	return taskToolWaitMessageItem{
		Result:   observed.Result,
		Done:     observed.Done,
		State:    state,
		SysError: observed.SysError,
	}
}

func taskToolWaitMessageKey(target taskToolResolvedWaitMessageTarget) string {
	return target.Target + "/" + target.MessageID
}

func taskToolWaitTimeout(timeoutMS int64) time.Duration {
	if timeoutMS <= 0 {
		return defaultTaskToolWaitTimeout
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout > maxTaskToolWaitTimeout {
		return maxTaskToolWaitTimeout
	}
	return timeout
}
