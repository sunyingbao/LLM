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
	"github.com/bytedance/sonic"
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
	StructGet(ctx context.Context, key string, v interface{}) error
	StructGetPrimary(ctx context.Context, key string, v interface{}) error
	StructMGet(ctx context.Context, keys []string, values interface{}) error
	StructMGetPrimary(ctx context.Context, keys []string, values interface{}) error
	StructSet(ctx context.Context, key string, v interface{}) error
	StructSetTTL(ctx context.Context, key string, v interface{}, ttl time.Duration) error
	StructMSet(ctx context.Context, keys []string, values []interface{}) error
	GetInt64Primary(ctx context.Context, key string) (int64, error)
	Incr(ctx context.Context, key string) (int64, error)
	ZAdd(ctx context.Context, key string, score float64, member string) error
	ZRange(ctx context.Context, key string, start int64, stop int64) ([]string, error)
	ZRangePrimary(ctx context.Context, key string, start int64, stop int64) ([]string, error)
	ZRangeByScore(ctx context.Context, key string, rangeOpt redisv6.ZRangeBy) ([]string, error)
	ZRangeByScorePrimary(ctx context.Context, key string, rangeOpt redisv6.ZRangeBy) ([]string, error)
	ZCard(ctx context.Context, key string) (int64, error)
	ZCardPrimary(ctx context.Context, key string) (int64, error)
	ZScore(ctx context.Context, key string, member string) (float64, error)
	ZScorePrimary(ctx context.Context, key string, member string) (float64, error)
	ZRem(ctx context.Context, key string, members []interface{}) (int64, error)
	Del(ctx context.Context, keys ...string) (int64, error)
}

type abaseClient struct {
	direct *redisv6.Client
}

type commandClient interface {
	Get(key string) *redisv6.StringCmd
	MGet(keys ...string) *redisv6.SliceCmd
	Set(key string, value interface{}, expiration time.Duration) *redisv6.StatusCmd
	MSet(pairs ...interface{}) *redisv6.StatusCmd
	Incr(key string) *redisv6.IntCmd
	ZAdd(key string, members ...redisv6.Z) *redisv6.IntCmd
	ZRange(key string, start, stop int64) *redisv6.StringSliceCmd
	ZRangeByScore(key string, opt redisv6.ZRangeBy) *redisv6.StringSliceCmd
	ZCard(key string) *redisv6.IntCmd
	ZScore(key string, member string) *redisv6.FloatCmd
	ZRem(key string, members ...interface{}) *redisv6.IntCmd
	Del(keys ...string) *redisv6.IntCmd
}

var (
	clientCache sync.Map
)

