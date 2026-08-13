package session

import (
	"context"
	"errors"
	"regexp"
	"testing"

	coordinator "code.byted.org/overpass/ad_creative_aic_agent_coordinator/kitex_gen/agent_coordinator"
	sessiondal "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/dal/session"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_session/infra/idgen"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestCloseProjectClosesActiveSessionsAndThreads(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	svc, err := New(store, idgen.NewLocalGenerator(), &fakeAC{
		threads: map[int64][]*coordinator.Thread{
			101: {
				{ThreadId: 1001, Status: coordinator.ThreadStatus_RUNNING},
				{ThreadId: 1002, Status: coordinator.ThreadStatus_CLOSED},
			},
			102: {
				{ThreadId: 1003, Status: coordinator.ThreadStatus_BLOCKED},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, uid, project_name, project_path, title, status, main_thread_id, last_message_preview, last_active_at_ms, created_at_ms, updated_at_ms, closed_at_ms, COALESCE(metadata_json, '') FROM t_agent_session")).
		WithArgs(int64(7), "proj", sessiondal.StatusActive, maxCloseProjectRows+1).
		WillReturnRows(sessionRows(
			&sessiondal.Session{SessionID: 101, UID: 7, ProjectName: "proj", ProjectPath: "/workspace/proj", Status: sessiondal.StatusActive, LastActiveAtMS: 300, CreatedAtMS: 100, UpdatedAtMS: 300, MetadataJSON: "{}"},
			&sessiondal.Session{SessionID: 102, UID: 7, ProjectName: "proj", ProjectPath: "/workspace/proj", Status: sessiondal.StatusActive, LastActiveAtMS: 200, CreatedAtMS: 100, UpdatedAtMS: 200, MetadataJSON: "{}"},
		))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_agent_session")).
		WithArgs(sessiondal.StatusClosed, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7), "proj", sessiondal.StatusActive, int64(101), int64(102)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	result, err := svc.CloseProject(ctx, 7, "proj", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.ClosedSessionCount != 2 {
		t.Fatalf("ClosedSessionCount=%d, want 2", result.ClosedSessionCount)
	}
	if result.ClosedThreadCount != 2 {
		t.Fatalf("ClosedThreadCount=%d, want 2", result.ClosedThreadCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseProjectACFailureDoesNotHideSessions(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	svc, err := New(store, idgen.NewLocalGenerator(), &fakeAC{failSessionID: 102})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, uid, project_name, project_path, title, status, main_thread_id, last_message_preview, last_active_at_ms, created_at_ms, updated_at_ms, closed_at_ms, COALESCE(metadata_json, '') FROM t_agent_session")).
		WithArgs(int64(7), "proj", sessiondal.StatusActive, maxCloseProjectRows+1).
		WillReturnRows(sessionRows(
			&sessiondal.Session{SessionID: 101, UID: 7, ProjectName: "proj", ProjectPath: "/workspace/proj", Status: sessiondal.StatusActive, LastActiveAtMS: 300, CreatedAtMS: 100, UpdatedAtMS: 300, MetadataJSON: "{}"},
			&sessiondal.Session{SessionID: 102, UID: 7, ProjectName: "proj", ProjectPath: "/workspace/proj", Status: sessiondal.StatusActive, LastActiveAtMS: 200, CreatedAtMS: 100, UpdatedAtMS: 200, MetadataJSON: "{}"},
		))

	_, err = svc.CloseProject(ctx, 7, "proj", "test")
	if err == nil {
		t.Fatal("CloseProject() error=nil, want AC failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type fakeAC struct {
	threads       map[int64][]*coordinator.Thread
	failSessionID int64
}

func (f *fakeAC) ListSessionThreads(context.Context, int64) ([]*coordinator.Thread, error) {
	return nil, nil
}

func (f *fakeAC) CloseSessionThreads(_ context.Context, sessionID int64, _ string) ([]*coordinator.Thread, error) {
	if f.failSessionID == sessionID {
		return nil, errors.New("close threads failed")
	}
	return f.threads[sessionID], nil
}

func newMockStore(t *testing.T) (*sessiondal.Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := sessiondal.NewStore(db, "t_agent_session")
	if err != nil {
		t.Fatal(err)
	}
	return store, mock
}

func sessionRows(sessions ...*sessiondal.Session) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{
		"session_id", "uid", "project_name", "project_path", "title", "status", "main_thread_id",
		"last_message_preview", "last_active_at_ms", "created_at_ms", "updated_at_ms", "closed_at_ms", "metadata_json",
	})
	for _, sess := range sessions {
		rows.AddRow(
			sess.SessionID,
			sess.UID,
			sess.ProjectName,
			sess.ProjectPath,
			sess.Title,
			sess.Status,
			sess.MainThreadID,
			sess.LastMessagePreview,
			sess.LastActiveAtMS,
			sess.CreatedAtMS,
			sess.UpdatedAtMS,
			sess.ClosedAtMS,
			sess.MetadataJSON,
		)
	}
	return rows
}
