package mysql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

const defaultReadTimeout = 5 * time.Second

type Config struct {
	DSN         string
	ReadDSN     string
	ReadTimeout time.Duration
}

type Client struct {
	cfg Config
	db  *gorm.DB
}

func New(cfg Config) *Client {
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = defaultReadTimeout
	}
	return &Client{cfg: cfg}
}

func (c *Client) Open(ctx context.Context) (*gorm.DB, error) {
	if c == nil {
		return nil, fmt.Errorf("mysql client is nil")
	}
	if c.db != nil {
		return c.db, nil
	}
	db, err := open(c.cfg)
	if err != nil {
		return nil, err
	}
	if err := ping(ctx, db); err != nil {
		return nil, err
	}
	c.db = db
	return db, nil
}

func (c *Client) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	sqlDB, err := c.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func open(cfg Config) (*gorm.DB, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("mysql dsn is empty")
	}
	return openWithDSN(cfg)
}

func openWithDSN(cfg Config) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{
		SkipDefaultTransaction: true,
		PrepareStmt:            true,
		TranslateError:         true,
	})
	if err != nil {
		logs.Error("mysql open failed, dsn_configured=%t err=%v", cfg.DSN != "", err)
		return nil, err
	}
	if strings.TrimSpace(cfg.ReadDSN) != "" {
		readDB, err := gorm.Open(mysql.Open(cfg.ReadDSN), &gorm.Config{SkipDefaultTransaction: true, PrepareStmt: true})
		if err != nil {
			return nil, err
		}
		if err := db.Use(dbresolver.Register(dbresolver.Config{Replicas: []gorm.Dialector{readDB.Dialector}})); err != nil {
			return nil, err
		}
	}
	return db, nil
}

func ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("mysql db handle: %w", err)
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("mysql ping: %w", err)
	}
	return nil
}
