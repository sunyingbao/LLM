package eventlog

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"code.byted.org/lang/gg/choose"
	"eino-cli/deepagent/coordinator/internal/infra/idgen"
	"eino-cli/deepagent/coordinator/internal/model"
	"eino-cli/deepagent/coordinator/internal/storage"
	"eino-cli/deepagent/coordinator/internal/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultListLimit = int32(100)
	maxListLimit     = int32(1000)
)

type ListDirection string

const (
	ListDirectionForward  ListDirection = "forward"
	ListDirectionBackward ListDirection = "backward"
)

type ListOrder string

const (
	ListOrderEventID                   ListOrder = "event_id"
	ListOrderCreatedAtInThreadSequence ListOrder = "created_at_in_thread_seq"
)

type Event struct {
	EventID           int64
	SessionID         string
	ThreadID          int64
	InThreadSeq       int64
	TurnID            string
	EventType         string
	Payload           []byte
	Metadata          map[string]string
	CreatedAt         time.Time
	PersistToEventLog *bool
	FanoutToSession   *bool
	DeliveryID        string
}

type AppendEventsRequest struct {
	LeaseToken string
	Namespace  string
	ThreadID   int64
	TurnID     string
	Events     []Event
}

type ListEventsRequest struct {
	Namespace  string
	ThreadID   int64
	Cursor     int64
	Limit      int32
	TurnID     string
	EventType  string
	Direction  ListDirection
	Order      ListOrder
	PageCursor string
}

type ListSessionEventsRequest struct {
	Namespace string
	SessionID string
	Cursor    string
	Limit     int32
	TurnID    string
	EventType string
	Direction ListDirection
}

type EventLog struct {
	writeDB          *gorm.DB
	readDB           *gorm.DB
	idgen            idgen.Generator
	now              func() time.Time
	defaultListLimit int32
	maxListLimit     int32
}

type EventLogOption func(*EventLog)

func NewEventLog(writeDB *gorm.DB, readDB *gorm.DB, idgen idgen.Generator, opts ...EventLogOption) *EventLog {
	svc := &EventLog{
		writeDB:          writeDB,
		readDB:           readDB,
		idgen:            idgen,
		now:              time.Now,
		defaultListLimit: defaultListLimit,
		maxListLimit:     maxListLimit,
	}
	for _, opt := range opts {
		opt(svc)
	}
	if svc.readDB == nil {
		svc.readDB = writeDB
	}
	return svc
}

func WithClock(now func() time.Time) EventLogOption {
	return func(s *EventLog) {
		s.now = now
	}
}

func (s *EventLog) AppendEvents(ctx context.Context, req AppendEventsRequest) (appended []Event, cursor int64, err error) {
	if req.LeaseToken != "" {
		err = s.writeDB.WithContext(ctx).Transaction(func(tx *gorm.DB) (txErr error) {
			var thread model.TThread
			if txErr = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("namespace = ? and thread_id = ?", req.Namespace, req.ThreadID).Take(&thread).Error; txErr != nil {
				return txErr
			}
			if thread.LeaseToken != req.LeaseToken || !thread.LeaseDeadlineAt.After(s.now()) || (thread.Status != model.ThreadStatusRunning && thread.Status != model.ThreadStatusClosing) {
				return ErrLeaseMismatch
			}
			appended, cursor, txErr = s.writeEvents(ctx, tx, tx, req)
			if txErr == nil && !thread.LeaseDeadlineAt.After(s.now()) {
				return ErrLeaseMismatch
			}
			return txErr
		})
		if err != nil {
			return nil, 0, err
		}
		return appended, cursor, nil
	}
	return s.writeEvents(ctx, s.writeDB, s.readDB, req)
}

