package idgen

import (
	"context"
	"testing"
	"time"
)

func TestLocalIDGeneratorUsesCallTimeNotConstructionTime(t *testing.T) {
	ctx := context.Background()
	oldWorkerGen := NewLocalIDGenerator()
	time.Sleep(2 * time.Millisecond)
	newWorkerGen := NewLocalIDGenerator()

	newWorkerID := newWorkerGen.MessageID(ctx, "thread", "turn")
	time.Sleep(2 * time.Millisecond)
	oldWorkerLaterID := oldWorkerGen.MessageID(ctx, "thread", "turn")

	if oldWorkerLaterID <= newWorkerID {
		t.Fatalf("later id from older generator = %d, want > newer generator id %d", oldWorkerLaterID, newWorkerID)
	}
}

func TestLocalIDGeneratorMonotonicWithinProcess(t *testing.T) {
	ctx := context.Background()
	gen := NewLocalIDGenerator()
	prev := gen.MessageID(ctx, "thread", "turn")
	for i := 0; i < 1000; i++ {
		next := gen.MessageID(ctx, "thread", "turn")
		if next <= prev {
			t.Fatalf("id not monotonic at %d: prev=%d next=%d", i, prev, next)
		}
		prev = next
	}
}

func TestLocalIDGeneratorNextID(t *testing.T) {
	ctx := context.Background()
	gen := NewLocalIDGenerator()
	first, err := gen.NextID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gen.NextID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second <= first {
		t.Fatalf("NextID not monotonic: first=%d second=%d", first, second)
	}
}

func TestNewGeneratorFallsBackToLocalWhenNamespaceEmpty(t *testing.T) {
	gen, err := NewGenerator("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gen.(*LocalIDGenerator); !ok {
		t.Fatalf("generator type = %T, want *LocalIDGenerator", gen)
	}
}

type fakeNTClient struct {
	id  uint64
	err error
}

func (f fakeNTClient) Get(context.Context) (uint64, error) {
	return f.id, f.err
}

func TestNTIDGeneratorUsesClientID(t *testing.T) {
	gen := &NTIDGenerator{client: fakeNTClient{id: 12345}}
	id, err := gen.NextID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id != 12345 {
		t.Fatalf("id = %d, want 12345", id)
	}
}
