package model

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

const (
	ThreadStatusIdle    = "idle"
	ThreadStatusReady   = "ready"
	ThreadStatusRunning = "running"
	ThreadStatusBlocked = "blocked"
	ThreadStatusClosing = "closing"
	ThreadStatusClosed  = "closed"

	MessageStatusPending  = "pending"
	MessageStatusAcked    = "acked"
	MessageStatusCanceled = "canceled"

	SenderTypeUser   = "user"
	SenderTypeSystem = "system"
	SenderTypeAgent  = "agent"

	AgentCoordinatorSenderID = "agent_coordinator"

	MessageTypeControlCancelInput = "__agent_control_cancel_input"
	MessageTypeControlCloseThread = "__agent_control_close_thread"
	ControlMessageTypePrefix      = "__agent_control_"
	ControlTypeCancelInput        = "cancel_input"
	ControlTypeCloseThread        = "close_thread"

	MetadataKeyBytedCtxMetaInfo = "byted_ctx_meta_info"
	MetadataKeyKEnv             = "K_ENV"
)

var ErrInvalidCursor = errors.New("invalid cursor")

func IsControlMessageType(messageType string) (found bool) {
	return strings.HasPrefix(messageType, ControlMessageTypePrefix)
}

func IsOrdinaryInputMessage(messageType string) (found bool) {
	return !IsControlMessageType(messageType)
}

type ThreadProfile struct {
	Role string `json:"role,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

// TimeFromUnixMicrosForDB restores a cursor timestamp for DATETIME comparisons.
func TimeFromUnixMicrosForDB(unixMicros int64) (at time.Time) {
	return time.UnixMicro(unixMicros).Local()
}

type ReadyCursor struct {
	ReadyAtUnixMicros int64 `json:"ready_at_us"`
	ThreadID          int64 `json:"thread_id"`
}

func EncodeReadyCursor(cursor ReadyCursor) (value string, resultErr error) {
	raw, err := sonic.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeReadyCursor(encoded string) (value ReadyCursor, resultErr error) {
	if encoded == "" {
		return ReadyCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ReadyCursor{}, ErrInvalidCursor
	}
	var cursor ReadyCursor
	if err := sonic.Unmarshal(raw, &cursor); err != nil {
		return ReadyCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}
