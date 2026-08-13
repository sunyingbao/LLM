package idgen

import (
	"context"
	"testing"
)

func TestNewGeneratorFallsBackToLocalWhenNamespaceEmpty(t *testing.T) {
	gen, err := NewGenerator("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gen.(*LocalGenerator); !ok {
		t.Fatalf("generator type = %T, want *LocalGenerator", gen)
	}
}

type fakeNTClient struct {
	id  uint64
	err error
}

func (f fakeNTClient) Get(context.Context) (uint64, error) {
	return f.id, f.err
}

func TestNTGeneratorUsesClientID(t *testing.T) {
	gen := &NTGenerator{client: fakeNTClient{id: 67890}}
	id, err := gen.SessionID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id != 67890 {
		t.Fatalf("id = %d, want 67890", id)
	}
}
