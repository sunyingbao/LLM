package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DockerSandbox Docker 沙箱实现
// 使用 Docker CLI 命令而不是 SDK，以减少依赖
type DockerSandbox struct {
	config      *SandboxConfig
	options     *DockerOptions
	containerID string
	status      *SandboxStatus
	mu          sync.RWMutex
}

// DockerSandboxFactory Docker 沙箱工厂
type DockerSandboxFactory struct {
	options *DockerOptions
}

// NewDockerSandboxFactory 创建 Docker 沙箱工厂
func NewDockerSandboxFactory(options *DockerOptions) *DockerSandboxFactory {
	if options == nil {
		options = &DockerOptions{}
	}
	return &DockerSandboxFactory{
		options: options,
	}
}

// Type 返回沙箱类型
func (f *DockerSandboxFactory) Type() SandboxType {
	return TypeDocker
}

// Create 创建 Docker 沙箱
func (f *DockerSandboxFactory) Create(ctx context.Context, config *SandboxConfig) (Sandbox, error) {
	sandbox := &DockerSandbox{
		config:  MergeConfig(nil, config),
		options: f.options,
		status: &SandboxStatus{
			State:     StateCreating,
			CreatedAt: time.Now(),
		},
	}

	if err := sandbox.Create(ctx, config); err != nil {
		return nil, err
	}

	return sandbox, nil
}

// NewDockerSandbox 创建 Docker 沙箱（直接创建，不通过工厂）
func NewDockerSandbox(options *DockerOptions) *DockerSandbox {
	if options == nil {
		options = &DockerOptions{}
	}
	return &DockerSandbox{
		options: options,
		status: &SandboxStatus{
			State:     StateCreating,
			CreatedAt: time.Now(),
		},
	}
}

// Create 创建沙箱环境
func (s *DockerSandbox) Create(ctx context.Context, config *SandboxConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config = MergeConfig(nil, config)
	s.status.Image = s.config.Image

	// 构建 docker run 命令
	args := []string{"run", "-d", "--rm"}

	// 工作目录
	if s.config.WorkDir != "" {
		args = append(args, "-w", s.config.WorkDir)
	}

	// 环境变量
	for k, v := range s.config.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// 内存限制
	if s.config.Memory > 0 {
		args = append(args, "-m", fmt.Sprintf("%d", s.config.Memory))
	}

	// CPU 限制
	if s.config.CPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.2f", s.config.CPUs))
	}

	// 网络
	if !s.config.Network {
		args = append(args, "--network", "none")
	}

	// 挂载卷
	for _, v := range s.config.Volumes {
		args = append(args, "-v", v)
	}

	// 标签
	for k, v := range s.config.Labels {
		args = append(args, "-l", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, "-l", "deepagent-sandbox=true")

	// 镜像和保持容器运行的命令
	args = append(args, s.config.Image, "sleep", "infinity")

	// 执行 docker run
	cmd := s.dockerCommand(ctx, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.status.State = StateError
		s.status.Error = fmt.Sprintf("failed to create container: %v, output: %s", err, string(output))
		return fmt.Errorf("failed to create container: %w", err)
	}

	s.containerID = strings.TrimSpace(string(output))
	s.status.ID = s.containerID
	s.status.State = StateRunning
	s.status.StartedAt = time.Now()

	// 执行初始化脚本
	if s.config.SetupScript != "" {
		result, err := s.executeInternal(ctx, s.config.SetupScript)
		if err != nil || result.ExitCode != 0 {
			// 初始化失败，清理容器
			_ = s.Close(ctx)
			if err != nil {
				return fmt.Errorf("setup script failed: %w", err)
			}
			return fmt.Errorf("setup script failed with exit code %d: %s", result.ExitCode, result.Stderr)
		}
	}

	return nil
}

// Execute 在沙箱中执行命令
func (s *DockerSandbox) Execute(ctx context.Context, command string) (*ExecuteResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.containerID == "" {
		return &ExecuteResult{
			ExitCode: -1,
			Error:    "sandbox not created",
		}, fmt.Errorf("sandbox not created")
	}

	return s.executeInternal(ctx, command)
}