func (s *EventLog) writeEvents(ctx context.Context, writeDB, readDB *gorm.DB, req AppendEventsRequest) (appended []Event, cursor int64, err error) {
	if len(req.Events) == 0 {
		return nil, 0, nil
	}
	sessionID, err := getThreadSession(ctx, readDB, req.Namespace, req.ThreadID)
	if err != nil {
		return nil, 0, err
	}
	models := make([]*model.TEventLog, 0, len(req.Events))
	events := make([]Event, 0, len(req.Events))
	for _, event := range req.Events {
		eventID, err := s.idgen.NextID(ctx)
		if err != nil {
			return nil, 0, err
		}
		metadataJSON := choose.If(len(event.Metadata) == 0, "{}", util.ToString(event.Metadata))
		if event.CreatedAt.IsZero() {
			event.CreatedAt = s.now()
		}
		if event.TurnID == "" {
			event.TurnID = req.TurnID
		}
		if event.TurnID == "" {
			return nil, 0, ErrTurnIDRequired
		}
		if event.EventType == "" {
			return nil, 0, ErrEventTypeRequired
		}
		event.EventID = eventID
		event.SessionID = sessionID
		event.ThreadID = req.ThreadID
		models = append(models, &model.TEventLog{
			EventId:      eventID,
			Namespace:    req.Namespace,
			SessionId:    sessionID,
			ThreadId:     req.ThreadID,
			InThreadSeq:  event.InThreadSeq,
			TurnId:       event.TurnID,
			EventType:    event.EventType,
			Payload:      string(event.Payload),
			MetadataJson: metadataJSON,
			CreatedAt:    event.CreatedAt,
		})
		events = append(events, event)
	}
	if err := writeDB.WithContext(ctx).CreateInBatches(&models, 20).Error; err != nil {
		return nil, 0, err
	}
	return events, events[len(events)-1].EventID, nil
}

func (s *EventLog) ValidateLease(ctx context.Context, namespace string, threadID int64, leaseToken string) (err error) {
	var thread model.TThread
	if err = s.writeDB.WithContext(ctx).Where("namespace = ? and thread_id = ?", namespace, threadID).Take(&thread).Error; err != nil {
		return err
	}
	if thread.LeaseToken != leaseToken || !thread.LeaseDeadlineAt.After(s.now()) || (thread.Status != model.ThreadStatusRunning && thread.Status != model.ThreadStatusClosing) {
		return ErrLeaseMismatch
	}
	return nil
}

func (s *EventLog) ListEvents(ctx context.Context, req ListEventsRequest) ([]Event, int64, string, bool, error) {
	if _, err := s.GetThreadSession(ctx, req.Namespace, req.ThreadID); err != nil {
		return nil, 0, "", false, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = s.defaultListLimit
	}
	if limit > s.maxListLimit {
		limit = s.maxListLimit
	}

	direction := normalizeListDirection(req.Direction)
	order := normalizeListOrder(req.Order)
	if order == ListOrderCreatedAtInThreadSequence {
		if req.Cursor != 0 {
			return nil, 0, "", false, ErrInvalidCursor
		}
		return s.listEventsByCreatedSeq(ctx, req, limit, direction)
	}
	if req.PageCursor != "" {
		return nil, 0, "", false, ErrInvalidCursor
	}
	return s.listEventsByEventID(ctx, req, limit, direction)
}

func (s *EventLog) listEventsByEventID(ctx context.Context, req ListEventsRequest, limit int32, direction ListDirection) ([]Event, int64, string, bool, error) {
	var rows []*model.TEventLog
	query := s.listEventsBaseQuery(ctx, req)
	if direction == ListDirectionBackward {
		if req.Cursor > 0 {
			query = query.Where("event_id < ?", req.Cursor)
		}
	} else {
		query = query.Where("event_id > ?", req.Cursor)
	}
	order := "event_id ASC"
	if direction == ListDirectionBackward {
		order = "event_id DESC"
	}
	err := query.Order(order).Limit(int(limit + 1)).Find(&rows).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, "", false, err
	}
	if len(rows) == 0 {
		return nil, req.Cursor, "", false, nil
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}
	if direction == ListDirectionBackward {
		slices.Reverse(rows)
	}

	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return nil, 0, "", false, err
		}
		events = append(events, event)
	}
	nextCursor := events[len(events)-1].EventID
	if direction == ListDirectionBackward {
		nextCursor = events[0].EventID
	}
	return events, nextCursor, "", hasMore, nil
}

