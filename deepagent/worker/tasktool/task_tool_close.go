package tasktool

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const defaultCloseTaskResult = "thread closed"

// CloseTaskObserver formats the tool result after close_task has closed the
// thread and listed recent events.
type CloseTaskObserver func(events []*Event) string

// TaskToolCloseTaskInput is the model-provided input for close_task.
type TaskToolCloseTaskInput struct {
	Target string `json:"target" jsonschema:"description=Target thread reference to close."`
	Reason string `json:"reason,omitempty" jsonschema:"description=Optional reason recorded by the host."`
}

func (t *TaskTool) newCloseTaskTool() tool.BaseTool {
	tool, _ := utils.InferTool(
		ToolCloseTask,
		t.closeTaskDescription(),
		func(ctx context.Context, input *TaskToolCloseTaskInput) (string, error) {
			if input == nil {
				return taskToolErrorResult("input is required"), nil
			}
			if strings.TrimSpace(input.Target) == "" {
				return taskToolErrorResult("target is required"), nil
			}
			if t.InputValidator.CloseTask != nil {
				if err := t.InputValidator.CloseTask(ctx, input); err != nil {
					return taskToolErrorResult(err.Error()), nil
				}
			}
			result, err := t.closeTask(ctx, input)
			if err != nil {
				return taskToolErrorResult(err.Error()), nil
			}
			return taskToolDataResult(result), nil
		},
	)
	return tool
}

func (t *TaskTool) closeTaskDescription() string {
	if t != nil {
		if desc := strings.TrimSpace(t.Descriptions.CloseTask); desc != "" {
			return desc
		}
	}
	return defaultCloseTaskDescription
}

func (t *TaskTool) closeTask(ctx context.Context, input *TaskToolCloseTaskInput) (string, error) {
	target := strings.TrimSpace(input.Target)
	targetThreadID, err := t.resolveTarget(ctx, target)
	if err != nil {
		return "", err
	}
	if targetThreadID == "" {
		return "", fmt.Errorf("target is invalid")
	}
	if t.ThreadID != "" && targetThreadID == t.ThreadID {
		return "", fmt.Errorf("close_task on current thread is not allowed")
	}
	if _, err := t.Host.CloseThread(ctx, CloseThreadRequest{
		ThreadID: targetThreadID,
		Reason:   strings.TrimSpace(input.Reason),
	}); err != nil {
		return "", err
	}
	if t.CloseTaskObserver != nil {
		events, err := t.listRecentThreadEvents(ctx, targetThreadID, t.waitEventLimit())
		if err != nil {
			return "", err
		}
		if result := strings.TrimSpace(t.CloseTaskObserver(events)); result != "" {
			return result, nil
		}
	}
	return defaultCloseTaskResult, nil
}
