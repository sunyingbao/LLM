package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// LocalSandbox 本地沙箱实现
// 直接在本地执行命令，不提供隔离
// 适用于开发和测试环境
type LocalSandbox struct {
	config  *SandboxConfig
	workDir string
	status  *SandboxStatus
	mu      sync.RWMutex
}

// LocalSandboxFactory 本地沙箱工厂
type LocalSandboxFactory struct{}

// NewLocalSandboxFactory 创建本地沙箱工厂
func NewLocalSandboxFactory() *LocalSandboxFactory {
	return &LocalSandboxFactory{}
}

// Type 返回沙箱类型
func (f *LocalSandboxFactory) Type() SandboxType {
	return TypeLocal
}

// Create 创建本地沙箱
func (f *LocalSandboxFactory) Create(ctx context.Context, config *SandboxConfig) (Sandbox, error) {
	sandbox := NewLocalSandbox()
	if err := sandbox.Create(ctx, config); err != nil {
		return nil, err
	}
	return sandbox, nil
}

// NewLocalSandbox 创建本地沙箱
func NewLocalSandbox() *LocalSandbox {
	return &LocalSandbox{
		status: &SandboxStatus{
			State:     StateCreating,
			CreatedAt: time.Now(),
		},
	}
}

// Create 创建沙箱环境
func (s *LocalSandbox) Create(ctx context.Context, config *SandboxConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config = MergeConfig(nil, config)

	// 设置工作目录
	workDir := s.config.WorkDir
	if workDir == "" {
		// 创建临时工作目录
		tmpDir, err := os.MkdirTemp("", "deepagent-sandbox-*")
		if err != nil {
			s.status.State = StateError
			s.status.Error = fmt.Sprintf("failed to create work dir: %v", err)
			return fmt.Errorf("failed to create work dir: %w", err)
		}
		workDir = tmpDir
	} else {
		// 确保目录存在
		if err := os.MkdirAll(workDir, 0755); err != nil {
			s.status.State = StateError
			s.status.Error = fmt.Sprintf("failed to create work dir: %v", err)
			return fmt.Errorf("failed to create work dir: %w", err)
		}
	}

	s.workDir = workDir
	s.status.ID = workDir
	s.status.State = StateRunning
	s.status.StartedAt = time.Now()

	// 执行初始化脚本
	if s.config.SetupScript != "" {
		result, err := s.executeInternal(ctx, s.config.SetupScript)
		if err != nil || result.ExitCode != 0 {
			s.status.State = StateError
			if err != nil {
				s.status.Error = fmt.Sprintf("setup script failed: %v", err)
				return fmt.Errorf("setup script failed: %w", err)
			}
			s.status.Error = fmt.Sprintf("setup script failed with exit code %d", result.ExitCode)
			return fmt.Errorf("setup script failed with exit code %d: %s", result.ExitCode, result.Stderr)
		}
	}

	return nil
}

// Execute 在沙箱中执行命令
func (s *LocalSandbox) Execute(ctx context.Context, command string) (*ExecuteResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.workDir == "" {
		return &ExecuteResult{
			ExitCode: -1,
			Error:    "sandbox not created",
		}, fmt.Errorf("sandbox not created")
	}

	return s.executeInternal(ctx, command)
}

// executeInternal 内部执行命令（不加锁）
func (s *LocalSandbox) executeInternal(ctx context.Context, command string) (*ExecuteResult, error) {
	start := time.Now()

	// 设置超时
	timeout := s.config.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = s.workDir

	// 设置环境变量
	cmd.Env = os.Environ()
	for k, v := range s.config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	result := &ExecuteResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if ctx.Err() == context.DeadlineExceeded {
		result.Timeout = true
		result.ExitCode = -1
		result.Error = "command timed out"
		return result, nil
	}

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
		} else {
			result.ExitCode = -1
			result.Error = err.Error()
		}
	}

	return result, nil
}

// Upload 上传文件到沙箱
func (s *LocalSandbox) Upload(ctx context.Context, localPath, remotePath string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.workDir == "" {
		return fmt.Errorf("sandbox not created")
	}

	// 解析目标路径
	destPath := s.resolvePath(remotePath)

	// 确保目标目录存在
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// 读取源文件
	content, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}

	// 写入目标文件
	if err := os.WriteFile(destPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write destination file: %w", err)
	}

	return nil
}

// Download 从沙箱下载文件
func (s *LocalSandbox) Download(ctx context.Context, remotePath, localPath string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.workDir == "" {
		return fmt.Errorf("sandbox not created")
	}

	// 解析源路径
	srcPath := s.resolvePath(remotePath)

	// 确保目标目录存在
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// 读取源文件
	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}

	// 写入目标文件
	if err := os.WriteFile(localPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write destination file: %w", err)
	}

	return nil
}

// WriteFile 直接写入内容到沙箱中的文件
func (s *LocalSandbox) WriteFile(ctx context.Context, remotePath string, content []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.workDir == "" {
		return fmt.Errorf("sandbox not created")
	}

	destPath := s.resolvePath(remotePath)

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	return os.WriteFile(destPath, content, 0644)
}

// ReadFile 读取沙箱中的文件内容
func (s *LocalSandbox) ReadFile(ctx context.Context, remotePath string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.workDir == "" {
		return nil, fmt.Errorf("sandbox not created")
	}

	srcPath := s.resolvePath(remotePath)
	file, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return io.ReadAll(file)
}

// Status 获取沙箱状态
func (s *LocalSandbox) Status(ctx context.Context) (*SandboxStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := *s.status

	// 检查工作目录是否存在
	if s.workDir != "" {
		if _, err := os.Stat(s.workDir); os.IsNotExist(err) {
			status.State = StateStopped
		}
	}

	return &status, nil
}

// Close 关闭并销毁沙箱
func (s *LocalSandbox) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 如果工作目录是临时创建的，则删除
	if s.workDir != "" && s.config.WorkDir == "" {
		_ = os.RemoveAll(s.workDir)
	}

	s.status.State = StateStopped
	s.workDir = ""

	return nil
}

// resolvePath 解析沙箱内的路径
func (s *LocalSandbox) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		// 绝对路径，映射到工作目录
		return filepath.Join(s.workDir, path)
	}
	return filepath.Join(s.workDir, path)
}

// WorkDir 获取工作目录
func (s *LocalSandbox) WorkDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workDir
}