// executeInternal 内部执行命令（不加锁）
func (s *DockerSandbox) executeInternal(ctx context.Context, command string) (*ExecuteResult, error) {
	start := time.Now()

	// 设置超时
	timeout := s.config.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"exec", s.containerID, "sh", "-c", command}
	cmd := s.dockerCommand(ctx, args...)

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
func (s *DockerSandbox) Upload(ctx context.Context, localPath, remotePath string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.containerID == "" {
		return fmt.Errorf("sandbox not created")
	}

	args := []string{"cp", localPath, fmt.Sprintf("%s:%s", s.containerID, remotePath)}
	cmd := s.dockerCommand(ctx, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to upload file: %w, output: %s", err, string(output))
	}

	return nil
}

// Download 从沙箱下载文件
func (s *DockerSandbox) Download(ctx context.Context, remotePath, localPath string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.containerID == "" {
		return fmt.Errorf("sandbox not created")
	}

	// 确保本地目录存在
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}

	args := []string{"cp", fmt.Sprintf("%s:%s", s.containerID, remotePath), localPath}
	cmd := s.dockerCommand(ctx, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to download file: %w, output: %s", err, string(output))
	}

	return nil
}

// WriteFile 直接写入内容到沙箱中的文件
func (s *DockerSandbox) WriteFile(ctx context.Context, remotePath string, content []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.containerID == "" {
		return fmt.Errorf("sandbox not created")
	}

	// 使用 docker exec 和 cat 写入文件
	args := []string{"exec", "-i", s.containerID, "sh", "-c", fmt.Sprintf("cat > %s", remotePath)}
	cmd := s.dockerCommand(ctx, args...)
	cmd.Stdin = bytes.NewReader(content)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to write file: %w, output: %s", err, string(output))
	}

	return nil
}

// ReadFile 读取沙箱中的文件内容
func (s *DockerSandbox) ReadFile(ctx context.Context, remotePath string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.containerID == "" {
		return nil, fmt.Errorf("sandbox not created")
	}

	args := []string{"exec", s.containerID, "cat", remotePath}
	cmd := s.dockerCommand(ctx, args...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return io.ReadAll(&stdout)
}

// Status 获取沙箱状态
func (s *DockerSandbox) Status(ctx context.Context) (*SandboxStatus, error) {
	s.mu.RLock()
	status := *s.status
	s.mu.RUnlock()

	// 如果容器已创建，检查实际状态
	if s.containerID != "" {
		args := []string{"inspect", "--format", "{{.State.Status}}", s.containerID}
		cmd := s.dockerCommand(ctx, args...)
		output, err := cmd.Output()
		if err != nil {
			status.State = StateError
			status.Error = "container not found"
		} else {
			state := strings.TrimSpace(string(output))
			if state == "running" {
				status.State = StateRunning
			} else {
				status.State = StateStopped
			}
		}
	}

	return &status, nil
}

// Close 关闭并销毁沙箱
func (s *DockerSandbox) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.containerID == "" {
		return nil
	}

	// 停止并移除容器
	args := []string{"rm", "-f", s.containerID}
	cmd := s.dockerCommand(ctx, args...)
	_ = cmd.Run() // 忽略错误，因为容器可能已经不存在

	s.status.State = StateStopped
	s.containerID = ""

	return nil
}

// dockerCommand 创建 docker 命令
func (s *DockerSandbox) dockerCommand(ctx context.Context, args ...string) *exec.Cmd {
	var cmdArgs []string

	// 添加 host 参数
	if s.options != nil && s.options.Host != "" {
		cmdArgs = append(cmdArgs, "-H", s.options.Host)
	}

	// 添加 TLS 参数
	if s.options != nil && s.options.TLSVerify {
		cmdArgs = append(cmdArgs, "--tlsverify")
		if s.options.CertPath != "" {
			cmdArgs = append(cmdArgs, "--tlscacert", filepath.Join(s.options.CertPath, "ca.pem"))
			cmdArgs = append(cmdArgs, "--tlscert", filepath.Join(s.options.CertPath, "cert.pem"))
			cmdArgs = append(cmdArgs, "--tlskey", filepath.Join(s.options.CertPath, "key.pem"))
		}
	}

	cmdArgs = append(cmdArgs, args...)

	return exec.CommandContext(ctx, "docker", cmdArgs...)
}

// ContainerID 获取容器 ID
func (s *DockerSandbox) ContainerID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.containerID
}
