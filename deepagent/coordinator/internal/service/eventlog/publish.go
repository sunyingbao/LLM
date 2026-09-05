package eventlog

import (
	"context"
	"errors"
	"time"

	"code.byted.org/gopkg/logs/v2"
)

var (
	ErrInvalidDeliveryOptions = errors.New("persist_to_event_log and fanout_to_session cannot both be false")
	ErrFanoutRequiresSession  = errors.New("fanout_to_session requires thread session_id")
	ErrFanoutUnavailable      = errors.New("fanout_to_session is unavailable")
)

type eventStore interface {
	ValidateLease(ctx context.Context, namespace string, threadID int64, leaseToken string) error
	GetThreadSession(ctx context.Context, namespace string, threadID int64) (string, error)
	AppendEvents(ctx context.Context, req AppendEventsRequest) ([]Event, int64, error)
}

type sessionFanout interface {
	FanoutEventRecords(ctx context.Context, namespace string, sessionID string, events []Event) error
}

func Publish(ctx context.Context, store eventStore, fanout sessionFanout, req AppendEventsRequest) (published []Event, nextCursor int64, err error) {
	if len(req.Events) == 0 {
		return nil, 0, nil
	}
	sessionID, err := store.GetThreadSession(ctx, req.Namespace, req.ThreadID)
	if err != nil {
		return nil, 0, err
	}

	persistedInputs := make([]Event, 0, len(req.Events))
	requireFanout := false
	for _, event := range req.Events {
		persist := event.PersistToEventLog == nil || *event.PersistToEventLog
		deliver := event.FanoutToSession == nil || *event.FanoutToSession
		if !persist && !deliver {
			return nil, 0, ErrInvalidDeliveryOptions
		}
		if deliver && !persist && sessionID == "" {
			return nil, 0, ErrFanoutRequiresSession
		}
		if persist {
			persistedInputs = append(persistedInputs, event)
		}
		requireFanout = requireFanout || deliver && !persist
	}

	persistRequest := req
	persistRequest.Events = persistedInputs
	persisted, nextCursor, err := store.AppendEvents(ctx, persistRequest)
	if err != nil {
		return nil, 0, err
	}
	published = append([]Event(nil), req.Events...)
	fanoutEvents := make([]Event, 0, len(published))
	for index := range published {
		event := &published[index]
		if event.TurnID == "" {
			event.TurnID = req.TurnID
		}
		event.SessionID = sessionID
		event.ThreadID = req.ThreadID
		if event.CreatedAt.IsZero() {
			event.CreatedAt = time.Now()
		}
		if (event.PersistToEventLog == nil || *event.PersistToEventLog) && len(persisted) > 0 {
			*event = persisted[0]
			persisted = persisted[1:]
		}
		if flag := req.Events[index].FanoutToSession; flag == nil || *flag {
			fanoutEvents = append(fanoutEvents, *event)
		}
	}
	if req.LeaseToken != "" && len(fanoutEvents) > 0 {
		if err = store.ValidateLease(ctx, req.Namespace, req.ThreadID, req.LeaseToken); err != nil {
			return nil, 0, err
		}
	}
	if len(fanoutEvents) == 0 || sessionID == "" {
		return published, nextCursor, nil
	}
	if fanout == nil {
		err = ErrFanoutUnavailable
	} else {
		err = fanout.FanoutEventRecords(ctx, req.Namespace, sessionID, fanoutEvents)
	}
	if err != nil {
		if requireFanout {
			return nil, 0, err
		}
		logs.CtxWarn(ctx, "[EventPublish] optional fanout failed after persistence, namespace=%s session_id=%s event_count=%d err=%v", req.Namespace, sessionID, len(fanoutEvents), err)
	}
	return published, nextCursor, nil
}
