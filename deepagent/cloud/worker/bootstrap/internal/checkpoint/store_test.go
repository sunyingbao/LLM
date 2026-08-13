package checkpoint

import (
	"context"
	"testing"
)

type fakeRedis struct {
	values map[string][]byte
	ints   map[string]int64
}

func (f *fakeRedis) Get(_ context.Context, key string) ([]byte, bool, error) {
	if f.values == nil {
		return nil, false, nil
	}
	value, ok := f.values[key]
	return value, ok, nil
}

func (f *fakeRedis) Set(_ context.Context, key string, value []byte) error {
	if f.values == nil {
		f.values = map[string][]byte{}
	}
	f.values[key] = append([]byte(nil), value...)
	return nil
}

func (f *fakeRedis) Del(_ context.Context, keys ...string) (int64, error) {
	var deleted int64
	for _, key := range keys {
		if _, ok := f.values[key]; ok {
			delete(f.values, key)
			deleted++
		}
	}
	return deleted, nil
}

func (f *fakeRedis) IncrBy(_ context.Context, key string, value int64) (int64, error) {
	if f.ints == nil {
		f.ints = map[string]int64{}
	}
	f.ints[key] += value
	return f.ints[key], nil
}

func TestRedisStoreRoundTrip(t *testing.T) {
	store := NewRedisStore(&fakeRedis{}, "ckpt", "thread-1")
	if err := store.Set(context.Background(), "turn-1", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || string(got) != "payload" {
		t.Fatalf("checkpoint = %q ok=%v", got, ok)
	}
}
