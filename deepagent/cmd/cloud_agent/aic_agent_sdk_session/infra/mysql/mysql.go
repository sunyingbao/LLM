package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"code.byted.org/gorm/bytedgorm"
	_ "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Config struct {
	PSM         string
	DBName      string
	DSN         string
	ReadTimeout time.Duration
}

func Open(ctx context.Context, cfg Config) (*sql.DB, error) {
	var (
		db  *sql.DB
		err error
	)
	if strings.TrimSpace(cfg.DSN) != "" {
		db, err = openDSN(cfg)
	} else {
		db, err = openPSM(cfg)
	}
	if err != nil {
		return nil, err
	}
	if err := ping(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func openDSN(cfg Config) (*sql.DB, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("mysql dsn is required")
	}
	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func openPSM(cfg Config) (*sql.DB, error) {
	if strings.TrimSpace(cfg.PSM) == "" || strings.TrimSpace(cfg.DBName) == "" {
		return nil, fmt.Errorf("mysql dsn or mysql psm + db_name is required")
	}
	gormDB, err := gorm.Open(
		bytedgorm.MySQL(cfg.PSM, cfg.DBName).WithReadReplicas(),
		&gorm.Config{
			SkipDefaultTransaction: true,
			PrepareStmt:            true,
		},
		bytedgorm.WithDefaults(),
		bytedgorm.Logger{IgnoreRecordNotFoundError: true, LogLevel: logger.Error},
		bytedgorm.WithStressTestSupport(),
	)
	if err != nil {
		return nil, err
	}
	db, err := gormDB.DB()
	if err != nil {
		return nil, err
	}
	return db, nil
}

func ping(ctx context.Context, db *sql.DB) error {
	db.SetMaxOpenConns(64)
	db.SetMaxIdleConns(16)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	return nil
}
