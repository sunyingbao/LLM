package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	serialiser "eino-cli/deepagent/serialiser"
	"eino-cli/deepagent/worker"
	inprocess "eino-cli/deepagent/worker/inprocess"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const DefaultEventTable = "agentworker_inprocess_events"

type SQLiteEventStore struct {
	db    *gorm.DB
	table string
}

type eventRow struct {
	ID           string    `gorm:"column:id;primaryKey;size:128"`
	ThreadID     string    `gorm:"column:thread_id;size:128;index"`
	TurnID       string    `gorm:"column:turn_id;size:128;index"`
	Type         string    `gorm:"column:type;size:128;index"`
	Payload      []byte    `gorm:"column:payload;type:blob"`
	MetadataJSON string    `gorm:"column:metadata_json;type:text"`
	TS           time.Time `gorm:"column:ts;index"`
}

func OpenSQLiteEventStore(path string, table string) (*SQLiteEventStore, error) {
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	store := NewSQLiteEventStore(db, table)
	if err := store.AutoMigrate(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func NewSQLiteEventStore(db *gorm.DB, table string) *SQLiteEventStore {
	table = strings.TrimSpace(table)
	if table == "" {
		table = DefaultEventTable
	}
	return &SQLiteEventStore{db: db, table: table}
}

func (s *SQLiteEventStore) AutoMigrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite event store is nil")
	}
	return s.db.WithContext(ctx).Table(s.table).AutoMigrate(&eventRow{})
}

func (s *SQLiteEventStore) AppendEvent(ctx context.Context, ev *agentworker.Event) error {
	if s == nil || s.db == nil {
		return inprocess.ErrMissingEventStore
	}
	if ev == nil {
		return nil
	}
	id := strings.TrimSpace(ev.ID)
	if id == "" {
		id = uuid.NewString()
	}
	row := eventRow{
		ID:           id,
		ThreadID:     ev.ThreadID,
		TurnID:       ev.TurnID,
		Type:         string(ev.Type),
		Payload:      append([]byte(nil), ev.Payload...),
		MetadataJSON: serialiser.ToString(ev.Metadata),
		TS:           ev.TS,
	}
	if row.TS.IsZero() {
		row.TS = time.Now()
	}
	return s.db.WithContext(ctx).Table(s.table).Create(&row).Error
}

func (s *SQLiteEventStore) ListEvents(ctx context.Context, threadID string, opts inprocess.ListEventsOptions) ([]*agentworker.Event, error) {
	if s == nil || s.db == nil {
		return nil, inprocess.ErrMissingEventStore
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := parseEventOffset(opts.Cursor)
	order := "ts ASC, id ASC"
	if opts.Reverse {
		order = "ts DESC, id DESC"
	}
	var rows []eventRow
	if err := s.db.WithContext(ctx).Table(s.table).
		Where("thread_id = ?", threadID).
		Order(order).
		Limit(limit).
		Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*agentworker.Event, 0, len(rows))
	for _, row := range rows {
		metadata, err := serialiser.ToStruct[map[string]string](row.MetadataJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, &agentworker.Event{
			ID:       row.ID,
			ThreadID: row.ThreadID,
			TurnID:   row.TurnID,
			Type:     agentworker.EventType(row.Type),
			Payload:  append([]byte(nil), row.Payload...),
			Metadata: metadata,
			TS:       row.TS,
		})
	}
	return out, nil
}

func parseEventOffset(cursor string) int {
	if cursor == "" {
		return 0
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

var _ inprocess.EventStore = (*SQLiteEventStore)(nil)
