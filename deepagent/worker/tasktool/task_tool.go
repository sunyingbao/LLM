package tasktool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"code.byted.org/lang/gg/gmap"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/uuid"
)

const (
	// ToolSendMessage is the generic tool name for cross-thread message delivery.
	ToolSendMessage = "send_message"
	// ToolSpawnTask is the generic tool name for creating a child task thread.
	ToolSpawnTask = "spawn_task"
	// ToolWaitMessage is the generic tool name for observing message state.
	ToolWaitMessage = "wait_message"
	// ToolCloseTask is the generic tool name for closing a task thread.
	ToolCloseTask = "close_task"
)

const (
	defaultSendMessageDescription = "Send a message to an existing task thread. Provide a target thread reference, a complete message body, and optional metadata. The tool returns a message_id."
	defaultSpawnTaskDescription   = "Create a new task thread and send its initial message. Provide the initial message content, plus optional title and metadata. The tool returns a target thread reference plus the initial_message_id for follow-up collaboration."
	defaultWaitMessageDescription = "Wait for one or more submitted messages on target task threads to reach an observed state. Provide target and message_id for one wait target, or targets for multiple wait targets. The tool returns a res map keyed by target/message_id with done, timed_out, state, and any observed result."
	defaultCloseTaskDescription   = "Close a target task thread when it is no longer needed. Provide a target thread reference and optional reason. The tool returns a host-formatted close result."
)

// TaskToolDescriptions customizes the model-facing top-level descriptions of
// TaskTool's tools. Empty fields use the SDK defaults.
type TaskToolDescriptions struct {
	SendMessage string
	SpawnTask   string
	WaitMessage string
	CloseTask   string
}

// TaskToolInputValidator validates model-provided task tool inputs before
// TaskTool performs host side effects. If a validator returns a non-nil error,
// err.Error() is returned to the model as the task tool error result and the
// underlying host operation is not executed.
type TaskToolInputValidator struct {
	SendMessage func(ctx context.Context, input *TaskToolSendMessageInput) error
	SpawnTask   func(ctx context.Context, input *TaskToolSpawnTaskInput) error
	WaitMessage func(ctx context.Context, input *TaskToolWaitMessageInput) error
	CloseTask   func(ctx context.Context, input *TaskToolCloseTaskInput) error
}

// SpawnedThread describes one newly created thread after spawn_task succeeds.
type SpawnedThread struct {
	// ThreadID is the backend thread identifier.
	ThreadID string
	// InitialMessageID is the identifier of the first message submitted to the
	// spawned thread.
	InitialMessageID string
	// UserID is the durable owner of the spawned thread.
	UserID int64
	// SessionID is the backend session identifier for the spawned thread.
	SessionID string
	// Title is the current thread title.
	Title string
	// Profile carries host-defined execution profile for the spawned thread.
	Profile ThreadProfile
	// Metadata contains backend- or business-specific thread metadata.
	Metadata map[string]string
}

// OutboundMessage is the logical outbound message before backend-specific
// payload formatting is applied.
type OutboundMessage struct {
	// FromThreadID is the current thread identifier.
	FromThreadID string
	// Target is the user-facing target token before resolution.
	Target string
	// Content is the logical message body before backend formatting.
	Content string
}

// FormattedOutboundMessage is the backend-ready outbound payload returned by
// FormatOutbound.
type FormattedOutboundMessage struct {
	// MessageType is the backend-ready message type. Empty means the host default.
	MessageType string
	// Payload is the backend-ready message body.
	Payload []byte
	// Metadata contains backend-ready message metadata.
	Metadata map[string]string
}

