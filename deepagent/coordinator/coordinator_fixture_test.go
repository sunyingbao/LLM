package coordinator

import (
	"context"
	"eino-cli/deepagent/coordinator/internal/infra/idgen"
	redisstore "eino-cli/deepagent/coordinator/internal/infra/store/redis"
	"eino-cli/deepagent/coordinator/internal/model"
	"eino-cli/deepagent/coordinator/internal/storage"
	"eino-cli/deepagent/coordinator/internal/util"
	"strings"
	"time"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/lang/gg/choose"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type coordinatorTestOption func(*Coordinator)

func newTestCoordinator(writeDB *gorm.DB, readDB *gorm.DB, redisClient redisstore.Client, generator idgen.Generator, opts ...coordinatorTestOption) (coordinator *Coordinator) {
	coordinator = newCoordinator(writeDB, readDB, redisClient, generator)
	for _, opt := range opts {
		opt(coordinator)
	}
	return coordinator
}

func (c *Coordinator) registerTestNamespace(ctx context.Context, namespace string, description string, createdBy string, metadata map[string]string) (*model.TAgentNamespace, error) {
	namespaceID, err := c.idgen.NextID(ctx)
	if err != nil {
		return nil, err
	}
	metadataJSON := choose.If(len(metadata) == 0, "{}", util.ToString(metadata))
	now := c.now()
	row := &model.TAgentNamespace{
		NamespaceId:  namespaceID,
		Namespace:    namespace,
		Description:  description,
		CreatedBy:    choose.If(createdBy != "", createdBy, defaultCreatedByValue),
		MetadataJson: metadataJSON,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	result := c.writeDB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "namespace"}},
		DoNothing: true,
	}).Create(row)
	if result.Error != nil {
		logs.CtxError(ctx, "[registerTestNamespace] insert namespace failed, namespace=%s err=%v", namespace, result.Error)
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		logs.CtxInfo(ctx, "[registerTestNamespace] namespace already exists, namespace=%s", namespace)
		// 冲突行可能刚由并发注册写入主库，从库回读会在主从延迟窗口内 not found，走主库。
		existing, err := storage.FindNamespace(ctx, c.writeDB, namespace)
		if err != nil {
			return nil, err
		}
		return existing, nil
	}
	logs.CtxInfo(ctx, "[registerTestNamespace] namespace registered, namespace=%s namespace_id=%d", namespace, namespaceID)
	return row, nil
}

func (c *Coordinator) createTestThread(ctx context.Context, namespace string, env string, userID int64, title string, metadata map[string]string, initialMessage *InitialMessage) (*model.TThread, *model.TMailboxMessage, error) {
	return c.createThreadRow(ctx, namespace, env, userID, "", title, metadata, nil, initialMessage)
}

func WithClock(now func() time.Time) coordinatorTestOption {
	return func(c *Coordinator) {
		c.now = now
	}
}

func WithLeaseTokenGenerator(fn func() string) coordinatorTestOption {
	return func(c *Coordinator) {
		c.newLeaseToken = fn
	}
}

// WithFailureReleaseBackoff 覆盖失败退避时长；传 0 关闭退避（release 后立即可重扫）。
func WithFailureReleaseBackoff(backoff time.Duration) coordinatorTestOption {
	return func(c *Coordinator) {
		c.failureBackoff = backoff
	}
}

// WithFailureReleaseReasons 覆盖失败 reason 白名单（精确匹配，大小写不敏感）。
func WithFailureReleaseReasons(reasons []string) coordinatorTestOption {
	return func(c *Coordinator) {
		set := make(map[string]struct{}, len(reasons))
		for _, reason := range reasons {
			set[strings.ToLower(strings.TrimSpace(reason))] = struct{}{}
		}
		c.failureReasons = set
	}
}

func WithScanLimit(scanLimit int32) coordinatorTestOption {
	return func(c *Coordinator) {
		if scanLimit > 0 {
			c.defaultScanLimit = scanLimit
			c.maxScanLimit = scanLimit
		}
	}
}
