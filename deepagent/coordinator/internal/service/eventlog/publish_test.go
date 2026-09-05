package eventlog

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPublishPlansPersistenceAndFanoutAndRestoresInputOrder(t *testing.T) {
	persistOnly, fanoutOnly := true, false
	events := []Event{
		{EventType: "persist", PersistToEventLog: &persistOnly, FanoutToSession: &fanoutOnly},
		{EventType: "live", PersistToEventLog: &fanoutOnly, FanoutToSession: &persistOnly},
		{EventType: "both"},
	}
	store := &fakeEventStore{sessionID: "session-1", nextCursor: 88}
	fanout := &fakeFanout{}
	startedAt := time.Now()
	published, nextCursor, err := Publish(context.Background(), store, fanout, AppendEventsRequest{
		Namespace: "ns", ThreadID: 42, TurnID: "turn-1", Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(store.appended.Events); !reflect.DeepEqual(got, []string{"persist", "both"}) {
		t.Fatalf("persisted types = %v", got)
	}
	if got := eventTypes(fanout.events); !reflect.DeepEqual(got, []string{"live", "both"}) {
		t.Fatalf("fanout types = %v", got)
	}
	if got := eventTypes(published); !reflect.DeepEqual(got, []string{"persist", "live", "both"}) {
		t.Fatalf("result types = %v", got)
	}
	if published[0].EventID == 0 || published[2].EventID == 0 || published[1].EventID != 0 {
		t.Fatalf("event ids = %+v", published)
	}
	if published[1].SessionID != "session-1" || published[1].TurnID != "turn-1" || published[1].CreatedAt.Before(startedAt) || published[1].CreatedAt.After(time.Now()) {
		t.Fatalf("completed live event = %+v", published[1])
	}
	if nextCursor != 88 {
		t.Fatalf("next cursor = %d", nextCursor)
	}
}

func TestPublishRejectsInvalidDeliveryCombinations(t *testing.T) {
	falseValue, trueValue := false, true
	tests := []struct {
		name      string
		sessionID string
		event     Event
		want      error
	}{
		{name: "neither", sessionID: "session", event: Event{EventType: "x", PersistToEventLog: &falseValue, FanoutToSession: &falseValue}, want: ErrInvalidDeliveryOptions},
		{name: "fanout without session", event: Event{EventType: "x", PersistToEventLog: &falseValue, FanoutToSession: &trueValue}, want: ErrFanoutRequiresSession},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Publish(context.Background(), &fakeEventStore{sessionID: tt.sessionID}, &fakeFanout{}, AppendEventsRequest{Namespace: "ns", ThreadID: 1, TurnID: "turn", Events: []Event{tt.event}})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPublishPreservesPositionsAndDeliveryFlagsWithPartialPersistenceResult(t *testing.T) {
	no := false
	store := &partialEventStore{fakeEventStore{sessionID: "session", nextCursor: 88}}
	fanout := &fakeFanout{}
	published, cursor, err := Publish(context.Background(), store, fanout, AppendEventsRequest{
		Namespace: "ns", ThreadID: 42, TurnID: "run",
		Events: []Event{
			{EventType: "persist", FanoutToSession: &no},
			{EventType: "live", PersistToEventLog: &no},
			{EventType: "both", Payload: []byte("original")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(published); !reflect.DeepEqual(got, []string{"persist", "live", "both"}) {
		t.Fatalf("published order = %v", got)
	}
	if published[0].EventID != 1 || published[1].EventID != 0 || published[2].EventID != 0 || cursor != 88 {
		t.Fatalf("published ids = %v, cursor = %d", eventIDs(published), cursor)
	}
	if published[2].SessionID != "session" || published[2].TurnID != "run" || published[2].CreatedAt.IsZero() || string(published[2].Payload) != "original" {
		t.Fatalf("unpersisted event lost original fields or defaults: %+v", published[2])
	}
	if got := eventTypes(fanout.events); !reflect.DeepEqual(got, []string{"live", "both"}) {
		t.Fatalf("fanout must use input flags, got %v", got)
	}
}

type partialEventStore struct {
	fakeEventStore
}

func (f *partialEventStore) AppendEvents(ctx context.Context, req AppendEventsRequest) (events []Event, cursor int64, err error) {
	events, cursor, err = f.fakeEventStore.AppendEvents(ctx, req)
	events = events[:1]
	events[0].PersistToEventLog, events[0].FanoutToSession = nil, nil
	return events, cursor, err
}

func TestPublishMakesFanoutFailureRequiredOnlyForFanoutOnlyEvents(t *testing.T) {
	falseValue, trueValue := false, true
	fanoutErr := errors.New("fanout failed")
	optional := Event{EventType: "persisted"}
	if _, _, err := Publish(context.Background(), &fakeEventStore{sessionID: "session"}, &fakeFanout{err: fanoutErr}, AppendEventsRequest{Namespace: "ns", ThreadID: 1, TurnID: "turn", Events: []Event{optional}}); err != nil {
		t.Fatalf("optional fanout err = %v", err)
	}
	required := Event{EventType: "live", PersistToEventLog: &falseValue, FanoutToSession: &trueValue}
	if _, _, err := Publish(context.Background(), &fakeEventStore{sessionID: "session"}, &fakeFanout{err: fanoutErr}, AppendEventsRequest{Namespace: "ns", ThreadID: 1, TurnID: "turn", Events: []Event{required}}); !errors.Is(err, fanoutErr) {
		t.Fatalf("required fanout err = %v", err)
	}
	if _, _, err := Publish(context.Background(), &fakeEventStore{sessionID: "session"}, nil, AppendEventsRequest{Namespace: "ns", ThreadID: 1, TurnID: "turn", Events: []Event{required}}); !errors.Is(err, ErrFanoutUnavailable) {
		t.Fatalf("unavailable fanout err = %v", err)
	}
}

type fakeEventStore struct {
	leaseErr   error
	sessionID  string
	appended   AppendEventsRequest
	nextCursor int64
}

func (f *fakeEventStore) ValidateLease(ctx context.Context, namespace string, threadID int64, leaseToken string) (err error) {
	return f.leaseErr
}

func TestPublishRejectsStaleLeaseBeforeFanout(t *testing.T) {
	falseValue := false
	for _, persist := range []*bool{nil, &falseValue} {
		store := &fakeEventStore{sessionID: "session", leaseErr: ErrLeaseMismatch}
		fanout := &fakeFanout{}
		_, _, err := Publish(context.Background(), store, fanout, AppendEventsRequest{Namespace: "ns", ThreadID: 42, LeaseToken: "old", Events: []Event{{EventType: "event", PersistToEventLog: persist}}})
		if !errors.Is(err, ErrLeaseMismatch) {
			t.Fatalf("error = %v", err)
		}
		if len(fanout.events) != 0 {
			t.Fatal("stale lease event was published")
		}
		if store.appended.LeaseToken != "old" {
			t.Fatal("lease token missing at persistence boundary")
		}
	}
}

func (f *fakeEventStore) GetThreadSession(context.Context, string, int64) (string, error) {
	return f.sessionID, nil
}

func (f *fakeEventStore) AppendEvents(_ context.Context, req AppendEventsRequest) ([]Event, int64, error) {
	f.appended = req
	result := append([]Event(nil), req.Events...)
	for idx := range result {
		result[idx].EventID = int64(idx + 1)
		result[idx].ThreadID = req.ThreadID
		result[idx].TurnID = req.TurnID
		result[idx].SessionID = f.sessionID
	}
	return result, f.nextCursor, nil
}

type fakeFanout struct {
	events []Event
	err    error
}

func (f *fakeFanout) FanoutEventRecords(_ context.Context, _ string, _ string, events []Event) error {
	f.events = append([]Event(nil), events...)
	return f.err
}

func eventTypes(events []Event) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, event.EventType)
	}
	return result
}
