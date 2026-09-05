package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"code.byted.org/gopkg/logs/v2"
	redisv6 "code.byted.org/kv/redis-v6"
)

const (
	defaultReadTimeout  = 500 * time.Millisecond
	defaultWriteTimeout = 500 * time.Millisecond
	initRetryCount      = 3
	initRetryWait       = 100 * time.Millisecond
)

type Config struct {
	Addr         string
	Password     string
	DB           int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type Client interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte) error
	Del(ctx context.Context, keys ...string) (int64, error)
	IncrBy(ctx context.Context, key string, value int64) (int64, error)
}

type abaseClient struct {
	direct *redisv6.Client
}

var clientCache sync.Map

func New(cfg Config) (Client, error) {
	cfg = cfg.withDefaults()
	if strings.TrimSpace(cfg.Addr) == "" {
		return nil, fmt.Errorf("redis addr is empty")
	}
	cacheKey := cfg.cacheKey()
	if cli, ok := clientCache.Load(cacheKey); ok {
		return cli.(*abaseClient), nil
	}

	var err error
	for i := 0; i < initRetryCount; i++ {
		cli := redisv6.NewClient(&redisv6.Options{
			Addr:         cfg.Addr,
			Password:     cfg.Password,
			DB:           cfg.DB,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
		})
		err = cli.Ping().Err()
		if err == nil {
			wrapped := &abaseClient{direct: cli}
			actual, _ := clientCache.LoadOrStore(cacheKey, wrapped)
			return actual.(*abaseClient), nil
		}
		time.Sleep(initRetryWait)
	}
	logs.Error("redis init failed, addr=%s err=%v", cfg.Addr, err)
	return nil, err
}

func (c *abaseClient) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	raw, err := c.direct.WithContext(ctx).Get(key).Result()
	if errors.Is(err, redisv6.Nil) {
		return nil, false, nil
	}
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=get key=%s err=%v", key, err)
		return nil, false, err
	}
	return []byte(raw), true, nil
}

func (c *abaseClient) Set(ctx context.Context, key string, value []byte) (err error) {
	if err = c.direct.WithContext(ctx).Set(key, string(value), 0).Err(); err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=set key=%s err=%v", key, err)
		return err
	}
	return nil
}

func (c *abaseClient) Del(ctx context.Context, keys ...string) (count int64, err error) {
	if len(keys) == 0 {
		return 0, nil
	}
	count, err = c.direct.WithContext(ctx).Del(keys...).Result()
	if err != nil && !errors.Is(err, redisv6.Nil) {
		logs.CtxError(ctx, "[redis] command failed, op=del key_count=%d err=%v", len(keys), err)
	}
	return count, err
}

func (c *abaseClient) IncrBy(ctx context.Context, key string, value int64) (total int64, err error) {
	total, err = c.direct.WithContext(ctx).IncrBy(key, value).Result()
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=incrby key=%s err=%v", key, err)
		return 0, err
	}
	return total, nil
}

func (cfg Config) withDefaults() Config {
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = defaultReadTimeout
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = defaultWriteTimeout
	}
	return cfg
}

func (cfg Config) cacheKey() string {
	return fmt.Sprintf("%s|%s|%d|%d|%d", cfg.Addr, cfg.Password, cfg.DB, cfg.ReadTimeout, cfg.WriteTimeout)
}
