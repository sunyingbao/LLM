package eventlog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"eino-cli/deepagent/coordinator/internal/model"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLeasedAppendUsesTransactionAndLeavesReplicaBindingUnchanged(t *testing.T) {
	ctx := context.Background()
	primary := newTestDB(t)
	replica, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "replica.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, replica.AutoMigrate(&model.TThread{}))
	for _, db := range []*gorm.DB{primary, replica} {
		connection, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { _ = connection.Close() })
	}
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	thread := model.TThread{ThreadId: 1, Namespace: "ns", SessionId: "primary", Status: model.ThreadStatusRunning, LeaseToken: "owner", LeaseDeadlineAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	requireThread(t, primary, &thread)
	thread.SessionId = "replica"
	requireThread(t, replica, &thread)
	svc := NewEventLog(primary, replica, &stubIDGen{ids: []int64{101, 102}}, WithClock(func() (at time.Time) { return now }))
	request := AppendEventsRequest{Namespace: "ns", ThreadID: 1, LeaseToken: "owner", TurnID: "run", Events: []Event{{EventType: "message"}}}
	appended, _, err := svc.AppendEvents(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "primary", appended[0].SessionID)
	require.Equal(t, "owner", request.LeaseToken)
	require.Zero(t, request.Events[0].EventID)
	request.LeaseToken = ""
	appended, _, err = svc.AppendEvents(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "replica", appended[0].SessionID)
}

func TestAppendEventsRejectsInvalidLease(t *testing.T) {
	for _, name := range []string{"stale", "expired", "closed", "expires during insert", "valid", "nonworker"} {
		t.Run(name, func(t *testing.T) {
			db := newTestDB(t)
			now := time.Now()
			thread := &model.TThread{ThreadId: 1, Namespace: "ns", SessionId: "session", Status: model.ThreadStatusRunning, LeaseToken: "current", LeaseDeadlineAt: now.Add(time.Minute), MetadataJson: "{}", CreatedAt: now, UpdatedAt: now}
			if name == "expired" {
				thread.LeaseDeadlineAt = now
			}
			if name == "closed" {
				thread.Status = model.ThreadStatusClosed
			}
			requireThread(t, db, thread)
			clockCalls := 0
			svc := NewEventLog(db, db, &stubIDGen{ids: []int64{101}}, WithClock(func() (current time.Time) {
				clockCalls++
				if name == "expires during insert" && clockCalls > 1 {
					return now.Add(2 * time.Minute)
				}
				return now
			}))
			token := "current"
			if name == "stale" {
				token = "old"
			}
			if name == "nonworker" {
				token = ""
			}
			_, _, err := svc.AppendEvents(context.Background(), AppendEventsRequest{Namespace: "ns", ThreadID: 1, LeaseToken: token, TurnID: "turn", Events: []Event{{EventType: "event"}}})
			valid := name == "valid" || name == "nonworker"
			if valid && err != nil {
				t.Fatal(err)
			}
			if !valid && !errors.Is(err, ErrLeaseMismatch) {
				t.Fatalf("error = %v", err)
			}
			var count int64
			if err = db.Model(&model.TEventLog{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if valid && count != 1 || !valid && count != 0 {
				t.Fatalf("persisted count = %d", count)
			}
		})
	}
}

func TestAppendEventsAndListEvents(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 20, 0, 0, 0, time.UTC)
	svc := NewEventLog(db, db, &stubIDGen{ids: []int64{101, 102, 103}}, WithClock(func() time.Time { return now }))
	requireThread(t, db, &model.TThread{
		ThreadId:     1,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-1",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	events, nextCursor, err := svc.AppendEvents(context.Background(), AppendEventsRequest{
		Namespace: "dreamina",
		ThreadID:  1,
		TurnID:    "turn-7",
		Events: []Event{
			{EventType: "message", Payload: []byte("a"), Metadata: map[string]string{"k": "v"}},
			{EventType: "progress", Payload: []byte("b")},
		},
	})
	if err != nil {
		t.Fatalf("append events: %v", err)
	}
	if len(events) != 2 || events[0].EventID != 101 || events[1].EventID != 102 {
		t.Fatalf("unexpected appended events: %+v", events)
	}
	if nextCursor != 102 {
		t.Fatalf("next cursor = %d, want 102", nextCursor)
	}
	if events[1].TurnID != "turn-7" {
		t.Fatalf("request turn id not propagated: %+v", events[1])
	}
	if !events[0].CreatedAt.Equal(now) {
		t.Fatalf("created_at not defaulted: %v", events[0].CreatedAt)
	}
	if events[0].SessionID != "sess-1" || events[1].SessionID != "sess-1" {
		t.Fatalf("session_id not propagated: %+v", events)
	}

	listed, cursor, _, hasMore, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  1,
		Cursor:    100,
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(listed) != 1 || listed[0].EventID != 101 {
		t.Fatalf("unexpected listed events: %+v", listed)
	}
	if cursor != 101 {
		t.Fatalf("cursor = %d, want 101", cursor)
	}
	if !hasMore {
		t.Fatalf("hasMore should be true")
	}
	if listed[0].Metadata["k"] != "v" {
		t.Fatalf("metadata decode failed: %+v", listed[0].Metadata)
	}

	filtered, _, _, _, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  1,
		TurnID:    "turn-7",
		EventType: "progress",
	})
	if err != nil {
		t.Fatalf("list filtered events: %v", err)
	}
	if len(filtered) != 1 || filtered[0].EventID != 102 {
		t.Fatalf("unexpected filtered events: %+v", filtered)
	}

	_, _, err = svc.AppendEvents(context.Background(), AppendEventsRequest{
		Namespace: "other",
		ThreadID:  1,
		TurnID:    "turn-1",
		Events:    []Event{{EventType: "wrong_namespace"}},
	})
	if err != ErrThreadNotFound {
		t.Fatalf("append wrong namespace err=%v, want %v", err, ErrThreadNotFound)
	}
}

