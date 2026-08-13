package checkpointer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileStore 文件系统实现，适用于本地开发和持久化场景
type FileStore struct {
	mu  sync.RWMutex
	dir string
}

// NewFileStore 创建文件存储
// dir: 存储目录，如果不存在会自动创建
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

// Get 获取 checkpoint 数据
// 返回值: data, existed, error
func (s *FileStore) Get(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := s.getFilePath(checkpointID)
	data, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Set 保存 checkpoint 数据
func (s *FileStore) Set(ctx context.Context, checkpointID string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getFilePath(checkpointID)
	return os.WriteFile(filePath, data, 0644)
}

// Delete 删除指定 checkpoint
func (s *FileStore) Delete(ctx context.Context, checkpointID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getFilePath(checkpointID)
	err := os.Remove(filePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// List 列出所有 checkpoint ID
func (s *FileStore) List(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	ids := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".bin") {
			id := strings.TrimSuffix(entry.Name(), ".bin")
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Clear 清空所有 checkpoint（扩展方法，用于测试）
func (s *FileStore) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".bin") {
			filePath := filepath.Join(s.dir, entry.Name())
			if err := os.Remove(filePath); err != nil {
				return err
			}
		}
	}
	return nil
}

// Dir 返回存储目录
func (s *FileStore) Dir() string {
	return s.dir
}

// getFilePath 获取 checkpoint 文件路径
// 对 checkpointID 进行安全处理，避免路径遍历攻击
func (s *FileStore) getFilePath(checkpointID string) string {
	// 使用 filepath.Base 确保 ID 不包含路径分隔符
	safeID := filepath.Base(checkpointID)
	// 替换可能导致问题的字符
	safeID = strings.ReplaceAll(safeID, "..", "_")
	return filepath.Join(s.dir, safeID+".bin")
}
