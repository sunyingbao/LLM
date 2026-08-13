package threadrefs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultTableName = "t_agent_thread_ref"

var DefaultThreadNames = []string{
	"alice", "bob", "charlie", "diana", "eve", "frank", "grace", "heidi", "ivan", "judy",
	"mallory", "oscar", "peggy", "trent", "victor", "wendy", "aaron", "bella", "caleb", "daisy",
	"ethan", "fiona", "george", "hannah", "ian", "julia", "kevin", "luna", "mason", "nora",
	"oliver", "paula", "quentin", "rachel", "sam", "tina", "ulysses", "vera", "will", "xenia",
	"yara", "zack", "amber", "bruce", "claire", "daniel", "elena", "felix", "gwen", "harry",
	"iris", "jack", "kara", "leo", "mia", "noah", "opal", "peter", "queen", "rose",
	"sean", "tara", "uma", "vince", "whitney", "xander", "yasmin", "zoey", "atlas", "blair",
	"cora", "derek", "elsa", "floyd", "gina", "hugo", "isla", "jonas", "kira", "liam",
	"mila", "neil", "orion", "piper", "quinn", "riley", "sophia", "toby", "ursula", "violet",
	"wade", "xia", "yuki", "zane", "april", "brian", "celia", "dylan", "emily", "faye",
}

type Row struct {
	UserID     int64     `gorm:"column:user_id"`
	SessionID  string    `gorm:"column:session_id;size:128"`
	ThreadName string    `gorm:"column:thread_name;size:128"`
	ThreadID   int64     `gorm:"column:thread_id;index"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

type Store struct {
	db    *gorm.DB
	table string
}

func New(db *gorm.DB, table string) *Store {
	table = strings.TrimSpace(table)
	if table == "" {
		table = DefaultTableName
	}
	return &Store{db: db, table: table}
}

func (s *Store) Register(ctx context.Context, userID int64, sessionID string, threadName string, threadID int64) error {
	threadName = Normalize(threadName)
	if s == nil || s.db == nil || sessionID == "" || threadName == "" || threadID == 0 {
		return nil
	}
	now := time.Now()
	row := Row{
		UserID:     userID,
		SessionID:  sessionID,
		ThreadName: threadName,
		ThreadID:   threadID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	return s.db.WithContext(ctx).Table(s.table).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "user_id"},
				{Name: "session_id"},
				{Name: "thread_name"},
			},
			DoUpdates: clause.Assignments(map[string]any{
				"thread_id":  threadID,
				"updated_at": now,
			}),
		}).
		Create(&row).Error
}

func (s *Store) Allocate(ctx context.Context, userID int64, sessionID string, threadID int64) (string, error) {
	if s == nil || s.db == nil || sessionID == "" || threadID == 0 {
		return "", nil
	}
	for _, name := range DefaultThreadNames {
		ok, err := s.tryRegister(ctx, userID, sessionID, name, threadID)
		if err != nil {
			return "", err
		}
		if ok {
			return name, nil
		}
	}
	fallbackName := fmt.Sprintf("thread_%d", threadID)
	return fallbackName, s.Register(ctx, userID, sessionID, fallbackName, threadID)
}

func (s *Store) tryRegister(ctx context.Context, userID int64, sessionID string, threadName string, threadID int64) (bool, error) {
	threadName = Normalize(threadName)
	if s == nil || s.db == nil || sessionID == "" || threadName == "" || threadID == 0 {
		return false, nil
	}
	now := time.Now()
	row := Row{
		UserID:     userID,
		SessionID:  sessionID,
		ThreadName: threadName,
		ThreadID:   threadID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	tx := s.db.WithContext(ctx).Table(s.table).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "user_id"},
				{Name: "session_id"},
				{Name: "thread_name"},
			},
			DoNothing: true,
		}).
		Create(&row)
	if tx.Error != nil {
		return false, tx.Error
	}
	return tx.RowsAffected > 0, nil
}

func (s *Store) Resolve(ctx context.Context, userID int64, sessionID string, threadName string) (int64, bool, error) {
	threadName = Normalize(threadName)
	if threadName == "" {
		return 0, false, nil
	}
	if threadID, err := strconv.ParseInt(threadName, 10, 64); err == nil && threadID != 0 {
		return threadID, true, nil
	}
	if s == nil || s.db == nil || sessionID == "" {
		return 0, false, nil
	}
	var row Row
	err := s.db.WithContext(ctx).Table(s.table).
		Where("user_id = ? AND session_id = ? AND thread_name = ?", userID, sessionID, threadName).
		Take(&row).Error
	if err == nil {
		return row.ThreadID, row.ThreadID != 0, nil
	}
	if err == gorm.ErrRecordNotFound {
		return 0, false, nil
	}
	return 0, false, err
}

func (s *Store) RefForThread(ctx context.Context, userID int64, sessionID string, threadID int64) (string, bool, error) {
	if s == nil || s.db == nil || sessionID == "" || threadID == 0 {
		return "", false, nil
	}
	var rows []Row
	err := s.db.WithContext(ctx).Table(s.table).
		Where("user_id = ? AND session_id = ? AND thread_id = ?", userID, sessionID, threadID).
		Find(&rows).Error
	if err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	best := rows[0].ThreadName
	bestCreatedAt := rows[0].CreatedAt
	for _, row := range rows {
		if row.ThreadName == "main" {
			return row.ThreadName, true, nil
		}
		if best == "" || row.CreatedAt.Before(bestCreatedAt) {
			best = row.ThreadName
			bestCreatedAt = row.CreatedAt
		}
	}
	return best, best != "", nil
}

func Normalize(ref string) string {
	return strings.ToLower(strings.TrimSpace(ref))
}