func TestListEventsEmpty(t *testing.T) {
	db := newTestDB(t)
	svc := NewEventLog(db, db, &stubIDGen{})
	requireThread(t, db, &model.TThread{
		ThreadId:     1,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	})

	events, nextCursor, _, hasMore, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  1,
	})
	if err != nil {
		t.Fatalf("list empty events: %v", err)
	}
	if len(events) != 0 || nextCursor != 0 || hasMore {
		t.Fatalf("unexpected empty list result: events=%v nextCursor=%d hasMore=%v", events, nextCursor, hasMore)
	}
}

func TestListEventsRejectsCursorModeMixing(t *testing.T) {
	db := newTestDB(t)
	svc := NewEventLog(db, db, &stubIDGen{})
	requireThread(t, db, &model.TThread{
		ThreadId:     1,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	})

	_, _, _, _, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  1,
		Cursor:    100,
		Order:     ListOrderCreatedAtInThreadSequence,
	})
	if err != ErrInvalidCursor {
		t.Fatalf("created seq with legacy cursor err=%v, want %v", err, ErrInvalidCursor)
	}

	_, _, _, _, err = svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace:  "dreamina",
		ThreadID:   1,
		PageCursor: "100:1:2",
		Order:      ListOrderEventID,
	})
	if err != ErrInvalidCursor {
		t.Fatalf("legacy order with page cursor err=%v, want %v", err, ErrInvalidCursor)
	}
}

