package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"time"

	sdkruntime "eino-cli/deepagent/runtime"
	"eino-cli/deepagent/worker"
	"eino-cli/deepagent/worker/inprocess"
	inprocessstore "eino-cli/deepagent/worker/inprocess/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	legacyimport "eino-cli/deepagent/migration/legacy"
)

type LegacyDestination struct {
	threads       *inprocessstore.SQLiteThreadStateStore
	events        *inprocessstore.SQLiteEventStore
	index         sdkruntime.ThreadIndex
	workspaceRoot string
}

func NewLegacyDestination(ctx context.Context, databasePath, workspaceRoot string, index sdkruntime.ThreadIndex) (destination *LegacyDestination, err error) {
	database, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	destination = &LegacyDestination{threads: inprocessstore.NewSQLiteThreadStateStore(database, ""), events: inprocessstore.NewSQLiteEventStore(database, ""), index: index, workspaceRoot: workspaceRoot}
	if err = destination.threads.AutoMigrate(ctx); err != nil {
		return nil, err
	}
	if err = destination.events.AutoMigrate(ctx); err != nil {
		return nil, err
	}
	return destination, nil
}

func (destination *LegacyDestination) ImportLegacyThread(ctx context.Context, source legacyimport.Thread) (created bool, err error) {
	threadID := legacyThreadID(source.SourceSessionID)
	_, getErr := destination.threads.GetThread(ctx, threadID)
	if getErr != nil && !errors.Is(getErr, inprocess.ErrThreadNotFound) {
		return false, getErr
	}
	created = errors.Is(getErr, inprocess.ErrThreadNotFound)
	if created {
		_, err = destination.threads.CreateThread(ctx, inprocess.CreateThreadSpec{
			ID: threadID, UserID: 1, SessionID: "legacy:" + source.SourceSessionID, Title: source.Title,
			Profile:  inprocess.ThreadProfile{Cwd: destination.workspaceRoot},
			Metadata: map[string]string{"runtime.definition.name": "sgadk-legacy-import", "runtime.definition.version": "v1", "legacy.source.session_id": source.SourceSessionID, "legacy.source.fingerprint": source.Fingerprint},
		})
		if err != nil {
			return false, err
		}
	}
	existing, err := destination.events.ListEvents(ctx, threadID, inprocess.ListEventsOptions{Limit: 100000})
	if err != nil {
		return false, err
	}
	existingIDs := make(map[string]struct{}, len(existing))
	for _, event := range existing {
		if event != nil {
			existingIDs[event.ID] = struct{}{}
		}
	}
	for _, event := range source.Events {
		if _, exists := existingIDs[event.EventID]; exists {
			continue
		}
		if err = destination.events.AppendEvent(ctx, &agentworker.Event{ID: event.EventID, ThreadID: threadID, TurnID: event.TurnID, Type: agentworker.EventType(event.EventType), Payload: event.Payload, TS: time.UnixMilli(event.CreatedAtMs)}); err != nil {
			return false, err
		}
	}
	if source.Interrupted {
		closedAt := source.UpdatedAt
		if closedAt.IsZero() {
			closedAt = time.Now()
		}
		if _, err = destination.threads.UpdateThread(ctx, threadID, inprocess.UpdateThreadStatePatch{ClosedAt: &closedAt}); err != nil {
			return false, err
		}
	}
	if destination.index != nil {
		ref := sdkruntime.GlobalThreadRef{Runtime: sdkruntime.RuntimeLocal, Namespace: "legacy:" + source.SourceSessionID, ThreadID: threadID}
		err = destination.index.Put(ctx, sdkruntime.ThreadIndexEntry{SchemaVersion: sdkruntime.ThreadIndexSchemaVersion, Ref: ref, DefinitionName: "sgadk-legacy-import", DefinitionVersion: "v1", Workspace: sdkruntime.WorkspaceSpec{Cwd: destination.workspaceRoot}, Title: source.Title, State: importedThreadState(source), UpdatedAtMS: source.UpdatedAt.UnixMilli()})
		if err != nil {
			return false, err
		}
	}
	return created, nil
}

func legacyThreadID(sessionID string) (threadID string) {
	sum := sha256.Sum256([]byte(sessionID))
	return "legacy-" + hex.EncodeToString(sum[:12])
}

func importedThreadState(source legacyimport.Thread) (state sdkruntime.ThreadState) {
	if source.Interrupted {
		return sdkruntime.ThreadStateInterrupted
	}
	return sdkruntime.ThreadStateIdle
}

func LegacyRuntimePaths(runtimeRoot string) (databasePath, indexPath string) {
	return filepath.Join(runtimeRoot, "local.db"), filepath.Join(runtimeRoot, "thread-index.json")
}

var _ legacyimport.Destination = (*LegacyDestination)(nil)
