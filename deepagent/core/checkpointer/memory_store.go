// Package checkpointer 提供 eino compose.CheckPointStore 接口的实现
package checkpointer

import (
	"context"
	"sync"
)

// InMemoryStore 内存实现，适用于测试和短期会话
// 进程重启后数据丢失
type InMemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewInMemoryStore 创建内存存储
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		data: make(map[string][]byte),
	}
}

// Get 获取 checkpoint 数据
// 返回值: data, existed, error
func (s *InMemoryStore) Get(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, ok := s.data[checkpointID]
	if !ok {
		return nil, false, nil
	}

	// 返回副本，避免外部修改
	copied := make([]byte, len(data))
	copy(copied, data)
	return copied, true, nil
}

// Set 保存 checkpoint 数据
func (s *InMemoryStore) Set(ctx context.Context, checkpointID string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 存储副本
	copied := make([]byte, len(data))
	copy(copied, data)
	s.data[checkpointID] = copied
	return nil
}

// Delete 删除指定 checkpoint（扩展方法）
func (s *InMemoryStore) Delete(ctx context.Context, checkpointID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, checkpointID)
	return nil
}

// List 列出所有 checkpoint ID（扩展方法）
func (s *InMemoryStore) List(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.data))
	for id := range s.data {
		ids = append(ids, id)
	}
	return ids, nil
}

// Clear 清空所有 checkpoint（扩展方法，用于测试）
func (s *InMemoryStore) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = make(map[string][]byte)
	return nil
}

// Size 返回存储的 checkpoint 数量（扩展方法）
func (s *InMemoryStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}