func TestListEventsBackwardReturnsRecentPageInAscendingOrder(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 20, 30, 0, 0, time.UTC)
	svc := NewEventLog(db, db, &stubIDGen{ids: []int64{501, 502, 503, 504, 505}}, WithClock(func() time.Time { return now }))
	requireThread(t, db, &model.TThread{
		ThreadId:     5,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-5",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	_, _, err := svc.AppendEvents(context.Background(), AppendEventsRequest{
		Namespace: "dreamina",
		ThreadID:  5,
		TurnID:    "turn-recent",
		Events: []Event{
			{EventType: "step", Payload: []byte("one")},
			{EventType: "step", Payload: []byte("two")},
			{EventType: "step", Payload: []byte("three")},
			{EventType: "step", Payload: []byte("four")},
			{EventType: "step", Payload: []byte("five")},
		},
	})
	if err != nil {
		t.Fatalf("append events: %v", err)
	}

	recent, nextCursor, _, hasMore, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  5,
		Limit:     2,
		Direction: ListDirectionBackward,
	})
	if err != nil {
		t.Fatalf("list backward recent: %v", err)
	}
	if got := eventIDs(recent); !equalInt64s(got, []int64{504, 505}) {
		t.Fatalf("recent ids=%v, want [504 505]", got)
	}
	if nextCursor != 504 {
		t.Fatalf("recent next cursor=%d, want 504", nextCursor)
	}
	if !hasMore {
		t.Fatalf("recent hasMore should be true")
	}

	older, nextCursor, _, hasMore, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  5,
		Cursor:    nextCursor,
		Limit:     2,
		TurnID:    "turn-recent",
		Direction: ListDirectionBackward,
	})
	if err != nil {
		t.Fatalf("list backward older: %v", err)
	}
	if got := eventIDs(older); !equalInt64s(got, []int64{502, 503}) {
		t.Fatalf("older ids=%v, want [502 503]", got)
	}
	if nextCursor != 502 {
		t.Fatalf("older next cursor=%d, want 502", nextCursor)
	}
	if !hasMore {
		t.Fatalf("older hasMore should be true")
	}
}

func TestListEventsCreatedSeqOrderUsesCompositeCursor(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 20, 40, 0, 123000000, time.Local)
	svc := NewEventLog(db, db, &stubIDGen{})
	requireThread(t, db, &model.TThread{
		ThreadId:     8,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-8",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err := db.Create([]*model.TEventLog{
		{
			EventId:      300,
			Namespace:    "dreamina",
			SessionId:    "sess-8",
			ThreadId:     8,
			InThreadSeq:  2,
			TurnId:       "turn-a",
			EventType:    "step",
			MetadataJson: "{}",
			CreatedAt:    now,
		},
		{
			EventId:      100,
			Namespace:    "dreamina",
			SessionId:    "sess-8",
			ThreadId:     8,
			InThreadSeq:  1,
			TurnId:       "turn-a",
			EventType:    "step",
			MetadataJson: "{}",
			CreatedAt:    now,
		},
		{
			EventId:      150,
			Namespace:    "dreamina",
			SessionId:    "sess-8",
			ThreadId:     8,
			InThreadSeq:  1,
			TurnId:       "turn-a",
			EventType:    "step",
			MetadataJson: "{}",
			CreatedAt:    now,
		},
		{
			EventId:      200,
			Namespace:    "dreamina",
			SessionId:    "sess-8",
			ThreadId:     8,
			InThreadSeq:  0,
			TurnId:       "turn-b",
			EventType:    "step",
			MetadataJson: "{}",
			CreatedAt:    now.Add(time.Millisecond),
		},
	}).Error; err != nil {
		t.Fatalf("insert events: %v", err)
	}

	firstPage, _, pageCursor, hasMore, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  8,
		Limit:     2,
		Order:     ListOrderCreatedAtInThreadSequence,
	})
	if err != nil {
		t.Fatalf("list created seq first page: %v", err)
	}
	if got := eventIDs(firstPage); !equalInt64s(got, []int64{100, 150}) {
		t.Fatalf("first page ids=%v, want [100 150]", got)
	}
	if firstPage[0].InThreadSeq != 1 || firstPage[1].InThreadSeq != 1 {
		t.Fatalf("in_thread_seq not decoded: %+v", firstPage)
	}
	if pageCursor == "" || !hasMore {
		t.Fatalf("page cursor=%q hasMore=%v, want cursor and more", pageCursor, hasMore)
	}

	secondPage, _, _, hasMore, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace:  "dreamina",
		ThreadID:   8,
		Limit:      2,
		Order:      ListOrderCreatedAtInThreadSequence,
		PageCursor: pageCursor,
	})
	if err != nil {
		t.Fatalf("list created seq second page: %v", err)
	}
	if got := eventIDs(secondPage); !equalInt64s(got, []int64{300, 200}) {
		t.Fatalf("second page ids=%v, want [300 200]", got)
	}
	if hasMore {
		t.Fatalf("second page hasMore should be false")
	}

	turnEvents, _, _, _, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  8,
		TurnID:    "turn-a",
		Order:     ListOrderCreatedAtInThreadSequence,
	})
	if err != nil {
		t.Fatalf("list created seq turn events: %v", err)
	}
	if got := eventIDs(turnEvents); !equalInt64s(got, []int64{100, 150, 300}) {
		t.Fatalf("turn ids=%v, want [100 150 300]", got)
	}
}

