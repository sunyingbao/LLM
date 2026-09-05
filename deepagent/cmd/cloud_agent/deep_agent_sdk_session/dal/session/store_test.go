package session

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (store *Store, mock sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	})
	store, err = NewStore(db, "agent_sessions")
	require.NoError(t, err)
	return store, mock
}

func rowsForSessions(sessions ...*Session) (rows *sqlmock.Rows) {
	rows = sqlmock.NewRows([]string{
		"session_id", "uid", "project_name", "project_path", "title", "status", "main_thread_id",
		"last_message_preview", "last_active_at_ms", "created_at_ms", "updated_at_ms", "closed_at_ms", "metadata_json",
	})
	for _, session := range sessions {
		rows.AddRow(
			session.SessionID, session.UID, session.ProjectName, session.ProjectPath, session.Title,
			session.Status, session.MainThreadID, session.LastMessagePreview, session.LastActiveAtMS,
			session.CreatedAtMS, session.UpdatedAtMS, session.ClosedAtMS, session.MetadataJSON,
		)
	}
	return rows
}

func testSession(status int64) (session *Session) {
	return &Session{
		SessionID: 11, UID: 7, ProjectName: "project", ProjectPath: "/project", Title: "title",
		Status: status, MainThreadID: 21, LastMessagePreview: "preview", LastActiveAtMS: 300,
		CreatedAtMS: 100, UpdatedAtMS: 200, MetadataJSON: "{}",
	}
}