// TaskTool provides generic collaboration tools over any thread-based task
// host implementing TaskHost.
type TaskTool struct {
	// Host executes the generic task-thread operations used by this toolset.
	Host TaskHost

	// ThreadID is the current thread identifier from whose context the tool is
	// being invoked.
	ThreadID string
	// SessionID is the current session identifier reused by spawn_task.
	SessionID string
	// UserID is the current user that owns task threads created by spawn_task.
	// Hosts may also derive the owner from ThreadID when this is left unset.
	UserID int64

	// WorkerConcurrency is an optional hint used only for result warnings.
	WorkerConcurrency int
	// WaitEventLimit bounds how many recent events wait_message inspects per poll.
	WaitEventLimit int

	// Metadata is static default metadata merged into spawn_task thread metadata
	// and send_message message metadata. Per-call metadata overrides it. It is
	// not merged into spawn_task initial-message metadata.
	Metadata map[string]string
	// SpawnProfile is copied into every spawned thread creation request. The
	// spawn_task role input, when provided, overrides SpawnProfile.Role.
	SpawnProfile ThreadProfile
	// SpawnInitialMessageMetadata is static default metadata merged only into the
	// initial message sent by spawn_task. Metadata returned by FormatOutbound
	// overrides it for that initial message.
	SpawnInitialMessageMetadata map[string]string
	// SpawnMetadataDescription overrides the metadata field description in the
	// spawn_task schema when callers need backend-specific guidance.
	SpawnMetadataDescription string
	// Descriptions overrides the model-facing top-level tool descriptions.
	// Empty fields use the SDK defaults.
	Descriptions TaskToolDescriptions

	// InputValidator validates task tool inputs before host side effects. A
	// non-nil validation error is returned to the model as a task tool error
	// result, and the underlying host operation is skipped.
	InputValidator TaskToolInputValidator

	// ResolveTarget maps a user-facing target token to a backend thread ID.
	ResolveTarget func(ctx context.Context, target string) (string, error)
	// OnThreadSpawned can post-process a new thread and optionally return the
	// user-facing target string that spawn_task should expose.
	OnThreadSpawned func(ctx context.Context, spawned SpawnedThread) (string, error)
	// FormatOutbound customizes backend payload and metadata generation before
	// send_message dispatches a message or spawn_task seeds its initial message.
	FormatOutbound func(ctx context.Context, msg OutboundMessage) (*FormattedOutboundMessage, error)
	// TaskResultModifier can mutate the final spawn_task and send_message tool
	// results before they are returned to the model.
	TaskResultModifier TaskResultModifier
	// MessageWaitObserver decides whether a waited message is done based on
	// recently listed events.
	MessageWaitObserver MessageWaitObserver
	// CloseTaskObserver formats close_task's result after the target thread is
	// closed and recent events have been listed.
	CloseTaskObserver CloseTaskObserver
}

func (t *TaskTool) Tools() []tool.BaseTool {
	if t == nil || t.Host == nil {
		return nil
	}
	return []tool.BaseTool{
		t.newSendMessageTool(),
		t.newSpawnTaskTool(),
		t.newWaitMessageTool(),
		t.newCloseTaskTool(),
	}
}

// TaskToolSendMessageInput is the model-provided input for send_message.
type TaskToolSendMessageInput struct {
	Target   string            `json:"target" jsonschema:"description=Target thread reference. Use a target returned by spawn_task or any host-defined thread reference accepted by this tool."`
	Content  string            `json:"content" jsonschema:"description=Complete message body to send to the target thread."`
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"description=Optional message metadata."`
}

// TaskResultModifier customizes the final task-tool result returned to the model.
// The message/thread arguments expose the backend-visible objects available at
// that point. Implementations should keep backend identifiers stable unless
// they intentionally change follow-up collaboration semantics.
type TaskResultModifier interface {
	ModifySendMessageResult(ctx context.Context, result *TaskToolSendMessageResult, message *Message) error
	ModifySpawnTaskResult(ctx context.Context, result *TaskToolSpawnTaskResult, thread *Thread, initialMessage *Message) error
}

// TaskToolSendMessageResult is the model-visible data returned by send_message.
type TaskToolSendMessageResult struct {
	// MessageID is the identifier of the submitted outbound message.
	MessageID string `json:"message_id"`
	// Extra carries host-defined JSON-serializable result data.
	Extra any `json:"extra,omitempty"`
}

// TaskToolSpawnTaskInput is the model-provided input for spawn_task.
type TaskToolSpawnTaskInput struct {
	Title    string            `json:"title,omitempty" jsonschema:"description=Short human-readable label for the new task thread."`
	Role     string            `json:"role,omitempty" jsonschema:"description=Optional host-defined role for the new task thread. Use it only when collaboration instructions define available roles."`
	Content  string            `json:"content" jsonschema:"description=Initial message body for the new task thread."`
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"description=Optional thread metadata."`
}

// TaskToolSpawnTaskResult is the model-visible data returned by spawn_task.
type TaskToolSpawnTaskResult struct {
	ThreadID string `json:"-"`
	// InitialMessageID is the identifier of the first message sent to the new
	// thread.
	InitialMessageID string `json:"initial_message_id"`
	// Target is the user-facing thread reference that can be reused by later tool calls.
	Target string `json:"target,omitempty"`
	// Warning contains optional host hints, such as low concurrency warnings.
	Warning string `json:"warning,omitempty"`
	// Extra carries host-defined JSON-serializable result data.
	Extra any `json:"extra,omitempty"`
}