func TestListEventsCreatedSeqBackwardReturnsRecentPageInAscendingOrder(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 20, 42, 0, 123000000, time.Local)
	svc := NewEventLog(db, db, &stubIDGen{})
	requireThread(t, db, &model.TThread{
		ThreadId:     9,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-9",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err := db.Create([]*model.TEventLog{
		{EventId: 300, Namespace: "dreamina", SessionId: "sess-9", ThreadId: 9, InThreadSeq: 2, TurnId: "turn-a", EventType: "step", MetadataJson: "{}", CreatedAt: now},
		{EventId: 100, Namespace: "dreamina", SessionId: "sess-9", ThreadId: 9, InThreadSeq: 1, TurnId: "turn-a", EventType: "step", MetadataJson: "{}", CreatedAt: now},
		{EventId: 150, Namespace: "dreamina", SessionId: "sess-9", ThreadId: 9, InThreadSeq: 1, TurnId: "turn-a", EventType: "step", MetadataJson: "{}", CreatedAt: now},
		{EventId: 200, Namespace: "dreamina", SessionId: "sess-9", ThreadId: 9, InThreadSeq: 0, TurnId: "turn-a", EventType: "step", MetadataJson: "{}", CreatedAt: now.Add(time.Millisecond)},
	}).Error; err != nil {
		t.Fatalf("insert events: %v", err)
	}

	recent, _, pageCursor, hasMore, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace: "dreamina",
		ThreadID:  9,
		Limit:     2,
		Direction: ListDirectionBackward,
		Order:     ListOrderCreatedAtInThreadSequence,
	})
	if err != nil {
		t.Fatalf("list created seq backward recent: %v", err)
	}
	if got := eventIDs(recent); !equalInt64s(got, []int64{300, 200}) {
		t.Fatalf("recent ids=%v, want [300 200]", got)
	}
	if pageCursor == "" || !hasMore {
		t.Fatalf("page cursor=%q hasMore=%v, want cursor and more", pageCursor, hasMore)
	}

	older, _, _, hasMore, err := svc.ListEvents(context.Background(), ListEventsRequest{
		Namespace:  "dreamina",
		ThreadID:   9,
		Limit:      2,
		Direction:  ListDirectionBackward,
		Order:      ListOrderCreatedAtInThreadSequence,
		PageCursor: pageCursor,
	})
	if err != nil {
		t.Fatalf("list created seq backward older: %v", err)
	}
	if got := eventIDs(older); !equalInt64s(got, []int64{100, 150}) {
		t.Fatalf("older ids=%v, want [100 150]", got)
	}
	if hasMore {
		t.Fatalf("older hasMore should be false")
	}
}

