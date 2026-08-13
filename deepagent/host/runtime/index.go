package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	sdkruntime "eino-cli/deepagent/runtime"
)

const indexFileSchemaVersion = 1

type PersistentThreadIndex struct {
	mu      sync.RWMutex
	path    string
	entries map[string]sdkruntime.ThreadIndexEntry
}

type indexFile struct {
	SchemaVersion int               `json:"schema_version"`
	Entries       []json.RawMessage `json:"entries"`
}

func OpenPersistentThreadIndex(path string) (index *PersistentThreadIndex, err error) {
	if path == "" {
		return nil, fmt.Errorf("thread index path is required")
	}
	index = &PersistentThreadIndex{path: path, entries: make(map[string]sdkruntime.ThreadIndexEntry)}
	if err = index.load(); err != nil {
		return nil, err
	}
	return index, nil
}

func (index *PersistentThreadIndex) Put(ctx context.Context, entry sdkruntime.ThreadIndexEntry) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	key, err := indexKey(entry.Ref)
	if err != nil {
		return err
	}
	entry.SchemaVersion = sdkruntime.ThreadIndexSchemaVersion
	index.mu.Lock()
	previous, existed := index.entries[key]
	index.entries[key] = entry
	if err = index.persistLocked(); err != nil {
		if existed {
			index.entries[key] = previous
		} else {
			delete(index.entries, key)
		}
	}
	index.mu.Unlock()
	return err
}

func (index *PersistentThreadIndex) Get(ctx context.Context, ref sdkruntime.GlobalThreadRef) (entry sdkruntime.ThreadIndexEntry, err error) {
	if err = ctx.Err(); err != nil {
		return entry, err
	}
	key, err := indexKey(ref)
	if err != nil {
		return entry, err
	}
	index.mu.RLock()
	entry, ok := index.entries[key]
	index.mu.RUnlock()
	if !ok {
		return entry, &sdkruntime.Error{Code: sdkruntime.ErrorCodeNotFound, Op: "sgadk_thread_index.get", Runtime: ref.Runtime}
	}
	return entry, nil
}

func (index *PersistentThreadIndex) List(ctx context.Context, query sdkruntime.ThreadIndexQuery) (entries []sdkruntime.ThreadIndexEntry, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	index.mu.RLock()
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

func (index *PersistentThreadIndex) load() (err error) {
	data, err := os.ReadFile(index.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file indexFile
	if err = json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("decode thread index: %w", err)
	}
	if file.SchemaVersion != 0 && file.SchemaVersion != indexFileSchemaVersion {
		return fmt.Errorf("unsupported thread index schema %d", file.SchemaVersion)
	}
	for _, raw := range file.Entries {
		entry, decodeErr := decodeIndexEntry(raw)
		if decodeErr != nil {
			continue
		}
		key, keyErr := indexKey(entry.Ref)
		if keyErr != nil {
			continue
		}
		entry.SchemaVersion = sdkruntime.ThreadIndexSchemaVersion
		index.entries[key] = entry
	}
	return nil
}

type legacyIndexEntry struct {
	SchemaVersion int `json:"schema_version"`
	RawRef        struct {
		Runtime   sdkruntime.RuntimeKind `json:"runtime"`
		Namespace string                 `json:"namespace,omitempty"`
		ThreadID  string                 `json:"thread_id"`
	} `json:"ref"`
	DefinitionName    string                   `json:"definition_name"`
	DefinitionVersion string                   `json:"definition_version"`
	Workspace         sdkruntime.WorkspaceSpec `json:"workspace,omitempty"`
	Title             string                   `json:"title,omitempty"`
	State             sdkruntime.ThreadState   `json:"state,omitempty"`
	CreatedAtMS       int64                    `json:"created_at_ms,omitempty"`
	UpdatedAtMS       int64                    `json:"updated_at_ms,omitempty"`
	TimelineCursor    string                   `json:"timeline_cursor,omitempty"`
}

func decodeIndexEntry(raw json.RawMessage) (entry sdkruntime.ThreadIndexEntry, err error) {
	if err = json.Unmarshal(raw, &entry); err == nil {
		return entry, nil
	}
	var legacy legacyIndexEntry
	if err = json.Unmarshal(raw, &legacy); err != nil {
		return entry, err
	}
	entry = sdkruntime.ThreadIndexEntry{
		SchemaVersion:  legacy.SchemaVersion,
		Ref:            sdkruntime.GlobalThreadRef{Runtime: legacy.RawRef.Runtime, Namespace: legacy.RawRef.Namespace, ThreadID: legacy.RawRef.ThreadID},
		DefinitionName: legacy.DefinitionName, DefinitionVersion: legacy.DefinitionVersion,
		Workspace: legacy.Workspace, Title: legacy.Title, State: legacy.State,
		CreatedAtMS: legacy.CreatedAtMS, UpdatedAtMS: legacy.UpdatedAtMS, TimelineCursor: legacy.TimelineCursor,
	}
	return entry, nil
}

func (index *PersistentThreadIndex) persistLocked() (err error) {
	entries := make([]sdkruntime.ThreadIndexEntry, 0, len(index.entries))
	for _, entry := range index.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		left, _ := entries[i].Ref.MarshalText()
		right, _ := entries[j].Ref.MarshalText()
		return string(left) < string(right)
	})
	file := indexFile{SchemaVersion: indexFileSchemaVersion, Entries: make([]json.RawMessage, 0, len(entries))}
	for _, entry := range entries {
		encoded, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			return marshalErr
		}
		file.Entries = append(file.Entries, encoded)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(index.path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(index.path), ".thread-index-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	err = os.Rename(temporaryPath, index.path)
	return err
}

func indexKey(ref sdkruntime.GlobalThreadRef) (key string, err error) {
	encoded, err := ref.MarshalText()
	if err != nil {
		return "", &sdkruntime.Error{Code: sdkruntime.ErrorCodeInvalidArgument, Op: "sgadk_thread_index.key", Runtime: ref.Runtime, Cause: err}
	}
	key = string(encoded)
	return key, nil
}

var _ sdkruntime.ThreadIndex = (*PersistentThreadIndex)(nil)
