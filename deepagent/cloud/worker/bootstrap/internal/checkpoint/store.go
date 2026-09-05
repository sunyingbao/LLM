package checkpoint

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"eino-cli/deepagent/cloud/worker/bootstrap/internal/redis"
	"eino-cli/deepagent/core/checkpointer"
	"github.com/cloudwego/eino/compose"
)

type Factory struct {
	storeURI    string
	redisClient redis.Client
}

func NewFactory(storeURI string, redisClient redis.Client) *Factory {
	return &Factory{storeURI: strings.TrimSpace(storeURI), redisClient: redisClient}
}

func (f *Factory) NewStore(_ context.Context, threadID string) (compose.CheckPointStore, error) {
	if f == nil || f.storeURI == "" {
		return nil, fmt.Errorf("checkpoint store is not configured")
	}
	u, err := url.Parse(f.storeURI)
	if err != nil {
		return nil, fmt.Errorf("parse checkpoint store %q: %w", f.storeURI, err)
	}
	switch u.Scheme {
	case "file":
		root := strings.TrimPrefix(f.storeURI, "file://")
		if root == "" {
			root = u.Path
		}
		if root == "" {
			return nil, fmt.Errorf("file checkpoint store path is empty")
		}
		return checkpointer.NewFileStore(filepath.Join(root, threadID))
	case "redis":
		if f.redisClient == nil {
			return nil, fmt.Errorf("redis checkpoint store requires redis client")
		}
		prefix := strings.TrimPrefix(f.storeURI, "redis://")
		if prefix == "" {
			prefix = "deep_agent_sdk_worker_checkpoint"
		}
		return NewRedisStore(f.redisClient, prefix, threadID), nil
	default:
		return nil, fmt.Errorf("unsupported checkpoint store scheme %q", u.Scheme)
	}
}

type RedisStore struct {
	client   redis.Client
	prefix   string
	threadID string
}

func NewRedisStore(client redis.Client, prefix string, threadID string) *RedisStore {
	return &RedisStore{
		client:   client,
		prefix:   strings.Trim(strings.TrimSpace(prefix), ":/"),
		threadID: strings.TrimSpace(threadID),
	}
}

func (s *RedisStore) Get(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	if s == nil || s.client == nil {
		return nil, false, fmt.Errorf("redis checkpoint store is not initialized")
	}
	return s.client.Get(ctx, s.key(checkpointID))
}

func (s *RedisStore) Set(ctx context.Context, checkpointID string, data []byte) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis checkpoint store is not initialized")
	}
	return s.client.Set(ctx, s.key(checkpointID), data)
}

func (s *RedisStore) key(checkpointID string) string {
	safeID := filepath.Base(checkpointID)
	safeID = strings.ReplaceAll(safeID, "..", "_")
	return fmt.Sprintf("deep_agent_sdk_worker:%s:thread:%s:checkpoint:%s", s.prefix, s.threadID, safeID)
}