func TestListSessionEventsOrdersByCreatedAtThenEventID(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 20, 45, 0, 0, time.Local)
	svc := NewEventLog(db, db, &stubIDGen{})
	requireNamespace(t, db, "dreamina", now)
	requireThread(t, db, &model.TThread{
		ThreadId:     11,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-shared",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	requireThread(t, db, &model.TThread{
		ThreadId:     12,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-shared",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err := db.Create([]*model.TEventLog{
		{
			EventId:      300,
			Namespace:    "dreamina",
			SessionId:    "sess-shared",
			ThreadId:     11,
			TurnId:       "turn-a",
			EventType:    "step",
			Payload:      "first-by-time",
			MetadataJson: "{}",
			CreatedAt:    now,
		},
		{
			EventId:      100,
			Namespace:    "dreamina",
			SessionId:    "sess-shared",
			ThreadId:     12,
			TurnId:       "turn-b",
			EventType:    "step",
			Payload:      "second-by-time",
			MetadataJson: "{}",
			CreatedAt:    now.Add(time.Millisecond),
		},
		{
			EventId:      200,
			Namespace:    "dreamina",
			SessionId:    "sess-shared",
			ThreadId:     11,
			TurnId:       "turn-a",
			EventType:    "step",
			Payload:      "third-by-time",
			MetadataJson: "{}",
			CreatedAt:    now.Add(2 * time.Millisecond),
		},
	}).Error; err != nil {
		t.Fatalf("insert events: %v", err)
	}

	firstPage, cursor, hasMore, err := svc.ListSessionEvents(context.Background(), ListSessionEventsRequest{
		Namespace: "dreamina",
		SessionID: "sess-shared",
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("list session events first page: %v", err)
	}
	if got := eventIDs(firstPage); !equalInt64s(got, []int64{300, 100}) {
		t.Fatalf("first page ids=%v, want [300 100]", got)
	}
	if cursor == "" {
		t.Fatalf("cursor should be set")
	}
	if !hasMore {
		t.Fatalf("hasMore should be true")
	}

	secondPage, _, hasMore, err := svc.ListSessionEvents(context.Background(), ListSessionEventsRequest{
		Namespace: "dreamina",
		SessionID: "sess-shared",
		Cursor:    cursor,
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("list session events second page: %v", err)
	}
	if got := eventIDs(secondPage); !equalInt64s(got, []int64{200}) {
		t.Fatalf("second page ids=%v, want [200]", got)
	}
	if hasMore {
		t.Fatalf("second page hasMore should be false")
	}
}

func TestListSessionEventsUsesEventIDAsTieBreaker(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 20, 50, 0, 123000000, time.UTC)
	svc := NewEventLog(db, db, &stubIDGen{})
	requireNamespace(t, db, "dreamina", now)
	requireThread(t, db, &model.TThread{
		ThreadId:     21,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-tie",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err := db.Create([]*model.TEventLog{
		{
			EventId:      20,
			Namespace:    "dreamina",
			SessionId:    "sess-tie",
			ThreadId:     21,
			TurnId:       "turn-a",
			EventType:    "step",
			MetadataJson: "{}",
			CreatedAt:    now,
		},
		{
			EventId:      10,
			Namespace:    "dreamina",
			SessionId:    "sess-tie",
			ThreadId:     21,
			TurnId:       "turn-a",
			EventType:    "step",
			MetadataJson: "{}",
			CreatedAt:    now,
		},
	}).Error; err != nil {
		t.Fatalf("insert events: %v", err)
	}

	events, _, hasMore, err := svc.ListSessionEvents(context.Background(), ListSessionEventsRequest{
		Namespace: "dreamina",
		SessionID: "sess-tie",
	})
	if err != nil {
		t.Fatalf("list session events: %v", err)
	}
	if got := eventIDs(events); !equalInt64s(got, []int64{10, 20}) {
		t.Fatalf("ids=%v, want [10 20]", got)
	}
	if hasMore {
		t.Fatalf("hasMore should be false")
	}
}

func TestListSessionEventsRequiresRegisteredNamespace(t *testing.T) {
	db := newTestDB(t)
	svc := NewEventLog(db, db, &stubIDGen{})

	_, _, _, err := svc.ListSessionEvents(context.Background(), ListSessionEventsRequest{
		Namespace: "missing",
		SessionID: "sess-missing",
	})
	if err != ErrNamespaceNotFound {
		t.Fatalf("missing namespace err=%v, want %v", err, ErrNamespaceNotFound)
	}
}

