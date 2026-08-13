package runtime

import (
	"context"
	"sort"
	"sync"
)

type MemoryThreadIndex struct {
	mu      sync.RWMutex
	entries map[string]ThreadIndexEntry
}

func NewMemoryThreadIndex() (index *MemoryThreadIndex) {
	index = &MemoryThreadIndex{entries: make(map[string]ThreadIndexEntry)}
	return index
}

func (index *MemoryThreadIndex) Put(ctx context.Context, entry ThreadIndexEntry) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = entry.Ref.Validate(); err != nil {
		return &Error{Code: ErrorCodeInvalidArgument, Op: "thread_index.put", Runtime: entry.Ref.Runtime, Cause: err}
	}
	if entry.SchemaVersion == 0 {
		entry.SchemaVersion = ThreadIndexSchemaVersion
	}
	if entry.SchemaVersion != ThreadIndexSchemaVersion {
		return &Error{Code: ErrorCodeInvalidArgument, Op: "thread_index.put", Runtime: entry.Ref.Runtime, Message: "unsupported schema version"}
	}
	key, err := threadIndexKey(entry.Ref)
	if err != nil {
		return err
	}
	index.mu.Lock()
	index.entries[key] = entry
	index.mu.Unlock()
	return nil
}

func (index *MemoryThreadIndex) Get(ctx context.Context, ref GlobalThreadRef) (entry ThreadIndexEntry, err error) {
	if err = ctx.Err(); err != nil {
		return entry, err
	}
	key, err := threadIndexKey(ref)
	if err != nil {
		return entry, err
	}
	index.mu.RLock()
	entry, ok := index.entries[key]
	index.mu.RUnlock()
	if !ok {
		return entry, &Error{Code: ErrorCodeNotFound, Op: "thread_index.get", Runtime: ref.Runtime}
	}
	return entry, nil
}

func (index *MemoryThreadIndex) List(ctx context.Context, query ThreadIndexQuery) (entries []ThreadIndexEntry, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	index.mu.RLock()
	entries = make([]ThreadIndexEntry, 0, len(index.entries))
	for _, entry := range index.entries {
		if query.Runtime != "" && entry.Ref.Runtime != query.Runtime {
			continue
		}
		if query.Namespace != "" && entry.Ref.Namespace != query.Namespace {
			continue
		}
		entries = append(entries, entry)
	}
	index.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAtMS != entries[j].UpdatedAtMS {
			return entries[i].UpdatedAtMS > entries[j].UpdatedAtMS
		}
		left, _ := entries[i].Ref.MarshalText()
		right, _ := entries[j].Ref.MarshalText()
		return string(left) < string(right)
	})
	return entries, nil
}

func threadIndexKey(ref GlobalThreadRef) (key string, err error) {
	encoded, err := ref.MarshalText()
	if err != nil {
		return "", &Error{Code: ErrorCodeInvalidArgument, Op: "thread_index.key", Runtime: ref.Runtime, Cause: err}
	}
	key = string(encoded)
	return key, nil
}