func (s *EventLog) listEventsByCreatedSeq(ctx context.Context, req ListEventsRequest, limit int32, direction ListDirection) ([]Event, int64, string, bool, error) {
	var cursor eventPositionCursor
	if req.PageCursor != "" {
		decoded, err := decodeEventPositionCursor(req.PageCursor)
		if err != nil {
			return nil, 0, "", false, err
		}
		cursor = decoded
	}

	var rows []*model.TEventLog
	query := s.listEventsBaseQuery(ctx, req)
	if req.PageCursor != "" {
		if direction == ListDirectionBackward {
			query = query.Where(
				"(created_at < ? OR (created_at = ? AND in_thread_seq < ?) OR (created_at = ? AND in_thread_seq = ? AND event_id < ?))",
				cursor.CreatedAt, cursor.CreatedAt, cursor.InThreadSeq, cursor.CreatedAt, cursor.InThreadSeq, cursor.EventID,
			)
		} else {
			query = query.Where(
				"(created_at > ? OR (created_at = ? AND in_thread_seq > ?) OR (created_at = ? AND in_thread_seq = ? AND event_id > ?))",
				cursor.CreatedAt, cursor.CreatedAt, cursor.InThreadSeq, cursor.CreatedAt, cursor.InThreadSeq, cursor.EventID,
			)
		}
	}

	order := "created_at ASC, in_thread_seq ASC, event_id ASC"
	if direction == ListDirectionBackward {
		order = "created_at DESC, in_thread_seq DESC, event_id DESC"
	}
	err := query.Order(order).Limit(int(limit + 1)).Find(&rows).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, "", false, err
	}
	if len(rows) == 0 {
		return nil, 0, req.PageCursor, false, nil
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}
	if direction == ListDirectionBackward {
		slices.Reverse(rows)
	}

	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return nil, 0, "", false, err
		}
		events = append(events, event)
	}
	nextEvent := events[len(events)-1]
	if direction == ListDirectionBackward {
		nextEvent = events[0]
	}
	nextPageCursor := encodeEventPositionCursor(nextEvent.CreatedAt, nextEvent.InThreadSeq, nextEvent.EventID)
	return events, 0, nextPageCursor, hasMore, nil
}

func (s *EventLog) listEventsBaseQuery(ctx context.Context, req ListEventsRequest) *gorm.DB {
	query := s.readDB.WithContext(ctx).
		Where("namespace = ? AND thread_id = ?", req.Namespace, req.ThreadID)
	if req.TurnID != "" {
		query = query.Where("turn_id = ?", req.TurnID)
	}
	if req.EventType != "" {
		query = query.Where("event_type = ?", req.EventType)
	}
	return query
}

func (s *EventLog) ListSessionEvents(ctx context.Context, req ListSessionEventsRequest) ([]Event, string, bool, error) {
	if req.SessionID == "" {
		return nil, "", false, ErrSessionIDRequired
	}
	if err := s.ensureNamespace(ctx, req.Namespace); err != nil {
		return nil, "", false, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = s.defaultListLimit
	}
	if limit > s.maxListLimit {
		limit = s.maxListLimit
	}

	direction := normalizeListDirection(req.Direction)
	var cursor sessionEventCursor
	if req.Cursor != "" {
		decoded, err := decodeSessionEventCursor(req.Cursor)
		if err != nil {
			return nil, "", false, err
		}
		cursor = decoded
	}

	var rows []*model.TEventLog
	query := s.readDB.WithContext(ctx).
		Where("namespace = ? AND session_id = ?", req.Namespace, req.SessionID)
	if req.Cursor != "" {
		if direction == ListDirectionBackward {
			query = query.Where("(created_at < ? OR (created_at = ? AND event_id < ?))", cursor.CreatedAt, cursor.CreatedAt, cursor.EventID)
		} else {
			query = query.Where("(created_at > ? OR (created_at = ? AND event_id > ?))", cursor.CreatedAt, cursor.CreatedAt, cursor.EventID)
		}
	}
	if req.TurnID != "" {
		query = query.Where("turn_id = ?", req.TurnID)
	}
	if req.EventType != "" {
		query = query.Where("event_type = ?", req.EventType)
	}
	order := "created_at ASC, event_id ASC"
	if direction == ListDirectionBackward {
		order = "created_at DESC, event_id DESC"
	}
	err := query.Order(order).Limit(int(limit + 1)).Find(&rows).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", false, err
	}
	if len(rows) == 0 {
		return nil, req.Cursor, false, nil
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}
	if direction == ListDirectionBackward {
		slices.Reverse(rows)
	}

	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return nil, "", false, err
		}
		events = append(events, event)
	}
	nextEvent := events[len(events)-1]
	if direction == ListDirectionBackward {
		nextEvent = events[0]
	}
	nextCursor := encodeSessionEventCursor(nextEvent.CreatedAt, nextEvent.EventID)
	return events, nextCursor, hasMore, nil
}

func normalizeListDirection(direction ListDirection) ListDirection {
	if direction == ListDirectionBackward {
		return ListDirectionBackward
	}
	return ListDirectionForward
}

func normalizeListOrder(order ListOrder) ListOrder {
	if order == ListOrderCreatedAtInThreadSequence {
		return ListOrderCreatedAtInThreadSequence
	}
	return ListOrderEventID
}

type sessionEventCursor struct {
	CreatedAt time.Time
	EventID   int64
}

