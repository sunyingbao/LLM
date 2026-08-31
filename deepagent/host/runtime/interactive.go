package runtime

import (
	"context"
	"strings"
	"sync"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	protoinput "eino-cli/deepagent/cloud/protocol/input"
	"eino-cli/deepagent/cloud/protocol/timeline"
	sdkruntime "eino-cli/deepagent/runtime"
)

type ActionResult struct {
	Success bool
	Message string
	Output  string
}

type InteractiveRuntime interface {
	StartTurn(ctx context.Context, prompt string) (stream *TurnStream, err error)
	Resume(ctx context.Context, ref sdkruntime.GlobalThreadRef, payload protoinput.ResumeTurnPayload) (err error)
	ClearThread()
	ConsolidateMemory(ctx context.Context) (result ActionResult, err error)
	ExportThreadRef() (payload []byte, err error)
	ImportThreadRef(payload []byte) (err error)
	SetPlanMode(ctx context.Context, enabled bool) (result bool, err error)
	Name() (name string)
	RuntimeKind() (kind sdkruntime.RuntimeKind)
}

type TurnStream struct {
	Ref    sdkruntime.GlobalThreadRef
	TurnID string
	Events <-chan timeline.Event

	stop         func(context.Context) error
	subscription sdkruntime.TimelineSubscription
	once         sync.Once
	turnMu       sync.Mutex
}

// AcceptEvent locks a remotely submitted stream to the first observed turn.
// Local runtimes already return TurnID from Submit, while Agent Coordinator
// assigns it asynchronously and exposes it first through TURN_STARTED.
func (stream *TurnStream) AcceptEvent(event timeline.Event) (accepted bool) {
	if stream == nil {
		return false
	}
	stream.turnMu.Lock()
	defer stream.turnMu.Unlock()
	if stream.TurnID == "" {
		if protoevent.EventType(event.EventType) != protoevent.EventTypeTurnStarted || strings.TrimSpace(event.TurnID) == "" {
			return false
		}
		stream.TurnID = event.TurnID
	}
	return event.TurnID == stream.TurnID
}

func (stream *TurnStream) Stop(ctx context.Context) (err error) {
	if stream == nil || stream.stop == nil {
		return nil
	}
	err = stream.stop(ctx)
	return err
}

func (stream *TurnStream) Close() (err error) {
	if stream == nil || stream.subscription == nil {
		return nil
	}
	stream.once.Do(func() { err = stream.subscription.Close() })
	return err
}

func (stream *TurnStream) Err() (err error) {
	if stream == nil || stream.subscription == nil {
		return nil
	}
	err = stream.subscription.Err()
	return err
}