func (t *TaskTool) newSendMessageTool() tool.BaseTool {
	tool, _ := utils.InferTool(
		ToolSendMessage,
		t.sendMessageDescription(),
		func(ctx context.Context, input *TaskToolSendMessageInput) (string, error) {
			if input == nil {
				return taskToolErrorResult("input is required"), nil
			}
			if strings.TrimSpace(input.Target) == "" {
				return taskToolErrorResult("target is required"), nil
			}
			if input.Content == "" {
				return taskToolErrorResult("content is required"), nil
			}
			if t.InputValidator.SendMessage != nil {
				if err := t.InputValidator.SendMessage(ctx, input); err != nil {
					return taskToolErrorResult(err.Error()), nil
				}
			}
			result, err := t.sendMessage(ctx, input)
			if err != nil {
				return taskToolErrorResult(err.Error()), nil
			}
			return taskToolDataResult(result), nil
		},
	)
	return tool
}

func (t *TaskTool) sendMessageDescription() string {
	if t != nil {
		if desc := strings.TrimSpace(t.Descriptions.SendMessage); desc != "" {
			return desc
		}
	}
	return defaultSendMessageDescription
}

func (t *TaskTool) newSpawnTaskTool() tool.BaseTool {
	tool, _ := utils.InferTool(
		ToolSpawnTask,
		t.spawnTaskDescription(),
		func(ctx context.Context, input *TaskToolSpawnTaskInput) (string, error) {
			if input == nil {
				return taskToolErrorResult("input is required"), nil
			}
			if input.Content == "" {
				return taskToolErrorResult("content is required"), nil
			}
			if t.InputValidator.SpawnTask != nil {
				if err := t.InputValidator.SpawnTask(ctx, input); err != nil {
					return taskToolErrorResult(err.Error()), nil
				}
			}
			result, err := t.spawnTask(ctx, input)
			if err != nil {
				return taskToolErrorResult(err.Error()), nil
			}
			return taskToolDataResult(result), nil
		},
	)
	return tool
}

func (t *TaskTool) spawnTaskDescription() string {
	baseDesc := defaultSpawnTaskDescription
	metadataDesc := ""
	if t != nil {
		if desc := strings.TrimSpace(t.Descriptions.SpawnTask); desc != "" {
			baseDesc = desc
		}
		metadataDesc = strings.TrimSpace(t.SpawnMetadataDescription)
	}
	if metadataDesc == "" {
		metadataDesc = "Metadata is optional."
	}
	return baseDesc + " Metadata: " + metadataDesc
}

func (t *TaskTool) sendMessage(ctx context.Context, input *TaskToolSendMessageInput) (*TaskToolSendMessageResult, error) {
	target := strings.TrimSpace(input.Target)
	targetThreadID, err := t.resolveTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	if targetThreadID == "" {
		return nil, fmt.Errorf("target is invalid")
	}
	if t.ThreadID != "" && targetThreadID == t.ThreadID {
		return nil, fmt.Errorf("send_message to current thread is not allowed")
	}
	formatted, err := t.formatOutbound(ctx, OutboundMessage{
		FromThreadID: t.ThreadID,
		Target:       target,
		Content:      input.Content,
	})
	if err != nil {
		return nil, err
	}
	messageID := newTaskToolMessageID()
	msg := &Message{
		ID:          messageID,
		MessageType: formatted.MessageType,
		Payload:     formatted.Payload,
		Metadata:    mergeTaskToolMetadata(t.Metadata, input.Metadata),
	}
	msg.Metadata = mergeTaskToolMetadata(msg.Metadata, formatted.Metadata)
	sent, err := t.Host.SendMessage(ctx, SendMessageRequest{
		ThreadID:     targetThreadID,
		FromThreadID: t.ThreadID,
		Message:      msg,
	})
	if err != nil {
		return nil, err
	}
	if sent != nil && sent.ID != "" {
		messageID = sent.ID
	}
	result := &TaskToolSendMessageResult{MessageID: messageID}
	if t.TaskResultModifier != nil {
		message := sent
		if message == nil {
			message = msg
			message.ID = messageID
		}
		if err := t.TaskResultModifier.ModifySendMessageResult(ctx, result, message); err != nil {
			return nil, fmt.Errorf("ModifySendMessageResult: %w", err)
		}
	}
	return result, nil
}

