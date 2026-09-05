package coordinator

import (
	"eino-cli/deepagent/coordinator/internal/model"
	"eino-cli/deepagent/coordinator/internal/util"
)

// Legacy characterization tests assert database-shaped fields. These adapters
// only change result shape; every operation runs through the public Coordinator.
func testThreadRecord(thread *Thread) (row *model.TThread) {
	if thread == nil {
		return nil
	}
	return &model.TThread{
		ThreadId: thread.ThreadID, Namespace: thread.Namespace, Env: thread.Env,
		UserId: thread.UserID, SessionId: thread.SessionID, Title: thread.Title,
		Status: string(thread.Status), StatusReason: thread.StatusReason,
		ReadyAt: thread.ReadyAt, LeaseDeadlineAt: thread.LeaseDeadlineAt,
		LeaseOwnerHint: thread.LeaseOwnerHint, LastActiveAt: thread.LastActiveAt,
		CreatedAt: thread.CreatedAt, UpdatedAt: thread.UpdatedAt, ClosedAt: thread.ClosedAt,
		CreatedBy: thread.CreatedBy, MetadataJson: util.ToString(thread.Metadata),
		Profile: util.ToString(thread.Profile),
	}
}

func testMessageRecord(message *Message) (row *model.TMailboxMessage) {
	if message == nil {
		return nil
	}
	row = &model.TMailboxMessage{
		MessageId: message.MessageID, ThreadId: message.ThreadID, MessageType: message.MessageType,
		Status: string(message.Status), Payload: string(message.Payload),
		MetadataJson: util.ToString(message.Metadata), TriggerTurnId: message.TriggerRunID,
		CreatedAt: message.CreatedAt, UpdatedAt: message.UpdatedAt, HandledAt: message.HandledAt,
	}
	if message.Sender != nil {
		row.SenderType = string(message.Sender.Type)
		row.SenderId = message.Sender.ID
	}
	return row
}

func testSubmitInput(result SubmitInputResult, err error) (message *model.TMailboxMessage, thread *model.TThread, resultErr error) {
	return testMessageRecord(result.Message), testThreadRecord(result.Thread), err
}

func testReadPendingInputs(result ReadPendingInputsResult, err error) (messages []*model.TMailboxMessage, serverTimeMS int64, resultErr error) {
	rows, _ := testConfirmInputDelivery(result.Messages, nil)
	return rows, result.ServerTimeMS, err
}

func testConfirmInputDelivery(messages []*Message, err error) (rows []*model.TMailboxMessage, resultErr error) {
	for _, message := range messages {
		rows = append(rows, testMessageRecord(message))
	}
	return rows, err
}

type controlResultRecord struct {
	Thread              *model.TThread
	ControlMessage      *model.TMailboxMessage
	CutoffMessageID     int64
	CancelledMessageIDs []int64
}

func testRequestInputCancel(result *RequestInputCancelResult, err error) (record *controlResultRecord, resultErr error) {
	if result == nil {
		return nil, err
	}
	return &controlResultRecord{Thread: testThreadRecord(result.Thread), ControlMessage: testMessageRecord(result.ControlMessage), CutoffMessageID: result.CutoffMessageID, CancelledMessageIDs: result.CancelledMessageIDs}, err
}

func testRequestThreadClose(result *RequestThreadCloseResult, err error) (record *controlResultRecord, resultErr error) {
	if result == nil {
		return nil, err
	}
	return &controlResultRecord{Thread: testThreadRecord(result.Thread), ControlMessage: testMessageRecord(result.ControlMessage), CancelledMessageIDs: result.CancelledMessageIDs}, err
}

func testConfirmThreadClosed(result *ConfirmThreadClosedResult, err error) (record *controlResultRecord, resultErr error) {
	if result == nil {
		return nil, err
	}
	return &controlResultRecord{Thread: testThreadRecord(result.Thread), ControlMessage: testMessageRecord(result.ControlMessage)}, err
}
