package session

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrNotFound       = errors.New("agent session not found")
	ErrClosed         = errors.New("agent session is closed")
	ErrMainThreadBusy = errors.New("agent session main thread already bound")
)

const sessionColumns = "session_id, uid, project_name, project_path, title, status, main_thread_id, last_message_preview, last_active_at_ms, created_at_ms, updated_at_ms, closed_at_ms, COALESCE(metadata_json, '')"

var tableNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type Store struct {
	db    *sql.DB
	table string
}

func NewStore(db *sql.DB, table string) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("mysql db is required")
	}
	if !tableNamePattern.MatchString(table) {
		return nil, fmt.Errorf("invalid agent session table name %q", table)
	}
	return &Store{db: db, table: table}, nil
}

func (s *Store) Create(ctx context.Context, sess *Session) error {
	if sess == nil {
		return fmt.Errorf("session is required")
	}
	query := fmt.Sprintf(`INSERT INTO %s
(session_id, uid, project_name, project_path, title, status, main_thread_id, last_message_preview, last_active_at_ms, created_at_ms, updated_at_ms, closed_at_ms, metadata_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.table)
	_, err := s.db.ExecContext(ctx, query,
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
	return err
}

func (s *Store) Get(ctx context.Context, uid, sessionID int64) (*Session, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE uid = ? AND session_id = ? LIMIT 1`, sessionColumns, s.table)
	row := s.db.QueryRowContext(ctx, query, uid, sessionID)
	return scanSession(row)
}

