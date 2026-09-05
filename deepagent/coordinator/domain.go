package coordinator

import (
	"eino-cli/deepagent/coordinator/internal/model"
	"eino-cli/deepagent/coordinator/internal/service/eventlog"
	"eino-cli/deepagent/coordinator/internal/util"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

type ThreadStatus string

const (
	ThreadStatusIdle    ThreadStatus = "idle"
	ThreadStatusReady   ThreadStatus = "ready"
	ThreadStatusRunning ThreadStatus = "running"
	ThreadStatusBlocked ThreadStatus = "blocked"
	ThreadStatusClosing ThreadStatus = "closing"
	ThreadStatusClosed  ThreadStatus = "closed"
)

type MessageStatus string

const (
	MessageStatusPending  MessageStatus = "pending"
	MessageStatusAcked    MessageStatus = "acked"
	MessageStatusCanceled MessageStatus = "canceled"
)

type SenderType string

const (
	SenderTypeUser   SenderType = "user"
	SenderTypeSystem SenderType = "system"
	SenderTypeAgent  SenderType = "agent"
)

type Profile struct {
	Role string
	Cwd  string
}

type Thread struct {
	ThreadID        int64
	ReadyAt         time.Time
	LeaseDeadlineAt time.Time
	LastActiveAt    time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ClosedAt        time.Time
	Namespace       string
	Env             string
	UserID          int64
	SessionID       string
	Title           string
	Status          ThreadStatus
	StatusReason    string
	LeaseOwnerHint  string
	CreatedBy       string
	Metadata        map[string]string
	Profile         *Profile
}

type Sender struct {
	Type SenderType
	ID   string
}

type Message struct {
	MessageID    int64
	ThreadID     int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	HandledAt    time.Time
	Sender       *Sender
	MessageType  string
	Status       MessageStatus
	Payload      []byte
	Metadata     map[string]string
	TriggerRunID string
}

type Lease struct {
	ThreadID        int64
	LeaseToken      string
	LeaseDeadlineAt time.Time
}

type InitialMessage struct {
	SenderType  SenderType
	SenderID    string
	MessageType string
	Payload     []byte
	Metadata    map[string]string
}

type Event = eventlog.Event

type CreateThreadResult struct {
	Thread         *Thread
	InitialMessage *Message
}

type ResumeFromBlockResult struct {
	Thread  *Thread
	Message *Message
}

type RequestInputCancelResult struct {
	Thread              *Thread
	CutoffMessageID     int64
	ControlMessage      *Message
	CancelledMessageIDs []int64
}

type RequestThreadCloseResult struct {
	Thread              *Thread
	ControlMessage      *Message
	CancelledMessageIDs []int64
}

type ConfirmThreadClosedResult struct {
	Thread         *Thread
	ControlMessage *Message
}

type PublishEventsResult struct {
	Events     []Event
	NextCursor int64
}

func threadFromModel(row *model.TThread) (thread *Thread, err error) {
	if row == nil {
		return nil, nil
	}
	metadata, err := parseStringMap(row.MetadataJson)
	if err != nil {
		return nil, fmt.Errorf("decode thread metadata: %w", err)
	}
	profile, err := profileFromJSON(row.Profile)
	if err != nil {
		return nil, fmt.Errorf("decode thread profile: %w", err)
	}
	status, err := threadStatusFromModel(row.Status)
	if err != nil {
		return nil, err
	}
	thread = &Thread{
		ThreadID:        row.ThreadId,
		ReadyAt:         row.ReadyAt,
		LeaseDeadlineAt: row.LeaseDeadlineAt,
		LastActiveAt:    row.LastActiveAt,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		ClosedAt:        row.ClosedAt,
		Namespace:       row.Namespace,
		Env:             row.Env,
		UserID:          row.UserId,
		SessionID:       row.SessionId,
		Title:           row.Title,
		Status:          status,
		StatusReason:    row.StatusReason,
		LeaseOwnerHint:  row.LeaseOwnerHint,
		CreatedBy:       row.CreatedBy,
		Metadata:        metadata,
		Profile:         profile,
	}
	return thread, nil
}

func threadsFromModel(rows []*model.TThread) (threads []*Thread, err error) {
	threads = make([]*Thread, 0, len(rows))
	for _, row := range rows {
		thread, convertErr := threadFromModel(row)
		if convertErr != nil {
			return nil, convertErr
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

func messageFromModel(row *model.TMailboxMessage) (message *Message, err error) {
	if row == nil {
		return nil, nil
	}
	metadata, err := parseStringMap(row.MetadataJson)
	if err != nil {
		return nil, fmt.Errorf("decode message metadata: %w", err)
	}
	status, err := messageStatusFromModel(row.Status)
	if err != nil {
		return nil, err
	}
	message = &Message{
		MessageID:    row.MessageId,
		ThreadID:     row.ThreadId,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		HandledAt:    row.HandledAt,
		Sender:       &Sender{Type: senderTypeFromModel(row.SenderType), ID: row.SenderId},
		MessageType:  row.MessageType,
		Status:       status,
		Payload:      []byte(row.Payload),
		Metadata:     metadata,
		TriggerRunID: row.TriggerTurnId,
	}
	return message, nil
}

func messagesFromModel(rows []*model.TMailboxMessage) (messages []*Message, err error) {
	messages = make([]*Message, 0, len(rows))
	for _, row := range rows {
		message, convertErr := messageFromModel(row)
		if convertErr != nil {
			return nil, convertErr
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func profileToModel(profile *Profile) (result *model.ThreadProfile) {
	if profile == nil {
		return nil
	}
	return &model.ThreadProfile{Role: profile.Role, Cwd: profile.Cwd}
}

func cloneEvent(event Event) (cloned Event) {
	cloned = event
	cloned.Payload = append([]byte(nil), event.Payload...)
	cloned.Metadata = cloneStringMap(event.Metadata)
	cloned.PersistToEventLog = cloneBool(event.PersistToEventLog)
	cloned.FanoutToSession = cloneBool(event.FanoutToSession)
	return cloned
}

func cloneEvents(events []Event) (cloned []Event) {
	cloned = make([]Event, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, cloneEvent(event))
	}
	return cloned
}

func cancelResult(thread *model.TThread, message *model.TMailboxMessage, cutoff int64, cancelled []int64) (result *RequestInputCancelResult, err error) {
	result = &RequestInputCancelResult{CutoffMessageID: cutoff, CancelledMessageIDs: append([]int64(nil), cancelled...)}
	result.Thread, err = threadFromModel(thread)
	if err != nil {
		return nil, err
	}
	result.ControlMessage, err = messageFromModel(message)
	return result, err
}

func closeResult(thread *model.TThread, message *model.TMailboxMessage, cancelled []int64) (result *RequestThreadCloseResult, err error) {
	result = &RequestThreadCloseResult{CancelledMessageIDs: append([]int64(nil), cancelled...)}
	result.Thread, err = threadFromModel(thread)
	if err != nil {
		return nil, err
	}
	result.ControlMessage, err = messageFromModel(message)
	return result, err
}

func closedResult(thread *model.TThread, message *model.TMailboxMessage) (result *ConfirmThreadClosedResult, err error) {
	result = &ConfirmThreadClosedResult{}
	result.Thread, err = threadFromModel(thread)
	if err != nil {
		return nil, err
	}
	result.ControlMessage, err = messageFromModel(message)
	return result, err
}

func parseStringMap(raw string) (result map[string]string, err error) {
	result = map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	if err = sonic.UnmarshalString(raw, &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = map[string]string{}
	}
	return result, nil
}

func profileFromJSON(raw string) (profile *Profile, err error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	profile = &Profile{}
	if err = sonic.UnmarshalString(raw, profile); err != nil {
		return nil, err
	}
	if profile.Role == "" && profile.Cwd == "" {
		return nil, nil
	}
	return profile, nil
}

func threadStatusFromModel(status string) (result ThreadStatus, err error) {
	result = ThreadStatus(status)
	switch result {
	case ThreadStatusIdle, ThreadStatusReady, ThreadStatusRunning, ThreadStatusBlocked, ThreadStatusClosing, ThreadStatusClosed:
		return result, nil
	default:
		return "", fmt.Errorf("unknown thread status %q", status)
	}
}

func messageStatusFromModel(status string) (result MessageStatus, err error) {
	result = MessageStatus(status)
	switch result {
	case MessageStatusPending, MessageStatusAcked, MessageStatusCanceled:
		return result, nil
	default:
		return "", fmt.Errorf("unknown message status %q", status)
	}
}

func senderTypeFromModel(senderType string) (result SenderType) {
	switch SenderType(senderType) {
	case SenderTypeSystem:
		return SenderTypeSystem
	case SenderTypeAgent:
		return SenderTypeAgent
	default:
		return SenderTypeUser
	}
}

func normalizeSenderType(senderType SenderType) (result string) {
	return string(senderTypeFromModel(string(senderType)))
}

func cloneStringMap(source map[string]string) (result map[string]string) {
	if source == nil {
		return nil
	}
	result = make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneBool(value *bool) (result *bool) {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func resumeThreadMetadata(thread *model.TThread, activation map[string]string) (metadata map[string]string, err error) {
	metadata = map[string]string{}
	if thread != nil && strings.TrimSpace(thread.MetadataJson) != "" {
		parsed, parseErr := util.ToStruct[map[string]string](thread.MetadataJson)
		if parseErr != nil {
			return nil, parseErr
		}
		if parsed != nil {
			metadata = *parsed
		}
	}
	delete(metadata, "logid")
	delete(metadata, model.MetadataKeyBytedCtxMetaInfo)
	delete(metadata, model.MetadataKeyKEnv)
	for key, value := range activation {
		metadata[key] = value
	}
	return metadata, nil
}

func mergeRunnableThreads(groups ...[]*model.TThread) (resultThreads []*model.TThread) {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	threads := make([]*model.TThread, 0, total)
	for _, group := range groups {
		for _, thread := range group {
			if thread != nil {
				threads = append(threads, thread)
			}
		}
	}
	sort.Slice(threads, func(i, j int) bool {
		left := runnableAt(threads[i])
		right := runnableAt(threads[j])
		if left.Equal(right) {
			return threads[i].ThreadId < threads[j].ThreadId
		}
		return left.Before(right)
	})
	return threads
}

func runnableAt(thread *model.TThread) (at time.Time) {
	if thread == nil {
		return time.Time{}
	}
	if thread.Status == model.ThreadStatusRunning {
		return thread.LeaseDeadlineAt
	}
	return thread.ReadyAt
}

func threadMetadataWithActivation(metadataJSON string, activation map[string]string) (value string) {
	threadMetadata := map[string]string{}
	if strings.TrimSpace(metadataJSON) != "" {
		if parsed, err := util.ToStruct[map[string]string](metadataJSON); err == nil && parsed != nil {
			threadMetadata = *parsed
		}
	}

	delete(threadMetadata, "logid")
	delete(threadMetadata, model.MetadataKeyBytedCtxMetaInfo)
	delete(threadMetadata, model.MetadataKeyKEnv)
	if logID := strings.TrimSpace(activation["logid"]); logID != "" {
		threadMetadata["logid"] = logID
	}
	if ctxMetaInfo := strings.TrimSpace(activation[model.MetadataKeyBytedCtxMetaInfo]); ctxMetaInfo != "" {
		threadMetadata[model.MetadataKeyBytedCtxMetaInfo] = ctxMetaInfo
	}
	if env := strings.TrimSpace(activation[model.MetadataKeyKEnv]); env != "" {
		threadMetadata[model.MetadataKeyKEnv] = env
	}
	if len(threadMetadata) == 0 {
		return "{}"
	}
	return util.ToString(threadMetadata)
}

func messageIDsFromModels(messages []*model.TMailboxMessage) (resultIds []int64) {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.MessageId)
	}
	return ids
}
