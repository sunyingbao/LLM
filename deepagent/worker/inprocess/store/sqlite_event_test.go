package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
)

func TestSQLiteEventStoreAppendAndList(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteEventStore(filepath.Join(t.TempDir(), "events.sqlite"), "")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}

	base := time.Unix(100, 0)
	events := []*agentworker.Event{
		{ID: "ev_1", ThreadID: "thread_1", TurnID: "turn_1", Type: "token", Payload: []byte("one"), Metadata: map[string]string{"seq": "1"}, TS: base},
		{ID: "ev_2", ThreadID: "thread_1", TurnID: "turn_1", Type: "done", Payload: []byte("two"), Metadata: map[string]string{"seq": "2"}, TS: base.Add(time.Second)},
		{ID: "ev_other", ThreadID: "thread_2", TurnID: "turn_2", Type: "done", Payload: []byte("other"), TS: base.Add(2 * time.Second)},
	}
	for _, ev := range events {
		if err := store.AppendEvent(ctx, ev); err != nil {
			t.Fatalf("AppendEvent(%s) error = %v", ev.ID, err)
		}
	}

	got, err := store.ListEvents(ctx, "thread_1", inprocess.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ID != "ev_1" || got[1].ID != "ev_2" {
		t.Fatalf("event order = [%s %s], want [ev_1 ev_2]", got[0].ID, got[1].ID)
	}
	if string(got[0].Payload) != "one" || got[0].Metadata["seq"] != "1" {
		t.Fatalf("event payload/metadata not preserved: %+v", got[0])
	}

	paged, err := store.ListEvents(ctx, "thread_1", inprocess.ListEventsOptions{Cursor: "1", Limit: 1})
	if err != nil {
		t.Fatalf("ListEvents(cursor) error = %v", err)
	}
	if len(paged) != 1 || paged[0].ID != "ev_2" {
		t.Fatalf("paged = %+v, want ev_2", paged)
	}

	reversed, err := store.ListEvents(ctx, "thread_1", inprocess.ListEventsOptions{Reverse: true})
	if err != nil {
		t.Fatalf("ListEvents(reverse) error = %v", err)
	}
	if len(reversed) != 2 || reversed[0].ID != "ev_2" || reversed[1].ID != "ev_1" {
		t.Fatalf("reverse order = %+v, want ev_2 then ev_1", reversed)
	}
}
