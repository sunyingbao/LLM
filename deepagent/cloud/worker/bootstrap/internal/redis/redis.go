package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/kv/goredis"
	redisv6 "code.byted.org/kv/redis-v6"
)

const (
	defaultReadTimeout  = 500 * time.Millisecond
	defaultWriteTimeout = 500 * time.Millisecond
	initRetryCount      = 3
	initRetryWait       = 100 * time.Millisecond
)

type Config struct {
	PSM          string
	PrimaryPSM   string
	MasterPrefer string
	Table        string
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
	goredis        *goredis.Client
	primaryGoredis *goredis.Client
	direct         *redisv6.Client
}

type commandClient interface {
	Get(key string) *redisv6.StringCmd
	Set(key string, value interface{}, expiration time.Duration) *redisv6.StatusCmd
	Del(keys ...string) *redisv6.IntCmd
	IncrBy(key string, value int64) *redisv6.IntCmd
}

var clientCache sync.Map

func New(cfg Config) (Client, error) {
	cfg = cfg.withDefaults()
	if strings.TrimSpace(cfg.PSM) == "" && strings.TrimSpace(cfg.Addr) == "" {
		return nil, fmt.Errorf("redis psm or addr is empty")
	}
	cacheKey := cfg.cacheKey()
	if cli, ok := clientCache.Load(cacheKey); ok {
		return cli.(*abaseClient), nil
	}

	var err error
	for i := 0; i < initRetryCount; i++ {
		if strings.TrimSpace(cfg.Addr) != "" {
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
		} else {
			var cli *goredis.Client
			var primaryCli *goredis.Client
			cli, primaryCli, err = newGoredisClients(cfg)
			if err == nil {
				wrapped := &abaseClient{goredis: cli, primaryGoredis: primaryCli}
				actual, _ := clientCache.LoadOrStore(cacheKey, wrapped)
				return actual.(*abaseClient), nil
			}
		}
		time.Sleep(initRetryWait)
	}
	logs.Error("redis init failed, psm=%s primary_psm=%s addr=%s err=%v", cfg.PSM, cfg.effectivePrimaryPSM(), cfg.Addr, err)
	return nil, err
}

func newGoredisClients(cfg Config) (*goredis.Client, *goredis.Client, error) {
	opt := goredis.NewOption()
	opt.ReadTimeout = cfg.ReadTimeout
	opt.WriteTimeout = cfg.WriteTimeout

	newClient := func(psm string) (*goredis.Client, error) {
		if strings.TrimSpace(cfg.Table) != "" {
			return goredis.NewAbaseClientWithOption(psm, cfg.Table, opt)
		}
		return goredis.NewClientWithOption(psm, opt)
	}

	cli, err := newClient(cfg.PSM)
	if err != nil {
		return nil, nil, fmt.Errorf("create redis read client psm=%s: %w", cfg.PSM, err)
	}
	primaryPSM := cfg.effectivePrimaryPSM()
	if primaryPSM == cfg.PSM {
		return cli, cli, nil
	}
	primaryCli, err := newClient(primaryPSM)
	if err != nil {
		return nil, nil, fmt.Errorf("create redis primary client psm=%s: %w", primaryPSM, err)
	}
	return cli, primaryCli, nil
}

func (c *abaseClient) readContext(ctx context.Context) commandClient {
	if c.direct != nil {
		return c.direct.WithContext(ctx)
	}
	return c.goredis.WithContext(ctx)
}

func (c *abaseClient) primaryContext(ctx context.Context) commandClient {
	if c.direct != nil {
		return c.direct.WithContext(ctx)
	}
	if c.primaryGoredis != nil {
		return c.primaryGoredis.WithContext(ctx)
	}
	return c.goredis.WithContext(ctx)
}

func (c *abaseClient) Get(ctx context.Context, key string) ([]byte, bool, error) {
	raw, err := c.readContext(ctx).Get(key).Result()
	if errors.Is(err, redisv6.Nil) {
		return nil, false, nil
	}
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=get key=%s err=%v", key, err)
		return nil, false, err
	}
	return []byte(raw), true, nil
}

func (c *abaseClient) Set(ctx context.Context, key string, value []byte) error {
	if err := c.primaryContext(ctx).Set(key, string(value), 0).Err(); err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=set key=%s err=%v", key, err)
		return err
	}
	return nil
}

func (c *abaseClient) Del(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	result, err := c.primaryContext(ctx).Del(keys...).Result()
	if err != nil && !errors.Is(err, redisv6.Nil) {
		logs.CtxError(ctx, "[redis] command failed, op=del key_count=%d err=%v", len(keys), err)
	}
	return result, err
}

func (c *abaseClient) IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	result, err := c.primaryContext(ctx).IncrBy(key, value).Result()
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=incrby key=%s err=%v", key, err)
		return 0, err
	}
	return result, nil
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

func (cfg Config) effectivePrimaryPSM() string {
	if strings.TrimSpace(cfg.PrimaryPSM) != "" {
		return strings.TrimSpace(cfg.PrimaryPSM)
	}
	if strings.TrimSpace(cfg.PSM) == "" {
		return ""
	}
	return strings.TrimSpace(cfg.PSM) + "_primary"
}

func (cfg Config) cacheKey() string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d|%d|%d", cfg.PSM, cfg.effectivePrimaryPSM(), cfg.Table, cfg.Addr, cfg.Password, cfg.DB, cfg.ReadTimeout, cfg.WriteTimeout)
}