func TestSessionEventCursorPreservesMicrosecondTimestamp(t *testing.T) {
	createdAt := time.Date(2026, 4, 13, 10, 11, 12, 123456000, time.FixedZone("source", 2*60*60))
	cursor := encodeSessionEventCursor(createdAt, 901)
	decoded, err := decodeSessionEventCursor(cursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if decoded.CreatedAt.UnixMicro() != createdAt.UnixMicro() {
		t.Fatalf("created_at_micros=%d, want %d", decoded.CreatedAt.UnixMicro(), createdAt.UnixMicro())
	}
	if decoded.CreatedAt.Location() != time.Local {
		t.Fatalf("created_at_location=%v, want %v", decoded.CreatedAt.Location(), time.Local)
	}
	if decoded.EventID != 901 {
		t.Fatalf("event_id=%d, want 901", decoded.EventID)
	}
}

func TestGetEventsByIDsPreservesRequestedOrder(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 21, 0, 0, 0, time.UTC)
	svc := NewEventLog(db, db, &stubIDGen{ids: []int64{301, 302}}, WithClock(func() time.Time { return now }))
	requireThread(t, db, &model.TThread{
		ThreadId:     7,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-7",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	appended, _, err := svc.AppendEvents(context.Background(), AppendEventsRequest{
		Namespace: "dreamina",
		ThreadID:  7,
		TurnID:    "turn-7",
		Events: []Event{
			{EventType: "a", Payload: []byte("one")},
			{EventType: "b", Payload: []byte("two")},
		},
	})
	if err != nil {
		t.Fatalf("append events: %v", err)
	}

	events, err := svc.GetEventsByIDs(context.Background(), []int64{appended[1].EventID, appended[0].EventID})
	if err != nil {
		t.Fatalf("get by ids: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	if events[0].EventID != appended[1].EventID || events[1].EventID != appended[0].EventID {
		t.Fatalf("events order mismatch: %+v", events)
	}
}

func TestAppendEventsRequiresTurnIDAndEventType(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 4, 13, 22, 0, 0, 0, time.UTC)
	svc := NewEventLog(db, db, &stubIDGen{ids: []int64{401, 402}}, WithClock(func() time.Time { return now }))
	requireThread(t, db, &model.TThread{
		ThreadId:     1,
		Namespace:    "dreamina",
		Env:          "ppe_a",
		SessionId:    "sess-1",
		Status:       model.ThreadStatusReady,
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	_, _, err := svc.AppendEvents(context.Background(), AppendEventsRequest{
		Namespace: "dreamina",
		ThreadID:  1,
		Events:    []Event{{EventType: "progress"}},
	})
	if err != ErrTurnIDRequired {
		t.Fatalf("missing turn id err=%v, want %v", err, ErrTurnIDRequired)
	}

	_, _, err = svc.AppendEvents(context.Background(), AppendEventsRequest{
		Namespace: "dreamina",
		ThreadID:  1,
		TurnID:    "turn-1",
		Events:    []Event{{}},
	})
	if err != ErrEventTypeRequired {
		t.Fatalf("missing event type err=%v, want %v", err, ErrEventTypeRequired)
	}
}

func requireThread(t *testing.T, db *gorm.DB, thread *model.TThread) {
	t.Helper()
	if err := db.Create(thread).Error; err != nil {
		t.Fatalf("insert thread: %v", err)
	}
}

func requireNamespace(t *testing.T, db *gorm.DB, namespace string, now time.Time) {
	t.Helper()
	if err := db.Create(&model.TAgentNamespace{
		Namespace:    namespace,
		Description:  "test namespace",
		CreatedBy:    "tester",
		MetadataJson: "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("insert namespace: %v", err)
	}
}

func eventIDs(events []Event) []int64 {
	ids := make([]int64, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID)
	}
	return ids
}

func equalInt64s(a []int64, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.TAgentNamespace{}, &model.TThread{}, &model.TEventLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

type stubIDGen struct {
	ids []int64
	idx int
}

func (s *stubIDGen) NextID(context.Context) (int64, error) {
	if s.idx >= len(s.ids) {
		return 0, nil
	}
	value := s.ids[s.idx]
	s.idx++
	return value, nil
}
