//go:build mysql_integration

package eventlog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"eino-cli/deepagent/coordinator/internal/model"
	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMySQLAppendEventsSerializesLeaseReplacement(t *testing.T) {
	db := newMySQLLeaseTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.WithContext(ctx).Create(&model.TThread{
		ThreadId: 1, Namespace: "lease-test", SessionId: "session", Status: model.ThreadStatusRunning,
		LeaseToken: "old", LeaseDeadlineAt: now.Add(time.Minute), MetadataJson: "{}",
		ReadyAt: now, LastActiveAt: now, CreatedAt: now, UpdatedAt: now, ClosedAt: now,
	}).Error)

	generator := &leaseLockIDGenerator{entered: make(chan struct{}), proceed: make(chan struct{})}
	defer close(generator.proceed)
	svc := NewEventLog(db, db, generator)
	request := AppendEventsRequest{Namespace: "lease-test", ThreadID: 1, LeaseToken: "old", TurnID: "turn", Events: []Event{{EventType: "event"}}}
	appendDone := make(chan error, 1)
	go func() {
		_, _, err := svc.AppendEvents(ctx, request)
		appendDone <- err
	}()
	select {
	case <-generator.entered:
	case <-ctx.Done():
		t.Fatal("append did not acquire the thread lock before generating the event ID")
	}

	sqlDB, err := db.DB()
	require.NoError(t, err)
	connection, err := sqlDB.Conn(ctx)
	require.NoError(t, err)
	defer connection.Close()
	_, err = connection.ExecContext(ctx, "SET SESSION innodb_lock_wait_timeout = 1")
	require.NoError(t, err)
	const replaceLease = "UPDATE t_thread SET lease_token = 'new' WHERE thread_id = 1 AND lease_token = 'old'"
	_, err = connection.ExecContext(ctx, replaceLease)
	var mysqlErr *drivermysql.MySQLError
	require.ErrorAs(t, err, &mysqlErr)
	require.Equal(t, uint16(1205), mysqlErr.Number, "lease replacement must wait for the event transaction's row lock")

	generator.proceed <- struct{}{}
	select {
	case err = <-appendDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("append did not finish after releasing the event ID generator")
	}
	updated, err := connection.ExecContext(ctx, replaceLease)
	require.NoError(t, err)
	affected, err := updated.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)

	svc = NewEventLog(db, db, &stubIDGen{ids: []int64{102}})
	_, _, err = svc.AppendEvents(ctx, request)
	require.ErrorIs(t, err, ErrLeaseMismatch)
	var count int64
	require.NoError(t, db.WithContext(ctx).Model(&model.TEventLog{}).Count(&count).Error)
	require.Equal(t, int64(1), count, "only the event accepted before lease replacement may remain")
}

type leaseLockIDGenerator struct {
	entered chan struct{}
	proceed chan struct{}
}

func (g *leaseLockIDGenerator) NextID(ctx context.Context) (id int64, err error) {
	close(g.entered)
	select {
	case <-g.proceed:
		return 101, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func newMySQLLeaseTestDB(t *testing.T) (db *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("COORDINATOR_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set COORDINATOR_MYSQL_TEST_DSN to a disposable local MySQL server account with CREATE/DROP DATABASE permission")
	}
	config, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid MySQL test DSN")
	}
	config.DBName = ""
	config.ParseTime = true
	config.Timeout, config.ReadTimeout, config.WriteTimeout = 3*time.Second, 5*time.Second, 5*time.Second
	admin, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal("could not initialize MySQL test connection")
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = admin.PingContext(ctx); err != nil {
		t.Fatal("MySQL test server unavailable; connection details omitted")
	}
	databaseName := fmt.Sprintf("codex_lease_test_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE DATABASE `"+databaseName+"`")
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, cleanupErr := admin.ExecContext(cleanupCtx, "DROP DATABASE `"+databaseName+"`")
		if cleanupErr != nil {
			t.Errorf("failed to remove test-only database %s: %v", databaseName, cleanupErr)
		}
	})
	config.DBName = databaseName
	db, err = gorm.Open(gormmysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("could not connect to isolated MySQL test database")
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Set("gorm:table_options", "ENGINE=InnoDB").AutoMigrate(&model.TThread{}, &model.TEventLog{}))
	return db
}