func (t *TaskTool) spawnTask(ctx context.Context, input *TaskToolSpawnTaskInput) (*TaskToolSpawnTaskResult, error) {
	initialMessageID := newTaskToolMessageID()
	profile := t.SpawnProfile
	profile.Role = strings.TrimSpace(input.Role)
	formatted, err := t.formatOutbound(ctx, OutboundMessage{
		FromThreadID: t.ThreadID,
		Content:      input.Content,
	})
	if err != nil {
		return nil, err
	}
	initialMessage := &Message{
		ID:          initialMessageID,
		MessageType: formatted.MessageType,
		Payload:     formatted.Payload,
		Metadata:    mergeTaskToolMetadata(t.SpawnInitialMessageMetadata, formatted.Metadata),
	}
	thread, err := t.Host.CreateThread(ctx, CreateThreadRequest{
		UserID:         t.UserID,
		SessionID:      t.SessionID,
		ParentThreadID: t.ThreadID,
		Title:          input.Title,
		Profile:        profile,
		Metadata:       mergeTaskToolMetadata(t.Metadata, input.Metadata),
		InitialMessage: initialMessage,
	})
	if err != nil {
		return nil, err
	}
	if thread == nil || thread.ID == "" {
		return nil, fmt.Errorf("CreateThread returned empty thread")
	}
	if thread.InitialMessageID != "" {
		initialMessageID = thread.InitialMessageID
	}
	initialMessage.ID = initialMessageID
	target := thread.ID
	if t.OnThreadSpawned != nil {
		finalTarget, err := t.OnThreadSpawned(ctx, SpawnedThread{
			ThreadID:         thread.ID,
			InitialMessageID: initialMessageID,
			UserID:           thread.UserID,
			SessionID:        thread.SessionID,
			Title:            thread.Title,
			Profile:          thread.Profile,
			Metadata:         gmap.Clone(thread.Metadata),
		})
		if err != nil {
			return nil, fmt.Errorf("OnThreadSpawned: %w", err)
		}
		if strings.TrimSpace(finalTarget) != "" {
			target = strings.TrimSpace(finalTarget)
		}
	}
	result := &TaskToolSpawnTaskResult{
		ThreadID:         thread.ID,
		InitialMessageID: initialMessageID,
		Target:           target,
	}
	if t.WorkerConcurrency <= 1 {
		result.Warning = "Worker concurrency is 1; waiting for a newly started child task may take longer or time out."
	}
	if t.TaskResultModifier != nil {
		if err := t.TaskResultModifier.ModifySpawnTaskResult(ctx, result, thread, initialMessage); err != nil {
			return nil, fmt.Errorf("ModifySpawnTaskResult: %w", err)
		}
	}
	return result, nil
}

func (t *TaskTool) resolveTarget(ctx context.Context, target string) (string, error) {
	if t.ResolveTarget != nil {
		return t.ResolveTarget(ctx, target)
	}
	threadID := strings.TrimSpace(target)
	if threadID == "" {
		return "", fmt.Errorf("unknown target %q", target)
	}
	return threadID, nil
}

func (t *TaskTool) formatOutbound(ctx context.Context, msg OutboundMessage) (*FormattedOutboundMessage, error) {
	if t.FormatOutbound != nil {
		return t.FormatOutbound(ctx, msg)
	}
	return &FormattedOutboundMessage{
		Payload: []byte(msg.Content),
	}, nil
}

func (t *TaskTool) listRecentThreadEvents(ctx context.Context, threadID string, limit int) ([]*Event, error) {
	return t.Host.ListEvents(ctx, ListEventsRequest{
		ThreadID: threadID,
		Limit:    limit,
		Reverse:  true,
	})
}

func taskToolDataResult(data any) string {
	return taskToolResult{Data: data}.String()
}

func taskToolErrorResult(msg string) string {
	return taskToolResult{Errmsg: msg}.String()
}

type taskToolResult struct {
	Data   any    `json:"data"`
	Errmsg string `json:"errmsg"`
}

func (r taskToolResult) String() string {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf(`{"data":null,"errmsg":%q}`, err.Error())
	}
	return string(b)
}

func mergeTaskToolMetadata(staticMetadata map[string]string, dynamicMetadata map[string]string) map[string]string {
	if len(staticMetadata) == 0 && len(dynamicMetadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(staticMetadata)+len(dynamicMetadata))
	for k, v := range staticMetadata {
		out[k] = v
	}
	for k, v := range dynamicMetadata {
		out[k] = v
	}
	return out
}

func newTaskToolMessageID() string {
	return "msg_" + uuid.NewString()
}
