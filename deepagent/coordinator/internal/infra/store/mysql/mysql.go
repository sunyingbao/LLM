package mysql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"code.byted.org/gopkg/logs/v2"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const defaultReadTimeout = 5 * time.Second

type Config struct {
	DSN         string
	ReadDSN     string
	ReadTimeout time.Duration
}

type Client struct {
	cfg   Config
	onceW sync.Once
	conW  *gorm.DB
	errW  error
	onceR sync.Once
	conR  *gorm.DB
	errR  error
}

func New(cfg Config) *Client {
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = defaultReadTimeout
	}
	return &Client{cfg: cfg}
}

func (c *Client) ForWrite() *gorm.DB {
	c.onceW.Do(func() {
		c.conW, c.errW = open(c.cfg, false)
	})
	if c.errW != nil {
		panic(c.errW)
	}
	return c.conW
}

func (c *Client) ForReadOnly() *gorm.DB {
	c.onceR.Do(func() {
		c.conR, c.errR = open(c.cfg, true)
	})
	if c.errR != nil {
		panic(c.errR)
	}
	return c.conR
}

func (c *Client) Ping(ctx context.Context) error {
	if err := pingDB(ctx, "write", c.ForWrite()); err != nil {
		return err
	}
	if err := pingDB(ctx, "readonly", c.ForReadOnly()); err != nil {
		return err
	}
	return nil
}

func IsDuplicatedKeyError(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey)
}

func open(cfg Config, readOnly bool) (*gorm.DB, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("mysql dsn is required")
	}
	return openWithDSN(cfg, readOnly)
}

func pingDB(ctx context.Context, role string, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("mysql %s db handle: %w", role, err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("mysql %s ping: %w", role, err)
	}
	return nil
}

func openWithDSN(cfg Config, readOnly bool) (*gorm.DB, error) {
	dsn := cfg.DSN
	if readOnly && strings.TrimSpace(cfg.ReadDSN) != "" {
		dsn = cfg.ReadDSN
	}

	dbCli, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		SkipDefaultTransaction: true,
		PrepareStmt:            true,
		TranslateError:         true,
		Logger:                 logger.Default.LogMode(logger.Error),
		NamingStrategy:         schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		logs.Error("mysql open failed, readonly=%v", readOnly)
		return nil, err
	}
	return dbCli, nil
}