func (s *Store) List(ctx context.Context, filter ListFilter) ([]*Session, *Cursor, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	args := []any{filter.UID}
	where := []string{"uid = ?"}
	if filter.Status != nil {
		where = append(where, "status = ?")
		args = append(args, *filter.Status)
	}
	if strings.TrimSpace(filter.ProjectName) != "" {
		where = append(where, "project_name = ?")
		args = append(args, strings.TrimSpace(filter.ProjectName))
	}
	if filter.Cursor != nil {
		where = append(where, `(last_active_at_ms < ? OR (last_active_at_ms = ? AND updated_at_ms < ?) OR (last_active_at_ms = ? AND updated_at_ms = ? AND session_id < ?))`)
		args = append(args,
			filter.Cursor.LastActiveAtMS,
			filter.Cursor.LastActiveAtMS, filter.Cursor.UpdatedAtMS,
			filter.Cursor.LastActiveAtMS, filter.Cursor.UpdatedAtMS, filter.Cursor.SessionID,
		)
	}
	args = append(args, filter.Limit+1)
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE %s ORDER BY last_active_at_ms DESC, updated_at_ms DESC, session_id DESC LIMIT ?`,
		sessionColumns, s.table, strings.Join(where, " AND "))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	sessions := make([]*Session, 0, filter.Limit+1)
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, nil, err
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *Cursor
	if len(sessions) > filter.Limit {
		last := sessions[filter.Limit-1]
		next = &Cursor{
			LastActiveAtMS: last.LastActiveAtMS,
			UpdatedAtMS:    last.UpdatedAtMS,
			SessionID:      last.SessionID,
		}
		sessions = sessions[:filter.Limit]
	}
	return sessions, next, nil
}

func (s *Store) ListProjects(ctx context.Context, uid int64, status *int64) ([]*SessionProject, error) {
	args := []any{uid}
	where := []string{"uid = ?", "project_name <> ''"}
	if status != nil {
		where = append(where, "status = ?")
		args = append(args, *status)
	} else {
		where = append(where, "status <> ?")
		args = append(args, StatusClosed)
	}
	query := fmt.Sprintf(`SELECT project_name, MIN(project_path), COUNT(*), MAX(last_active_at_ms)
FROM %s
WHERE %s
GROUP BY project_name
ORDER BY MAX(last_active_at_ms) DESC, project_name ASC`, s.table, strings.Join(where, " AND "))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*SessionProject
	for rows.Next() {
		var project SessionProject
		if err := rows.Scan(&project.ProjectName, &project.ProjectPath, &project.SessionCount, &project.LastActiveAtMS); err != nil {
			return nil, err
		}
		projects = append(projects, &project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return projects, nil
}

func (s *Store) ListProjectSessions(ctx context.Context, uid int64, projectName string, status int64, limit int) ([]*Session, error) {
	projectName = strings.TrimSpace(projectName)
	if limit <= 0 {
		return nil, nil
	}
	query := fmt.Sprintf(`SELECT %s FROM %s
WHERE uid = ? AND project_name = ? AND status = ?
ORDER BY last_active_at_ms DESC, updated_at_ms DESC, session_id DESC
LIMIT ?`, sessionColumns, s.table)
	rows, err := s.db.QueryContext(ctx, query, uid, projectName, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]*Session, 0, limit)
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (s *Store) Update(ctx context.Context, uid, sessionID int64, patch UpdatePatch) (*Session, error) {
	current, err := s.Get(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	if current.Status == StatusClosed {
		return nil, ErrClosed
	}
	sets := []string{"updated_at_ms = ?"}
	args := []any{patch.UpdatedAt}
	if patch.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *patch.Title)
	}
	if patch.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *patch.Status)
	}
	args = append(args, uid, sessionID)
	query := fmt.Sprintf(`UPDATE %s SET %s WHERE uid = ? AND session_id = ?`, s.table, strings.Join(sets, ", "))
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return nil, err
	}
	return s.Get(ctx, uid, sessionID)
}

func (s *Store) Close(ctx context.Context, uid, sessionID, now int64) (*Session, error) {
	if _, err := s.Get(ctx, uid, sessionID); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`UPDATE %s
SET status = ?, closed_at_ms = CASE WHEN closed_at_ms = 0 THEN ? ELSE closed_at_ms END, updated_at_ms = ?, last_active_at_ms = ?
WHERE uid = ? AND session_id = ?`, s.table)
	if _, err := s.db.ExecContext(ctx, query, StatusClosed, now, now, now, uid, sessionID); err != nil {
		return nil, err
	}
	return s.Get(ctx, uid, sessionID)
}

func (s *Store) CloseProjectSessions(ctx context.Context, uid int64, projectName string, sessionIDs []int64, now int64) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(sessionIDs))
	args := []any{StatusClosed, now, now, now, uid, strings.TrimSpace(projectName), StatusActive}
	for _, sessionID := range sessionIDs {
		placeholders = append(placeholders, "?")
		args = append(args, sessionID)
	}
	query := fmt.Sprintf(`UPDATE %s
SET status = ?, closed_at_ms = CASE WHEN closed_at_ms = 0 THEN ? ELSE closed_at_ms END, updated_at_ms = ?, last_active_at_ms = ?
WHERE uid = ? AND project_name = ? AND status = ? AND session_id IN (%s)`,
		s.table, strings.Join(placeholders, ", "))
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *Store) BindMainThread(ctx context.Context, uid, sessionID, mainThreadID, now int64) (*Session, error) {
	if mainThreadID <= 0 {
		return nil, fmt.Errorf("main_thread_id is required")
	}
	query := fmt.Sprintf(`UPDATE %s
SET main_thread_id = ?, updated_at_ms = ?
WHERE uid = ? AND session_id = ? AND status <> ? AND (main_thread_id = 0 OR main_thread_id = ?)`, s.table)
	if _, err := s.db.ExecContext(ctx, query, mainThreadID, now, uid, sessionID, StatusClosed, mainThreadID); err != nil {
		return nil, err
	}
	current, err := s.Get(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	if current.Status == StatusClosed {
		return nil, ErrClosed
	}
	if current.MainThreadID != 0 && current.MainThreadID != mainThreadID {
		return nil, ErrMainThreadBusy
	}
	return current, nil
}

func (s *Store) Touch(ctx context.Context, uid, sessionID int64, patch TouchPatch) (*Session, error) {
	sets := []string{"last_active_at_ms = ?", "updated_at_ms = ?"}
	args := []any{patch.LastActiveAtMS, patch.UpdatedAtMS}
	if patch.LastMessagePreview != nil {
		sets = append(sets, "last_message_preview = ?")
		args = append(args, *patch.LastMessagePreview)
	}
	if patch.TitleIfEmpty != nil {
		sets = append(sets, "title = CASE WHEN title = '' THEN ? ELSE title END")
		args = append(args, *patch.TitleIfEmpty)
	}
	args = append(args, uid, sessionID, StatusClosed)
	query := fmt.Sprintf(`UPDATE %s SET %s WHERE uid = ? AND session_id = ? AND status <> ?`, s.table, strings.Join(sets, ", "))
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return nil, err
	}
	current, err := s.Get(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	if current.Status == StatusClosed {
		return nil, ErrClosed
	}
	return current, nil
}

func EncodeCursor(cursor *Cursor) (encoded string) {
	if cursor == nil {
		return ""
	}
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeCursor(raw string) (*Cursor, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode cursor: %w", err)
	}
	var cursor Cursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return nil, fmt.Errorf("parse cursor: %w", err)
	}
	if cursor.SessionID <= 0 {
		return nil, fmt.Errorf("invalid cursor")
	}
	return &cursor, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (*Session, error) {
	var sess Session
	if err := row.Scan(
		&sess.SessionID,
		&sess.UID,
		&sess.ProjectName,
		&sess.ProjectPath,
		&sess.Title,
		&sess.Status,
		&sess.MainThreadID,
		&sess.LastMessagePreview,
		&sess.LastActiveAtMS,
		&sess.CreatedAtMS,
		&sess.UpdatedAtMS,
		&sess.ClosedAtMS,
		&sess.MetadataJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sess, nil
}