func encodeSessionEventCursor(createdAt time.Time, eventID int64) string {
	return strconv.FormatInt(createdAt.UnixMicro(), 10) + ":" + strconv.FormatInt(eventID, 10)
}

func decodeSessionEventCursor(cursor string) (sessionEventCursor, error) {
	createdAtText, eventIDText, ok := strings.Cut(cursor, ":")
	if !ok {
		return sessionEventCursor{}, ErrInvalidCursor
	}
	createdAtMicros, err := strconv.ParseInt(createdAtText, 10, 64)
	if err != nil {
		return sessionEventCursor{}, ErrInvalidCursor
	}
	eventID, err := strconv.ParseInt(eventIDText, 10, 64)
	if err != nil {
		return sessionEventCursor{}, ErrInvalidCursor
	}
	return sessionEventCursor{
		CreatedAt: model.TimeFromUnixMicrosForDB(createdAtMicros),
		EventID:   eventID,
	}, nil
}

type eventPositionCursor struct {
	CreatedAt   time.Time
	InThreadSeq int64
	EventID     int64
}

func encodeEventPositionCursor(createdAt time.Time, inThreadSeq int64, eventID int64) string {
	return strconv.FormatInt(createdAt.UnixMicro(), 10) + ":" +
		strconv.FormatInt(inThreadSeq, 10) + ":" +
		strconv.FormatInt(eventID, 10)
}

func decodeEventPositionCursor(cursor string) (eventPositionCursor, error) {
	createdAtText, rest, ok := strings.Cut(cursor, ":")
	if !ok {
		return eventPositionCursor{}, ErrInvalidCursor
	}
	seqText, eventIDText, ok := strings.Cut(rest, ":")
	if !ok {
		return eventPositionCursor{}, ErrInvalidCursor
	}
	createdAtMicros, err := strconv.ParseInt(createdAtText, 10, 64)
	if err != nil {
		return eventPositionCursor{}, ErrInvalidCursor
	}
	inThreadSeq, err := strconv.ParseInt(seqText, 10, 64)
	if err != nil {
		return eventPositionCursor{}, ErrInvalidCursor
	}
	eventID, err := strconv.ParseInt(eventIDText, 10, 64)
	if err != nil {
		return eventPositionCursor{}, ErrInvalidCursor
	}
	return eventPositionCursor{
		CreatedAt:   model.TimeFromUnixMicrosForDB(createdAtMicros),
		InThreadSeq: inThreadSeq,
		EventID:     eventID,
	}, nil
}

func (s *EventLog) GetEventsByIDs(ctx context.Context, eventIDs []int64) ([]Event, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}
	rows, err := storage.FindEvents(ctx, s.readDB, eventIDs)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	eventByID := make(map[int64]Event, len(rows))
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return nil, err
		}
		eventByID[row.EventId] = event
	}

	events := make([]Event, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		event, ok := eventByID[eventID]
		if !ok {
			continue
		}
		events = append(events, event)
	}
	return events, nil
}

func eventFromRow(row *model.TEventLog) (Event, error) {
	if row == nil {
		return Event{}, nil
	}
	metadata, err := util.ToStruct[map[string]string](choose.If(row.MetadataJson == "", "{}", row.MetadataJson))
	if err != nil {
		return Event{}, err
	}
	metadataValue := map[string]string{}
	if metadata != nil {
		metadataValue = *metadata
	}
	return Event{
		EventID:     row.EventId,
		SessionID:   row.SessionId,
		ThreadID:    row.ThreadId,
		InThreadSeq: row.InThreadSeq,
		TurnID:      row.TurnId,
		EventType:   row.EventType,
		Payload:     []byte(row.Payload),
		Metadata:    metadataValue,
		CreatedAt:   row.CreatedAt,
	}, nil
}

func (s *EventLog) GetThreadSession(ctx context.Context, namespace string, threadID int64) (sessionID string, err error) {
	return getThreadSession(ctx, s.readDB, namespace, threadID)
}

func getThreadSession(ctx context.Context, db *gorm.DB, namespace string, threadID int64) (sessionID string, err error) {
	thread, err := storage.FindThread(ctx, db, threadID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrThreadNotFound
		}
		return "", err
	}
	if thread.Namespace != namespace {
		return "", ErrThreadNotFound
	}
	return thread.SessionId, nil
}

func (s *EventLog) ensureNamespace(ctx context.Context, namespace string) error {
	row, err := storage.FindNamespace(ctx, s.readDB, namespace)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNamespaceNotFound
		}
		return err
	}
	if row.Namespace != namespace {
		return ErrNamespaceNotFound
	}
	return nil
}
