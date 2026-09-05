package runtime

import (
	"fmt"
	"net/url"
	"strings"

	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
)

const globalThreadRefPrefix = "uar:v1:"

type RuntimeKind string

const (
	RuntimeLocal  RuntimeKind = "local"
	RuntimeRemote RuntimeKind = "remote"
)

func (kind RuntimeKind) Validate() (err error) {
	switch kind {
	case RuntimeLocal, RuntimeRemote:
		return nil
	default:
		return fmt.Errorf("unsupported runtime kind %q", kind)
	}
}

// GlobalThreadRef is the stable identity used for every post-create operation.
type GlobalThreadRef struct {
	Runtime   RuntimeKind `json:"runtime"`
	Namespace string      `json:"namespace,omitempty"`
	ThreadID  string      `json:"thread_id"`
}

func (ref GlobalThreadRef) Validate() (err error) {
	if err = ref.Runtime.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(ref.ThreadID) == "" {
		return fmt.Errorf("thread_id is required")
	}
	return nil
}

func (ref GlobalThreadRef) MarshalText() (encoded []byte, err error) {
	if err = ref.Validate(); err != nil {
		return nil, err
	}
	value := globalThreadRefPrefix + strings.Join([]string{
		url.QueryEscape(string(ref.Runtime)),
		url.QueryEscape(ref.Namespace),
		url.QueryEscape(ref.ThreadID),
	}, ":")
	encoded = []byte(value)
	return encoded, nil
}

func (ref *GlobalThreadRef) UnmarshalText(encoded []byte) (err error) {
	if ref == nil {
		return fmt.Errorf("global thread reference is nil")
	}
	value := string(encoded)
	if !strings.HasPrefix(value, globalThreadRefPrefix) {
		return fmt.Errorf("unsupported global thread reference version")
	}
	parts := strings.Split(strings.TrimPrefix(value, globalThreadRefPrefix), ":")
	if len(parts) != 3 {
		return fmt.Errorf("invalid global thread reference")
	}
	decoded := make([]string, len(parts))
	for i, part := range parts {
		if decoded[i], err = url.QueryUnescape(part); err != nil {
			return fmt.Errorf("decode global thread reference part %d: %w", i, err)
		}
	}
	candidate := GlobalThreadRef{
		Runtime:   RuntimeKind(decoded[0]),
		Namespace: decoded[1],
		ThreadID:  decoded[2],
	}
	if err = candidate.Validate(); err != nil {
		return err
	}
	*ref = candidate
	return nil
}

type WorkspaceSpec struct {
	ProjectID  string `json:"project_id,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	BackendRef string `json:"backend_ref,omitempty"`
}

type ThreadState string

const (
	ThreadStateIdle        ThreadState = "idle"
	ThreadStateRunning     ThreadState = "running"
	ThreadStateBlocked     ThreadState = "blocked"
	ThreadStateInterrupted ThreadState = "interrupted"
	ThreadStateFailed      ThreadState = "failed"
)

type Thread struct {
	Ref         GlobalThreadRef `json:"ref"`
	Workspace   WorkspaceSpec   `json:"workspace,omitempty"`
	Title       string          `json:"title,omitempty"`
	State       ThreadState     `json:"state"`
	CreatedAtMS int64           `json:"created_at_ms,omitempty"`
	UpdatedAtMS int64           `json:"updated_at_ms,omitempty"`
}

type CreateThreadRequest struct {
	Runtime   RuntimeKind      `json:"runtime"`
	Namespace string           `json:"namespace,omitempty"`
	ParentRef *GlobalThreadRef `json:"parent_ref,omitempty"`
	Workspace WorkspaceSpec    `json:"workspace,omitempty"`
	Title     string           `json:"title,omitempty"`
}

type CreateThreadResult struct {
	Thread *Thread `json:"thread"`
}

type SubmitRequest struct {
	Ref      GlobalThreadRef        `json:"ref"`
	Input    protoinput.UserMessage `json:"input"`
	Metadata map[string]string      `json:"metadata,omitempty"`
}

type SubmitResult struct {
	Ref    GlobalThreadRef `json:"ref"`
	TurnID string          `json:"turn_id"`
}

type ResumeRequest struct {
	Ref     GlobalThreadRef              `json:"ref"`
	Payload protoinput.ResumeTurnPayload `json:"payload"`
}

type StopRequest struct {
	Ref    GlobalThreadRef `json:"ref"`
	TurnID string          `json:"turn_id,omitempty"`
}

type StopResult struct {
	Ref     GlobalThreadRef `json:"ref"`
	Stopped bool            `json:"stopped"`
}

type ListThreadsQuery struct {
	Runtime   RuntimeKind `json:"runtime,omitempty"`
	Namespace string      `json:"namespace,omitempty"`
	Cursor    string      `json:"cursor,omitempty"`
	Limit     int         `json:"limit,omitempty"`
}

type ListThreadsResult struct {
	Threads    []*Thread `json:"threads"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type TimelineQuery struct {
	Ref          GlobalThreadRef `json:"ref"`
	AfterEventID string          `json:"after_event_id,omitempty"`
	Limit        int             `json:"limit,omitempty"`
}

type TimelineResult struct {
	Events     []timeline.Event `json:"events"`
	NextCursor string           `json:"next_cursor,omitempty"`
}
