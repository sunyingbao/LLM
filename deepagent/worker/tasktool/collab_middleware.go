package tasktool

import (
	"context"
	"strings"

	deepmiddleware "eino-cli/deepagent/core/middleware"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const collabMiddlewareName = "tasktool_collab"

// CollabMiddlewareConfig configures the DeepAgent middleware wrapper around
// TaskTool. Role semantics remain host-defined and are only described through
// RolesDescription when the host provides one.
type CollabMiddlewareConfig struct {
	TaskTool          *TaskTool
	BasePrompt        string
	RolesDescription  string
	ExtraInstructions string
}

// CollabMiddleware exposes TaskTool as DeepAgent middleware and injects the
// collaboration rules needed to use the task tools safely.
type CollabMiddleware struct {
	deepmiddleware.BaseMiddleware

	taskTool          *TaskTool
	basePrompt        string
	rolesDescription  string
	extraInstructions string
}

func NewCollabMiddleware(cfg CollabMiddlewareConfig) *CollabMiddleware {
	return &CollabMiddleware{
		taskTool:          cfg.TaskTool,
		basePrompt:        strings.TrimSpace(cfg.BasePrompt),
		rolesDescription:  strings.TrimSpace(cfg.RolesDescription),
		extraInstructions: strings.TrimSpace(cfg.ExtraInstructions),
	}
}

func (m *CollabMiddleware) Name() string {
	return collabMiddlewareName
}

func (m *CollabMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	if m == nil || m.taskTool == nil {
		return nil, nil
	}
	return m.taskTool.Tools(), nil
}

func (m *CollabMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	if m == nil {
		return nil, nil
	}
	parts := []string{m.basePromptText()}
	if m.rolesDescription == "" {
		parts = append(parts, "No host-defined role guide is available for spawn_task. Omit the role field unless the user or host instructions explicitly provide a role.")
	} else {
		parts = append(parts,
			"Host-defined role guide for spawn_task:",
			m.rolesDescription,
			"Choose roles only according to the host-provided role guide. Do not invent role names or role semantics.",
		)
	}
	if m.extraInstructions != "" {
		parts = append(parts, m.extraInstructions)
	}
	return []*schema.Message{schema.SystemMessage(strings.Join(parts, "\n\n"))}, nil
}

func (m *CollabMiddleware) basePromptText() string {
	if m != nil && m.basePrompt != "" {
		return m.basePrompt
	}
	return strings.TrimSpace(collabBasePrompt)
}

const collabBasePrompt = `
You have access to collaboration tools for task threads.

A task thread is an independent worker-owned conversation, not a synchronous function call. Use these tools to coordinate independent work, not to chat with yourself or repeat instructions.

### Before spawning
- First analyze the overall task and identify which parts are immediate blockers and which parts can run independently.
- Keep work local when the next step depends directly on your own judgment or when delegation would only add latency.
- Use spawn_task when a bounded task thread can materially advance the user goal in parallel or with specialized context.
- Do not spawn merely because the task is large, vague, or requires careful reasoning.

### Designing spawned tasks
- The spawned task must be concrete, self-contained, and independently actionable.
- Provide a short title when it helps identify the task.
- The initial message must include the objective, relevant context, scope boundaries, expected output, and any ownership constraints.
- Avoid overlapping ownership between task threads.
- Do not duplicate work between the current thread and spawned threads.
- Do not issue another spawn_task for the same unresolved task unless the new task is genuinely different.

### After spawning
- Continue useful non-overlapping work locally while spawned task threads run.
- Use wait_message only when you need the task result or need to collect status before deciding next steps.
- Prefer waiting for multiple independent targets in one wait_message call when appropriate.
- Do not repeatedly wait with short timeouts.
- A timed_out result means no terminal state was observed before the timeout. It is not failure and is not a reason to resend the same task.
- Use close_task once a task thread is completed, failed, cancelled, or no longer needed for the user goal.
- Do not keep task threads open after their result has been integrated or their work is no longer relevant.

### Using send_message
- Use send_message only to provide new information, clarify requirements, correct direction, or explicitly change the task.
- Do not use send_message to urge progress or repeat the same assignment.
- Do not send a message to the current thread.

### Handling states
- completed means the task produced a terminal result that can be summarized or integrated.
- failed means the task failed; report or recover from the failure rather than silently spawning a duplicate.
- closed means the task thread has been shut down and should not receive more messages.
- approval_required, followup_required, and interrupted are blocked states. Do not spawn the same task again.
- Send new information to a blocked task only when it can actually unblock the task.
- waiting means the task has not reached a terminal state in the observed events.

### Reporting
- Summarize task-thread outcomes in terms of user-visible progress and decisions.
- Do not expose raw thread IDs, message IDs, event polling details, or backend coordinator metadata unless needed for debugging.
`