func New(cfg Config) (Client, error) {
	cfg = cfg.withDefaults()
	if strings.TrimSpace(cfg.Addr) == "" {
		return nil, fmt.Errorf("redis address is required")
	}
	cacheKey := cfg.cacheKey()
	if cli, ok := clientCache.Load(cacheKey); ok {
		return cli.(*abaseClient), nil
	}

	var (
		err error
	)
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

func (c *abaseClient) readContext(ctx context.Context) commandClient {
	return c.direct.WithContext(ctx)
}

func (c *abaseClient) primaryContext(ctx context.Context) commandClient {
	return c.direct.WithContext(ctx)
}

func (c *abaseClient) StructGet(ctx context.Context, key string, v interface{}) error {
	return c.structGet(ctx, c.readContext(ctx), "get", key, v)
}

func (c *abaseClient) StructGetPrimary(ctx context.Context, key string, v interface{}) error {
	return c.structGet(ctx, c.primaryContext(ctx), "get_primary", key, v)
}

func (c *abaseClient) structGet(ctx context.Context, cli commandClient, op string, key string, v interface{}) error {
	raw, err := cli.Get(key).Result()
	if err != nil {
		logRedisError(ctx, op, key, err)
		return err
	}
	if err := sonic.UnmarshalString(raw, v); err != nil {
		logs.CtxError(ctx, "[redis] unmarshal failed, op=%s key=%s err=%v", op, key, err)
		return err
	}
	return nil
}

func (c *abaseClient) StructMGet(ctx context.Context, keys []string, values interface{}) error {
	return c.structMGet(ctx, c.readContext(ctx), "mget", keys, values)
}

func (c *abaseClient) StructMGetPrimary(ctx context.Context, keys []string, values interface{}) error {
	return c.structMGet(ctx, c.primaryContext(ctx), "mget_primary", keys, values)
}

func (c *abaseClient) structMGet(ctx context.Context, cli commandClient, op string, keys []string, values interface{}) error {
	if len(keys) == 0 {
		return nil
	}
	items, err := cli.MGet(keys...).Result()
	if err != nil {
		logRedisKeysError(ctx, op, keys, err)
		return err
	}
	payloads := make([]string, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case nil:
			payloads = append(payloads, "null")
		case string:
			payloads = append(payloads, value)
		case []byte:
			payloads = append(payloads, string(value))
		default:
			err := fmt.Errorf("unexpected redis mget value type %T", item)
			logRedisKeysError(ctx, op+"_decode", keys, err)
			return err
		}
	}
	if err := sonic.UnmarshalString("["+strings.Join(payloads, ",")+"]", values); err != nil {
		logRedisKeysError(ctx, op+"_unmarshal", keys, err)
		return err
	}
	return nil
}

func (c *abaseClient) StructSet(ctx context.Context, key string, v interface{}) error {
	return c.structSet(ctx, key, v, 0)
}

func (c *abaseClient) StructSetTTL(ctx context.Context, key string, v interface{}, ttl time.Duration) error {
	return c.structSet(ctx, key, v, ttl)
}

func (c *abaseClient) structSet(ctx context.Context, key string, v interface{}, ttl time.Duration) error {
	raw, err := sonic.Marshal(v)
	if err != nil {
		logs.CtxError(ctx, "[redis] marshal failed, op=set key=%s err=%v", key, err)
		return err
	}
	_, err = c.primaryContext(ctx).Set(key, string(raw), ttl).Result()
	if err != nil {
		logRedisError(ctx, "set", key, err)
	}
	return err
}

func (c *abaseClient) StructMSet(ctx context.Context, keys []string, values []interface{}) error {
	if len(keys) != len(values) {
		return fmt.Errorf("keys and values length mismatch")
	}
	if len(keys) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(keys)*2)
	for idx := range keys {
		raw, err := sonic.Marshal(values[idx])
		if err != nil {
			logRedisKeysError(ctx, "mset_marshal", keys, err)
			return err
		}
		args = append(args, keys[idx], string(raw))
	}
	if err := c.primaryContext(ctx).MSet(args...).Err(); err != nil {
		logRedisKeysError(ctx, "mset", keys, err)
		return err
	}
	return nil
}

func (c *abaseClient) GetInt64Primary(ctx context.Context, key string) (int64, error) {
	raw, err := c.primaryContext(ctx).Get(key).Int64()
	if errors.Is(err, redisv6.Nil) {
		return 0, nil
	}
	if err != nil {
		logRedisError(ctx, "get_int64_primary", key, err)
		return 0, err
	}
	return raw, nil
}

func (c *abaseClient) Incr(ctx context.Context, key string) (int64, error) {
	result, err := c.primaryContext(ctx).Incr(key).Result()
	if err != nil {
		logRedisError(ctx, "incr", key, err)
	}
	return result, err
}

func (c *abaseClient) ZAdd(ctx context.Context, key string, score float64, member string) error {
	err := c.primaryContext(ctx).ZAdd(key, redisv6.Z{
		Score:  score,
		Member: member,
	}).Err()
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=zadd key=%s score=%f member=%s err=%v", key, score, member, err)
	}
	return err
}

func (c *abaseClient) ZRange(ctx context.Context, key string, start int64, stop int64) ([]string, error) {
	return c.zRange(ctx, c.readContext(ctx), "zrange", key, start, stop)
}

