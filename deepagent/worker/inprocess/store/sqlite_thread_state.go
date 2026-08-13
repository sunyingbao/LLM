package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"eino-cli/deepagent/core/utils"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const DefaultThreadStateTable = "agentworker_inprocess_threads"

type SQLiteThreadStateStore struct {
	db    *gorm.DB
	table string
}

type threadStateRow struct {
	ID             string     `gorm:"column:id;primaryKey;size:128"`
	UserID         int64      `gorm:"column:user_id;index"`
	SessionID      string     `gorm:"column:session_id;size:128;index"`
	ParentThreadID string     `gorm:"column:parent_thread_id;size:128;index"`
	RootThreadID   string     `gorm:"column:root_thread_id;size:128;index"`
	Title          string     `gorm:"column:title;size:512"`
	ProfileJSON    string     `gorm:"column:profile;type:text"`
	Cwd            string     `gorm:"column:cwd;size:2048"`
	MetadataJSON   string     `gorm:"column:metadata_json;type:text"`
	PendingBlock   string     `gorm:"column:pending_block_json;type:text"`
	ClosedAt       *time.Time `gorm:"column:closed_at"`
	CreatedAt      time.Time  `gorm:"column:created_at;index"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;index"`
}

func OpenSQLiteThreadStateStore(path string, table string) (*SQLiteThreadStateStore, error) {
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	store := NewSQLiteThreadStateStore(db, table)
	if err := store.AutoMigrate(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func NewSQLiteThreadStateStore(db *gorm.DB, table string) *SQLiteThreadStateStore {
	table = strings.TrimSpace(table)
	if table == "" {
		table = DefaultThreadStateTable
	}
	return &SQLiteThreadStateStore{db: db, table: table}
}

func (s *SQLiteThreadStateStore) AutoMigrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite thread state store is nil")
	}
	return s.db.WithContext(ctx).Table(s.table).AutoMigrate(&threadStateRow{})
}

func (s *SQLiteThreadStateStore) CreateThread(ctx context.Context, spec inprocess.CreateThreadSpec) (*inprocess.ThreadState, error) {
	if s == nil || s.db == nil {
		return nil, inprocess.ErrMissingThreadStateStore
	}
	id := strings.TrimSpace(spec.ID)
	if id == "" {
		id = uuid.NewString()
	}
	rootID := id
	if spec.ParentThreadID != "" {
		parent, err := s.GetThread(ctx, spec.ParentThreadID)
		if err != nil {
			return nil, err
		}
		if spec.UserID == 0 {
			spec.UserID = parent.UserID
		} else if parent.UserID != 0 && parent.UserID != spec.UserID {
			return nil, inprocess.ErrInvalidThreadState
		}
		if spec.SessionID == "" {
			spec.SessionID = parent.SessionID
		} else if parent.SessionID != "" && parent.SessionID != spec.SessionID {
			return nil, inprocess.ErrInvalidThreadState
		}
		rootID = parent.RootThreadID
		if rootID == "" {
			rootID = parent.ID
		}
	}
	if spec.UserID == 0 {
		return nil, inprocess.ErrMissingUserID
	}
	if spec.SessionID == "" {
		return nil, inprocess.ErrMissingSessionID
	}
	now := time.Now()
	row := threadStateRow{
		ID:             id,
		UserID:         spec.UserID,
		SessionID:      spec.SessionID,
		ParentThreadID: spec.ParentThreadID,
		RootThreadID:   rootID,
		Title:          spec.Title,
		ProfileJSON:    utils.ToString(spec.Profile),
		Cwd:            spec.Profile.Cwd,
		MetadataJSON:   utils.ToString(spec.Metadata),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.db.WithContext(ctx).Table(s.table).Create(&row).Error; err != nil {
		return nil, err
	}
	return s.GetThread(ctx, id)
}

func (s *SQLiteThreadStateStore) GetThread(ctx context.Context, threadID string) (*inprocess.ThreadState, error) {
	if s == nil || s.db == nil {
		return nil, inprocess.ErrMissingThreadStateStore
	}
	var row threadStateRow
	if err := s.db.WithContext(ctx).Table(s.table).Where("id = ?", threadID).Take(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, inprocess.ErrThreadNotFound
		}
		return nil, err
	}
	return row.toThreadState()
}

func (s *SQLiteThreadStateStore) ListThreads(ctx context.Context, opts inprocess.ListThreadsOptions) ([]*inprocess.ThreadState, error) {
	if s == nil || s.db == nil {
		return nil, inprocess.ErrMissingThreadStateStore
	}
	query := s.db.WithContext(ctx).Table(s.table)
	if opts.UserID != 0 {
		query = query.Where("user_id = ?", opts.UserID)
	}
	if opts.SessionID != "" {
		query = query.Where("session_id = ?", opts.SessionID)
	}
	if opts.Cwd != "" {
		query = query.Where("cwd = ?", opts.Cwd)
	}
	if opts.RootOnly {
		query = query.Where("parent_thread_id = ?", "")
	}
	if !opts.IncludeClosed {
		query = query.Where("closed_at IS NULL")
	}
	return s.listThreads(ctx, query, opts, false)
}