func TestNewStoreAndCreate(t *testing.T) {
	_, err := NewStore(nil, "sessions")
	require.ErrorContains(t, err, "mysql db is required")
	db, standaloneMock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		standaloneMock.ExpectClose()
		require.NoError(t, db.Close())
	})
	_, err = NewStore(db, "invalid-table")
	require.ErrorContains(t, err, "invalid agent session table")

	store, mock := newTestStore(t)
	require.ErrorContains(t, store.Create(context.Background(), nil), "session is required")
	mock.ExpectExec("INSERT INTO agent_sessions").
		WithArgs(int64(11), int64(7), "project", "/project", "title", StatusActive, int64(21), "preview", int64(300), int64(100), int64(200), int64(0), "{}").
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, store.Create(context.Background(), testSession(StatusActive)))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAndScanSession(t *testing.T) {
	ctx := context.Background()
	store, mock := newTestStore(t)
	want := testSession(StatusActive)
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(want))
	got, err := store.Get(ctx, 7, 11)
	require.NoError(t, err)
	require.Equal(t, want, got)

	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(99)).WillReturnError(sql.ErrNoRows)
	_, err = store.Get(ctx, 7, 99)
	require.ErrorIs(t, err, ErrNotFound)

	wantErr := errors.New("query failed")
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(98)).WillReturnError(wantErr)
	_, err = store.Get(ctx, 7, 98)
	require.ErrorIs(t, err, wantErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessionsAndProjects(t *testing.T) {
	ctx := context.Background()
	store, mock := newTestStore(t)
	active := StatusActive
	cursor := &Cursor{LastActiveAtMS: 500, UpdatedAtMS: 400, SessionID: 30}
	first := testSession(StatusActive)
	second := *first
	second.SessionID = 10
	second.LastActiveAtMS = 250
	second.UpdatedAtMS = 150

	mock.ExpectQuery("SELECT session_id").
		WithArgs(int64(7), StatusActive, "project", int64(500), int64(500), int64(400), int64(500), int64(400), int64(30), 2).
		WillReturnRows(rowsForSessions(first, &second))
	sessions, next, err := store.List(ctx, ListFilter{UID: 7, Status: &active, ProjectName: " project ", Cursor: cursor, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []*Session{first}, sessions)
	require.Equal(t, &Cursor{LastActiveAtMS: first.LastActiveAtMS, UpdatedAtMS: first.UpdatedAtMS, SessionID: first.SessionID}, next)

	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), 21).WillReturnRows(rowsForSessions())
	sessions, next, err = store.List(ctx, ListFilter{UID: 7})
	require.NoError(t, err)
	require.Empty(t, sessions)
	require.Nil(t, next)

	mock.ExpectQuery("SELECT project_name").WithArgs(int64(7), StatusClosed).
		WillReturnRows(sqlmock.NewRows([]string{"project_name", "project_path", "count", "active"}).AddRow("project", "/project", 2, 300))
	projects, err := store.ListProjects(ctx, 7, nil)
	require.NoError(t, err)
	require.Equal(t, []*SessionProject{{ProjectName: "project", ProjectPath: "/project", SessionCount: 2, LastActiveAtMS: 300}}, projects)

	mock.ExpectQuery("SELECT project_name").WithArgs(int64(7), StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"project_name", "project_path", "count", "active"}))
	projects, err = store.ListProjects(ctx, 7, &active)
	require.NoError(t, err)
	require.Empty(t, projects)

	sessions, err = store.ListProjectSessions(ctx, 7, "project", StatusActive, 0)
	require.NoError(t, err)
	require.Nil(t, sessions)
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), "project", StatusActive, 2).WillReturnRows(rowsForSessions(first))
	sessions, err = store.ListProjectSessions(ctx, 7, " project ", StatusActive, 2)
	require.NoError(t, err)
	require.Equal(t, []*Session{first}, sessions)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateCloseAndBind(t *testing.T) {
	ctx := context.Background()
	store, mock := newTestStore(t)
	active := testSession(StatusActive)
	updated := *active
	updated.Title = "updated"
	title := "updated"
	status := StatusArchived

	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(active))
	mock.ExpectExec("UPDATE agent_sessions SET updated_at_ms").WithArgs(int64(400), title, status, int64(7), int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(&updated))
	got, err := store.Update(ctx, 7, 11, UpdatePatch{Title: &title, Status: &status, UpdatedAt: 400})
	require.NoError(t, err)
	require.Equal(t, &updated, got)

	closed := testSession(StatusClosed)
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(closed))
	_, err = store.Update(ctx, 7, 11, UpdatePatch{UpdatedAt: 500})
	require.ErrorIs(t, err, ErrClosed)

	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(active))
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(StatusClosed, int64(600), int64(600), int64(600), int64(7), int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(closed))
	got, err = store.Close(ctx, 7, 11, 600)
	require.NoError(t, err)
	require.Equal(t, closed, got)

	require.NoError(t, store.CloseProjectSessions(ctx, 7, "project", nil, 700))
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(StatusClosed, int64(700), int64(700), int64(700), int64(7), "project", StatusActive, int64(11), int64(12)).WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, store.CloseProjectSessions(ctx, 7, " project ", []int64{11, 12}, 700))

	_, err = store.BindMainThread(ctx, 7, 11, 0, 800)
	require.ErrorContains(t, err, "main_thread_id is required")
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(21), int64(800), int64(7), int64(11), StatusClosed, int64(21)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(active))
	got, err = store.BindMainThread(ctx, 7, 11, 21, 800)
	require.NoError(t, err)
	require.Equal(t, active, got)

	busy := *active
	busy.MainThreadID = 22
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(21), int64(800), int64(7), int64(11), StatusClosed, int64(21)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(&busy))
	_, err = store.BindMainThread(ctx, 7, 11, 21, 800)
	require.ErrorIs(t, err, ErrMainThreadBusy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTouchAndCursors(t *testing.T) {
	ctx := context.Background()
	store, mock := newTestStore(t)
	active := testSession(StatusActive)
	preview := "new preview"
	title := "new title"
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(900), int64(901), preview, title, int64(7), int64(11), StatusClosed).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(active))
	got, err := store.Touch(ctx, 7, 11, TouchPatch{LastMessagePreview: &preview, TitleIfEmpty: &title, LastActiveAtMS: 900, UpdatedAtMS: 901})
	require.NoError(t, err)
	require.Equal(t, active, got)

	closed := testSession(StatusClosed)
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(900), int64(901), int64(7), int64(11), StatusClosed).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(closed))
	_, err = store.Touch(ctx, 7, 11, TouchPatch{LastActiveAtMS: 900, UpdatedAtMS: 901})
	require.ErrorIs(t, err, ErrClosed)

	require.Empty(t, EncodeCursor(nil))
	wantCursor := &Cursor{LastActiveAtMS: 3, UpdatedAtMS: 2, SessionID: 1}
	raw := EncodeCursor(wantCursor)
	require.NotEmpty(t, raw)
	cursor, err := DecodeCursor(raw)
	require.NoError(t, err)
	require.Equal(t, wantCursor, cursor)
	cursor, err = DecodeCursor(" ")
	require.NoError(t, err)
	require.Nil(t, cursor)
	_, err = DecodeCursor("%%")
	require.ErrorContains(t, err, "decode cursor")
	_, err = DecodeCursor("e30")
	require.ErrorContains(t, err, "invalid cursor")
	_, err = DecodeCursor("ew")
	require.ErrorContains(t, err, "parse cursor")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	store, mock := newTestStore(t)
	wantErr := errors.New("database failed")
	active := testSession(StatusActive)

	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), 101).WillReturnError(wantErr)
	_, _, err := store.List(ctx, ListFilter{UID: 7, Limit: 1000})
	require.ErrorIs(t, err, wantErr)

	badRows := rowsForSessions(active)
	badRows = sqlmock.NewRows([]string{
		"session_id", "uid", "project_name", "project_path", "title", "status", "main_thread_id",
		"last_message_preview", "last_active_at_ms", "created_at_ms", "updated_at_ms", "closed_at_ms", "metadata_json",
	}).AddRow("invalid", 7, "project", "/project", "title", 1, 0, "", 1, 1, 1, 0, "{}")
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), 2).WillReturnRows(badRows)
	_, _, err = store.List(ctx, ListFilter{UID: 7, Limit: 1})
	require.Error(t, err)

	rowErrorRows := rowsForSessions(active).RowError(0, wantErr)
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), 2).WillReturnRows(rowErrorRows)
	_, _, err = store.List(ctx, ListFilter{UID: 7, Limit: 1})
	require.ErrorIs(t, err, wantErr)

	mock.ExpectQuery("SELECT project_name").WithArgs(int64(7), StatusClosed).WillReturnError(wantErr)
	_, err = store.ListProjects(ctx, 7, nil)
	require.ErrorIs(t, err, wantErr)
	mock.ExpectQuery("SELECT project_name").WithArgs(int64(7), StatusClosed).
		WillReturnRows(sqlmock.NewRows([]string{"project_name", "project_path", "count", "active"}).AddRow("project", "/project", "invalid", 1))
	_, err = store.ListProjects(ctx, 7, nil)
	require.Error(t, err)
	mock.ExpectQuery("SELECT project_name").WithArgs(int64(7), StatusClosed).
		WillReturnRows(sqlmock.NewRows([]string{"project_name", "project_path", "count", "active"}).AddRow("project", "/project", 1, 1).RowError(0, wantErr))
	_, err = store.ListProjects(ctx, 7, nil)
	require.ErrorIs(t, err, wantErr)

	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), "project", StatusActive, 1).WillReturnError(wantErr)
	_, err = store.ListProjectSessions(ctx, 7, "project", StatusActive, 1)
	require.ErrorIs(t, err, wantErr)
	projectBadRows := sqlmock.NewRows([]string{
		"session_id", "uid", "project_name", "project_path", "title", "status", "main_thread_id",
		"last_message_preview", "last_active_at_ms", "created_at_ms", "updated_at_ms", "closed_at_ms", "metadata_json",
	}).AddRow("invalid", 7, "project", "/project", "title", 1, 0, "", 1, 1, 1, 0, "{}")
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), "project", StatusActive, 1).WillReturnRows(projectBadRows)
	_, err = store.ListProjectSessions(ctx, 7, "project", StatusActive, 1)
	require.Error(t, err)
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), "project", StatusActive, 1).WillReturnRows(rowsForSessions(active).RowError(0, wantErr))
	_, err = store.ListProjectSessions(ctx, 7, "project", StatusActive, 1)
	require.ErrorIs(t, err, wantErr)

	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnError(wantErr)
	_, err = store.Update(ctx, 7, 11, UpdatePatch{})
	require.ErrorIs(t, err, wantErr)
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(active))
	mock.ExpectExec("UPDATE agent_sessions SET updated_at_ms").WithArgs(int64(0), int64(7), int64(11)).WillReturnError(wantErr)
	_, err = store.Update(ctx, 7, 11, UpdatePatch{})
	require.ErrorIs(t, err, wantErr)

	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnError(wantErr)
	_, err = store.Close(ctx, 7, 11, 1)
	require.ErrorIs(t, err, wantErr)
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(active))
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(StatusClosed, int64(1), int64(1), int64(1), int64(7), int64(11)).WillReturnError(wantErr)
	_, err = store.Close(ctx, 7, 11, 1)
	require.ErrorIs(t, err, wantErr)

	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(21), int64(1), int64(7), int64(11), StatusClosed, int64(21)).WillReturnError(wantErr)
	_, err = store.BindMainThread(ctx, 7, 11, 21, 1)
	require.ErrorIs(t, err, wantErr)
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(21), int64(1), int64(7), int64(11), StatusClosed, int64(21)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnError(wantErr)
	_, err = store.BindMainThread(ctx, 7, 11, 21, 1)
	require.ErrorIs(t, err, wantErr)
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(21), int64(1), int64(7), int64(11), StatusClosed, int64(21)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnRows(rowsForSessions(testSession(StatusClosed)))
	_, err = store.BindMainThread(ctx, 7, 11, 21, 1)
	require.ErrorIs(t, err, ErrClosed)

	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(1), int64(2), int64(7), int64(11), StatusClosed).WillReturnError(wantErr)
	_, err = store.Touch(ctx, 7, 11, TouchPatch{LastActiveAtMS: 1, UpdatedAtMS: 2})
	require.ErrorIs(t, err, wantErr)
	mock.ExpectExec("UPDATE agent_sessions").WithArgs(int64(1), int64(2), int64(7), int64(11), StatusClosed).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT session_id").WithArgs(int64(7), int64(11)).WillReturnError(wantErr)
	_, err = store.Touch(ctx, 7, 11, TouchPatch{LastActiveAtMS: 1, UpdatedAtMS: 2})
	require.ErrorIs(t, err, wantErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
