package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sessiondal "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/dal/session"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/infra/idgen"
	sessionservice "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk_session/service/session"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk_common"
	httpapi "eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/hertz_gen/deep_agent_sdk"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/deps"
	"eino-cli/deepagent/coordinator"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestLocalSessionGetPreservesHTTPFields(t *testing.T) {
	mock := installSessionService(t)
	mock.ExpectQuery("SELECT .* FROM t_agent_session WHERE uid = \\? AND session_id = \\?").
		WithArgs(int64(7), int64(101)).WillReturnRows(contractSessionRows())
	response, err := Get(context.Background(), 7, &httpapi.GetSessionHTTPRequest{SessionID: 101})
	require.NoError(t, err)
	require.Equal(t, int64(101), response.SessionView.Session.SessionID)
	require.Equal(t, int64(1001), response.SessionView.Session.GetMainThreadID())
	require.NotNil(t, response.SessionView.Session.Title)
	require.Equal(t, "", *response.SessionView.Session.Title)
	require.Nil(t, response.SessionView.Threads)
	require.Equal(t, common.BaseRespOK(), response.BaseResp)
	wire, err := json.Marshal(response.SessionView)
	require.NoError(t, err)
	require.JSONEq(t, `{"session":{"session_id":"101","uid":"7","title":"","main_thread_id":"1001","project_name":"proj","project_path":"/workspace/proj","last_message_preview":"preview","last_active_at_ms":300,"status":1,"created_at_ms":100,"updated_at_ms":300}}`, string(wire))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLocalSessionThreadFields(t *testing.T) {
	ac := &contractCoordinator{threads: []*coordinator.Thread{
		{ThreadID: 1001, Namespace: "demo", SessionID: "101", Status: "running", CreatedAt: time.UnixMilli(100), UpdatedAt: time.UnixMilli(200)},
		{ThreadID: 1002, Namespace: "demo", SessionID: "bad", UserID: 8, Status: "blocked", Metadata: map[string]string{"parent_thread_id": " 1001 ", "root_thread_id": "1001"}},
	}}
	mock := installSessionServiceWithCoordinator(t, ac)
	mock.ExpectQuery("SELECT .* FROM t_agent_session").WithArgs(int64(7), int64(101)).WillReturnRows(contractSessionRows())
	include := true
	response, err := Get(context.Background(), 7, &httpapi.GetSessionHTTPRequest{SessionID: 101, IncludeThreads: &include})
	require.NoError(t, err)
	require.Len(t, response.SessionView.Threads, 2)
	main, child := response.SessionView.Threads[0], response.SessionView.Threads[1]
	require.Equal(t, httpcommon.AgentThreadRole_MAIN, main.GetRole())
	require.Equal(t, httpcommon.AgentThreadStatus_RUNNING, main.Status)
	require.Equal(t, int64(7), main.UID)
	require.Equal(t, int64(100), main.GetCreatedAtMs())
	require.Equal(t, httpcommon.AgentThreadRole_CHILD, child.GetRole())
	require.Equal(t, httpcommon.AgentThreadStatus_BLOCKED, child.Status)
	require.Equal(t, int64(101), child.SessionID)
	require.Equal(t, int64(8), child.UID)
	require.Equal(t, "1001", child.GetParentThreadID())
	require.Equal(t, "1001", child.GetRootThreadID())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLocalSessionMutationRejections(t *testing.T) {
	mock := installSessionService(t)
	closed := httpcommon.AgentSessionStatus_CLOSED
	_, err := Update(context.Background(), 7, &httpapi.UpdateSessionHTTPRequest{SessionID: 101, Status: &closed})
	base, status := common.BaseRespFromError(err)
	require.Equal(t, 502, status)
	require.Equal(t, int32(400), base.StatusCode)
	err = BindMainThread(context.Background(), 7, 101, 0)
	base, _ = common.BaseRespFromError(err)
	require.Equal(t, int32(400), base.StatusCode)
	mock.ExpectExec("UPDATE t_agent_session SET main_thread_id").
		WithArgs(int64(2001), sqlmock.AnyArg(), int64(7), int64(101), sessiondal.StatusClosed, int64(2001)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT .* FROM t_agent_session").WithArgs(int64(7), int64(101)).WillReturnRows(contractSessionRows())
	err = BindMainThread(context.Background(), 7, 101, 2001)
	base, status = common.BaseRespFromError(err)
	require.Equal(t, 502, status)
	require.Equal(t, int32(409), base.StatusCode)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLocalSessionTouchEmptyPreviewDoesNotClearStoredText(t *testing.T) {
	mock := installSessionService(t)
	mock.ExpectExec("UPDATE t_agent_session SET last_active_at_ms = \\?, updated_at_ms = \\? WHERE uid = \\? AND session_id = \\?").
		WithArgs(int64(500), sqlmock.AnyArg(), int64(7), int64(101), sessiondal.StatusClosed).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .* FROM t_agent_session").WithArgs(int64(7), int64(101)).WillReturnRows(contractSessionRows())
	view, err := Touch(context.Background(), 7, 101, "", "", 500)
	require.NoError(t, err)
	require.Equal(t, "preview", view.Session.GetLastMessagePreview())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLocalSessionCloseStopsOnThreadFailure(t *testing.T) {
	mock := installSessionServiceWithCoordinator(t, &contractCoordinator{err: errors.New("close failed")})
	mock.ExpectQuery("SELECT .* FROM t_agent_session").WithArgs(int64(7), int64(101)).WillReturnRows(contractSessionRows())
	_, err := Close(context.Background(), 7, &httpapi.CloseSessionHTTPRequest{SessionID: 101})
	require.ErrorContains(t, err, "close failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLocalSessionProjectsAndEmptyClose(t *testing.T) {
	mock := installSessionService(t)
	mock.ExpectQuery("SELECT project_name, MIN\\(project_path\\), COUNT\\(\\*\\), MAX\\(last_active_at_ms\\)").
		WithArgs(int64(7), sessiondal.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"project_name", "project_path", "count", "last_active"}).AddRow("proj", "/workspace/proj", 2, 300))
	projects, err := ListProjects(context.Background(), 7, &httpapi.ListProjectsHTTPRequest{})
	require.NoError(t, err)
	require.Len(t, projects.Projects, 1)
	require.Equal(t, int64(2), projects.Projects[0].GetSessionCount())
	mock.ExpectQuery("SELECT .* FROM t_agent_session").WithArgs(int64(7), "empty", sessiondal.StatusActive, 101).
		WillReturnRows(sqlmock.NewRows([]string{"session_id"}))
	closed, err := CloseProject(context.Background(), 7, &httpapi.CloseProjectHTTPRequest{ProjectName: "empty"})
	require.NoError(t, err)
	require.Equal(t, "empty", closed.Project.ProjectName)
	require.NotNil(t, closed.ClosedSessionCount)
	require.Equal(t, int64(0), closed.GetClosedSessionCount())
	require.Equal(t, int64(0), closed.GetClosedThreadCount())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLocalSessionErrorsPreserveHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		name    string
		uid     int64
		err     error
		code    int32
		message string
	}{
		{name: "invalid owner", code: 400, message: "uid is required"},
		{name: "not found", uid: 7, err: sessiondal.ErrNotFound, code: 404, message: "agent session not found"},
		{name: "storage failure", uid: 7, err: errors.New("database unavailable"), code: 500, message: "database unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := installSessionService(t)
			if tc.uid > 0 {
				mock.ExpectQuery("SELECT .* FROM t_agent_session").WithArgs(tc.uid, int64(101)).WillReturnError(tc.err)
			}
			_, err := Get(context.Background(), tc.uid, &httpapi.GetSessionHTTPRequest{SessionID: 101})
			base, status := common.BaseRespFromError(err)
			require.Equal(t, 502, status)
			require.Equal(t, tc.code, base.StatusCode)
			require.Equal(t, "deep_agent_sdk_session.GetSession returned non-zero BaseResp: "+tc.message, base.StatusMessage)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLocalSessionListPreservesPagination(t *testing.T) {
	mock := installSessionService(t)
	rows := contractSessionRows()
	rows.AddRow(100, 7, "proj", "/workspace/proj", "older", 1, 0, "", 200, 100, 200, 0, "{}")
	mock.ExpectQuery("SELECT .* ORDER BY last_active_at_ms DESC, updated_at_ms DESC, session_id DESC LIMIT \\?").
		WithArgs(int64(7), 2).WillReturnRows(rows)
	limit := int32(1)
	response, err := List(context.Background(), 7, &httpapi.ListSessionsHTTPRequest{Limit: &limit})
	require.NoError(t, err)
	require.Len(t, response.Sessions, 1)
	require.True(t, response.PageInfo.HasMore)
	cursor, err := sessiondal.DecodeCursor(response.PageInfo.GetNextCursor())
	require.NoError(t, err)
	require.Equal(t, &sessiondal.Cursor{LastActiveAtMS: 300, UpdatedAtMS: 300, SessionID: 101}, cursor)
	require.NoError(t, mock.ExpectationsWereMet())
}

func installSessionService(t *testing.T) (mock sqlmock.Sqlmock) {
	t.Helper()
	return installSessionServiceWithCoordinator(t, nil)
}

func installSessionServiceWithCoordinator(t *testing.T, ac sessionservice.CoordinatorClient) (mock sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	store, err := sessiondal.NewStore(db, "t_agent_session")
	require.NoError(t, err)
	svc, err := sessionservice.New(store, idgen.NewLocalGenerator(), ac)
	require.NoError(t, err)
	deps.SetSessionService(svc)
	t.Cleanup(func() { deps.SetSessionService(nil); _ = db.Close() })
	return mock
}

type contractCoordinator struct {
	threads []*coordinator.Thread
	err     error
}

func (c *contractCoordinator) ListSessionThreads(context.Context, int64) (threads []*coordinator.Thread, err error) {
	return c.threads, c.err
}

func (c *contractCoordinator) CloseSessionThreads(context.Context, int64, string) (threads []*coordinator.Thread, err error) {
	return c.threads, c.err
}

func contractSessionRows() (rows *sqlmock.Rows) {
	return sqlmock.NewRows([]string{"session_id", "uid", "project_name", "project_path", "title", "status", "main_thread_id", "last_message_preview", "last_active_at_ms", "created_at_ms", "updated_at_ms", "closed_at_ms", "metadata_json"}).
		AddRow(101, 7, "proj", "/workspace/proj", "", 1, 1001, "preview", 300, 100, 300, 0, "{}")
}