func (c *abaseClient) ZRangePrimary(ctx context.Context, key string, start int64, stop int64) ([]string, error) {
	return c.zRange(ctx, c.primaryContext(ctx), "zrange_primary", key, start, stop)
}

func (c *abaseClient) zRange(ctx context.Context, cli commandClient, op string, key string, start int64, stop int64) ([]string, error) {
	result, err := cli.ZRange(key, start, stop).Result()
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=%s key=%s start=%d stop=%d err=%v", op, key, start, stop, err)
	}
	return result, err
}

func (c *abaseClient) ZRangeByScore(ctx context.Context, key string, rangeOpt redisv6.ZRangeBy) ([]string, error) {
	return c.zRangeByScore(ctx, c.readContext(ctx), "zrangebyscore", key, rangeOpt)
}

func (c *abaseClient) ZRangeByScorePrimary(ctx context.Context, key string, rangeOpt redisv6.ZRangeBy) ([]string, error) {
	return c.zRangeByScore(ctx, c.primaryContext(ctx), "zrangebyscore_primary", key, rangeOpt)
}

func (c *abaseClient) zRangeByScore(ctx context.Context, cli commandClient, op string, key string, rangeOpt redisv6.ZRangeBy) ([]string, error) {
	result, err := cli.ZRangeByScore(key, rangeOpt).Result()
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=%s key=%s min=%s max=%s offset=%d count=%d err=%v", op, key, rangeOpt.Min, rangeOpt.Max, rangeOpt.Offset, rangeOpt.Count, err)
	}
	return result, err
}

func (c *abaseClient) ZCard(ctx context.Context, key string) (int64, error) {
	return c.zCard(ctx, c.readContext(ctx), "zcard", key)
}

func (c *abaseClient) ZCardPrimary(ctx context.Context, key string) (int64, error) {
	return c.zCard(ctx, c.primaryContext(ctx), "zcard_primary", key)
}

func (c *abaseClient) zCard(ctx context.Context, cli commandClient, op string, key string) (int64, error) {
	result, err := cli.ZCard(key).Result()
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=%s key=%s err=%v", op, key, err)
	}
	return result, err
}

func (c *abaseClient) ZScore(ctx context.Context, key string, member string) (float64, error) {
	return c.zScore(ctx, c.readContext(ctx), "zscore", key, member)
}

func (c *abaseClient) ZScorePrimary(ctx context.Context, key string, member string) (float64, error) {
	return c.zScore(ctx, c.primaryContext(ctx), "zscore_primary", key, member)
}

func (c *abaseClient) zScore(ctx context.Context, cli commandClient, op string, key string, member string) (float64, error) {
	result, err := cli.ZScore(key, member).Result()
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=%s key=%s member=%s err=%v", op, key, member, err)
	}
	return result, err
}

func (c *abaseClient) ZRem(ctx context.Context, key string, members []interface{}) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	result, err := c.primaryContext(ctx).ZRem(key, members...).Result()
	if err != nil {
		logs.CtxError(ctx, "[redis] command failed, op=zrem key=%s member_count=%d members=%v err=%v", key, len(members), members, err)
	}
	return result, err
}

func (c *abaseClient) Del(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	result, err := c.primaryContext(ctx).Del(keys...).Result()
	if err != nil {
		logRedisKeysError(ctx, "del", keys, err)
	}
	return result, err
}

func logRedisError(ctx context.Context, op string, key string, err error) {
	if err == nil || errors.Is(err, redisv6.Nil) {
		return
	}
	logs.CtxError(ctx, "[redis] command failed, op=%s key=%s err=%v", op, key, err)
}

func logRedisKeysError(ctx context.Context, op string, keys []string, err error) {
	if err == nil || errors.Is(err, redisv6.Nil) {
		return
	}
	logs.CtxError(ctx, "[redis] command failed, op=%s key_count=%d keys=%v err=%v", op, len(keys), keys, err)
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