func (s *SQLiteThreadStateStore) ListThreadsBySession(ctx context.Context, sessionID string, opts inprocess.ListThreadsOptions) ([]*inprocess.ThreadState, error) {
	if s == nil || s.db == nil {
		return nil, inprocess.ErrMissingThreadStateStore
	}
	query := s.db.WithContext(ctx).Table(s.table).Where("session_id = ?", sessionID)
	return s.listThreads(ctx, query, opts, true)
}

func (s *SQLiteThreadStateStore) listThreads(ctx context.Context, query *gorm.DB, opts inprocess.ListThreadsOptions, legacySessionList bool) ([]*inprocess.ThreadState, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := parseCursorOffset(opts.Cursor)
	orderColumn, desc := normalizeListThreadsOrder(opts)
	orderDirection := "ASC"
	if desc {
		orderDirection = "DESC"
	}
	var rows []threadStateRow
	if legacySessionList {
		orderColumn = string(inprocess.ListThreadsOrderByUpdatedAt)
		orderDirection = "DESC"
	}
	if err := query.
		Order(orderColumn + " " + orderDirection + ", id ASC").
		Limit(limit).
		Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*inprocess.ThreadState, 0, len(rows))
	for _, row := range rows {
		state, err := row.toThreadState()
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}

func normalizeListThreadsOrder(opts inprocess.ListThreadsOptions) (string, bool) {
	switch opts.OrderBy {
	case inprocess.ListThreadsOrderByCreatedAt:
		return string(inprocess.ListThreadsOrderByCreatedAt), opts.Desc
	case inprocess.ListThreadsOrderByUpdatedAt:
		return string(inprocess.ListThreadsOrderByUpdatedAt), opts.Desc
	default:
		return string(inprocess.ListThreadsOrderByUpdatedAt), true
	}
}

func (s *SQLiteThreadStateStore) UpdateThread(ctx context.Context, threadID string, patch inprocess.UpdateThreadStatePatch) (*inprocess.ThreadState, error) {
	if s == nil || s.db == nil {
		return nil, inprocess.ErrMissingThreadStateStore
	}
	updates := map[string]any{
		"updated_at": time.Now(),
	}
	if patch.Title != nil {
		updates["title"] = *patch.Title
	}
	if patch.Cwd != nil {
		current, err := s.GetThread(ctx, threadID)
		if err != nil {
			return nil, err
		}
		profile := current.Profile
		profile.Cwd = *patch.Cwd
		updates["profile"] = utils.ToString(profile)
		updates["cwd"] = *patch.Cwd
	}
	if patch.Metadata != nil {
		updates["metadata_json"] = utils.ToString(patch.Metadata)
	}
	if patch.PendingBlock != nil {
		updates["pending_block_json"] = utils.ToString(patch.PendingBlock)
	} else if patch.ClearPendingBlock {
		updates["pending_block_json"] = ""
	}
	if patch.ClosedAt != nil {
		updates["closed_at"] = *patch.ClosedAt
	}
	tx := s.db.WithContext(ctx).Table(s.table).Where("id = ?", threadID).Updates(updates)
	if tx.Error != nil {
		return nil, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, inprocess.ErrThreadNotFound
	}
	return s.GetThread(ctx, threadID)
}

func (r threadStateRow) toThreadState() (*inprocess.ThreadState, error) {
	metadata, err := utils.ToStruct[map[string]string](r.MetadataJSON)
	if err != nil {
		return nil, err
	}
	profile, err := utils.ToStruct[inprocess.ThreadProfile](r.ProfileJSON)
	if err != nil {
		return nil, err
	}
	if profile.Cwd == "" {
		profile.Cwd = r.Cwd
	}
	var pendingBlock *agentworker.PendingBlock
	if strings.TrimSpace(r.PendingBlock) != "" {
		block, err := utils.ToStruct[agentworker.PendingBlock](r.PendingBlock)
		if err != nil {
			return nil, err
		}
		pendingBlock = &block
	}
	return &inprocess.ThreadState{
		ID:             r.ID,
		UserID:         r.UserID,
		SessionID:      r.SessionID,
		ParentThreadID: r.ParentThreadID,
		RootThreadID:   r.RootThreadID,
		Title:          r.Title,
		Profile:        profile,
		Metadata:       metadata,
		PendingBlock:   pendingBlock,
		ClosedAt:       r.ClosedAt,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}, nil
}

func parseCursorOffset(cursor string) int {
	if cursor == "" {
		return 0
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

var _ inprocess.ThreadStateStore = (*SQLiteThreadStateStore)(nil)
