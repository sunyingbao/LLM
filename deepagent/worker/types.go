// Package agentworker defines the narrow contract between a worker host and a
// business Agent runtime.
//
// A host owns transport and lifecycle mechanics: scanning or accepting work,
// delivering messages, acking messages after the runtime accepts them, appending
// events, blocking or releasing claims, interrupting active turns, and closing
// runtime resources. The runtime owns business semantics: how inputs are
// interpreted, how model/tool execution is driven, which events are emitted, and
// when the current claim should yield back to the host.
package agentworker

import (
	"context"
	"time"
)

// MessageType is a host-defined discriminator for one message payload.
//
// New business protocols should define their own message type values outside
// this base package. MessageTypeText is kept only as the historical default.
type MessageType string

const (
	MessageTypeText MessageType = "text"
)

// SenderType describes who produced a message.
type SenderType string

const (
	SenderTypeUser   SenderType = "user"
	SenderTypeAgent  SenderType = "agent"
	SenderTypeSystem SenderType = "system"
)

// Sender identifies the producer of one message.
type Sender struct {
	Type SenderType
	ID   string
}

// Message is the host-agnostic input delivered to one thread runtime.
//
// A nil return from AgentThread.PostMessage means the runtime accepted this
// message and the host may ack it. A runtime that has already closed should
// return ErrThreadClosed so the host can leave the message unacked.
type Message struct {
	ID       string
	Sender   *Sender
	Type     MessageType
	Payload  []byte
	Metadata map[string]string
}

// EventType describes the semantic kind of an event.
type EventType string

// ThreadInterruptKind describes why the host is asking the runtime to interrupt
// its current active turn.
type ThreadInterruptKind string

const (
	ThreadInterruptKindCancelInput ThreadInterruptKind = "cancel_input"
	ThreadInterruptKindCloseThread ThreadInterruptKind = "close_thread"
	// ThreadInterruptKindWorkerShutdownTimeout asks the runtime to abort the
	// active turn because the worker is shutting down and graceful drain timed
	// out. The runtime owns any business-level terminal event emitted for this
	// interrupt.
	ThreadInterruptKindWorkerShutdownTimeout ThreadInterruptKind = "worker_shutdown_timeout"
)

// Event is the host-agnostic event emitted by one thread runtime.
//
// agentworker does not define event payload semantics. Event.Type and Payload
// are owned by the business protocol layered on top of this package.
type Event struct {
	ID       string
	ThreadID string
	TurnID   string
	Type     EventType
	Payload  []byte
	Metadata map[string]string
	TS       time.Time

	// Optional host delivery hints. Hosts that do not distinguish durable
	// storage from live fanout may ignore them.
	PersistToEventLog *bool
	FanoutToSession   *bool
}

// ActiveTurn describes the single turn currently owned by one runtime.
//
// Hosts use ActiveTurn for drain, interrupt, and idle decisions. A nil
// ActiveTurn means the runtime is inactive and will not emit more ordered output
// for the previous turn. ConsumedMessageIDs is optional host attribution data;
// it is not the business event protocol.
type ActiveTurn struct {
	TurnID             string
	ConsumedMessageIDs []string
}

// ThreadInterruptRequest asks the runtime to interrupt its current active turn.
//
// A nil Interrupt return means the request was accepted; it does not mean the
// active turn ended synchronously. Hosts must keep observing ActiveTurn and
// ThreadOutput for completion.
type ThreadInterruptRequest struct {
	Kind             ThreadInterruptKind
	ControlMessageID string
	CutoffMessageID  string
	Reason           string
	// Timeout controls how long Interrupt waits for the running turn to stop.
	// Set: if the turn is still blocked after this duration, Interrupt returns
	// an interrupted result anyway.
	// Unset: Interrupt only records the interrupt request; if the current model
	// or tool call is blocked, the turn may keep running until that call returns.
	Timeout *time.Duration
}

// PendingBlock records runtime-defined values required to resume a blocked
// thread. The host should persist and route these values, but should not infer
// business semantics from them.
type PendingBlock struct {
	TurnID       string
	InterruptID  string
	CheckpointID string
	Kind         string
}

// AgentThread is the business-provided runtime bound to one thread.
//
// The contract is intentionally small:
//   - Init is called once for a claimed or hosted thread instance.
//   - PostMessage should return quickly after accepting or rejecting input.
//   - PostMessage must return ErrThreadClosed after Close takes effect.
//   - Interrupt accepts an asynchronous request; completion is observed through
//     ActiveTurn and ThreadOutput.
//   - Close releases runtime resources and should make future PostMessage calls
//     fail with ErrThreadClosed.
//
// Host implementations decide how messages, events, and yield signals are
// persisted or bridged to an external control plane.
type AgentThread interface {
	Init(ctx context.Context) (*ThreadOutput, error)
	PostMessage(ctx context.Context, msg *Message) (*PostMessageResult, error)
	Interrupt(ctx context.Context, req ThreadInterruptRequest) error
	ActiveTurn() *ActiveTurn
	Close(ctx context.Context) error
}

// PostMessageResult describes how a runtime accepted one message.
// It intentionally exposes only stable turn assignment facts, not runtime
// internals such as TurnHandle or runner config snapshots.
type PostMessageResult struct {
	TurnID string
}

// ThreadOutput is the asynchronous, ordered output surface of one thread
// runtime.
type ThreadOutput struct {
	Items <-chan ThreadOutputItem
}

// ThreadOutputItem is one ordered runtime output item. A runtime should emit
// events and yield signals through this single channel so hosts can preserve
// event ordering before changing thread state or releasing a lease.
type ThreadOutputItem struct {
	Event *Event
	Yield *ThreadYield
}

// ThreadYield describes why a runtime stops owning the current host claim.
//
// It is not a thread lifecycle decision. Hosts still decide whether to release,
// block, or complete a close control message according to their control-plane
// contract.
type ThreadYield struct {
	Reason string
	Block  *PendingBlock
	Err    error
}
