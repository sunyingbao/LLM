package checkpointer

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// GenerateThreadID 生成 8 字符的线程 ID
// 用于标识一个会话
func GenerateThreadID() string {
	return uuid.New().String()[:8]
}

// GenerateCheckpointID 生成完整的 checkpoint ID
// 格式: {threadID}-v{version}
func GenerateCheckpointID(threadID string, version int) string {
	return fmt.Sprintf("%s-v%d", threadID, version)
}

// GenerateUUID 生成完整的 UUID
func GenerateUUID() string {
	return uuid.New().String()
}

// ExtendedStore 扩展的存储接口
// 包含 eino CheckpointStore 接口以及列表和删除功能
type ExtendedStore interface {
	// Get 获取 checkpoint 数据
	// 返回值: data, existed, error
	Get(ctx context.Context, checkpointID string) ([]byte, bool, error)

	// Set 保存 checkpoint 数据
	Set(ctx context.Context, checkpointID string, checkpoint []byte) error

	// Delete 删除指定 checkpoint
	Delete(ctx context.Context, checkpointID string) error

	// List 列出所有 checkpoint ID
	List(ctx context.Context) ([]string, error)
}

// Clearable 可清空接口（用于测试）
type Clearable interface {
	Clear(ctx context.Context) error
}
