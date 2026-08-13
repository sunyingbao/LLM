// Package sandbox 提供沙箱执行环境的接口和实现
package sandbox

import (
	"context"
	"time"
)

// Sandbox 沙箱执行环境接口
// 提供安全隔离的代码执行环境
type Sandbox interface {
	// Create 创建沙箱环境
	Create(ctx context.Context, config *SandboxConfig) error

	// Execute 在沙箱中执行命令
	Execute(ctx context.Context, command string) (*ExecuteResult, error)

	// Upload 上传文件到沙箱
	Upload(ctx context.Context, localPath, remotePath string) error

	// Download 从沙箱下载文件
	Download(ctx context.Context, remotePath, localPath string) error

	// WriteFile 直接写入内容到沙箱中的文件
	WriteFile(ctx context.Context, remotePath string, content []byte) error

	// ReadFile 读取沙箱中的文件内容
	ReadFile(ctx context.Context, remotePath string) ([]byte, error)

	// Status 获取沙箱状态
	Status(ctx context.Context) (*SandboxStatus, error)

	// Close 关闭并销毁沙箱
	Close(ctx context.Context) error
}

// SandboxConfig 沙箱配置
type SandboxConfig struct {
	// Image 容器镜像名称
	Image string

	// Env 环境变量
	Env map[string]string

	// SetupScript 初始化脚本，在沙箱创建后执行
	SetupScript string

	// Timeout 默认命令执行超时时间
	Timeout time.Duration

	// WorkDir 工作目录
	WorkDir string

	// Memory 内存限制（字节）
	Memory int64

	// CPUs CPU 限制（核数）
	CPUs float64

	// Network 是否启用网络
	Network bool

	// Volumes 挂载卷配置 host:container
	Volumes []string

	// Labels 标签
	Labels map[string]string

	// Metadata 附加元数据
	Metadata map[string]interface{}
}

// ExecuteResult 命令执行结果
type ExecuteResult struct {
	// ExitCode 退出码
	ExitCode int `json:"exit_code"`

	// Stdout 标准输出
	Stdout string `json:"stdout"`

	// Stderr 标准错误
	Stderr string `json:"stderr"`

	// Duration 执行耗时
	Duration time.Duration `json:"duration"`

	// Error 执行错误（如果有）
	Error string `json:"error,omitempty"`

	// Killed 是否被终止
	Killed bool `json:"killed,omitempty"`

	// Timeout 是否超时
	Timeout bool `json:"timeout,omitempty"`
}

// SandboxStatus 沙箱状态
type SandboxStatus struct {
	// ID 沙箱 ID
	ID string `json:"id"`

	// State 状态：creating, running, stopped, error
	State string `json:"state"`

	// Image 使用的镜像
	Image string `json:"image"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`

	// StartedAt 启动时间
	StartedAt time.Time `json:"started_at,omitempty"`

	// Error 错误信息
	Error string `json:"error,omitempty"`

	// Metadata 附加信息
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// SandboxState 沙箱状态常量
const (
	StateCreating = "creating"
	StateRunning  = "running"
	StateStopped  = "stopped"
	StateError    = "error"
)

// SandboxType 沙箱类型
type SandboxType string

const (
	TypeDocker  SandboxType = "docker"
	TypeModal   SandboxType = "modal"
	TypeRunloop SandboxType = "runloop"
	TypeDaytona SandboxType = "daytona"
	TypeLocal   SandboxType = "local"
)

// SandboxFactory 沙箱工厂接口
type SandboxFactory interface {
	// Type 返回沙箱类型
	Type() SandboxType

	// Create 创建沙箱实例
	Create(ctx context.Context, config *SandboxConfig) (Sandbox, error)
}

// SandboxOptions 创建沙箱的选项
type SandboxOptions struct {
	// Type 沙箱类型
	Type SandboxType

	// Config 沙箱配置
	Config *SandboxConfig

	// DockerOptions Docker 特定选项
	DockerOptions *DockerOptions

	// ModalOptions Modal 特定选项
	ModalOptions *ModalOptions

	// RunloopOptions Runloop 特定选项
	RunloopOptions *RunloopOptions

	// DaytonaOptions Daytona 特定选项
	DaytonaOptions *DaytonaOptions
}

// DockerOptions Docker 沙箱选项
type DockerOptions struct {
	// Host Docker 守护进程地址
	Host string

	// TLSConfig TLS 配置
	TLSVerify bool
	CertPath  string

	// Registry 镜像仓库配置
	RegistryAuth string
}

// ModalOptions Modal 沙箱选项
type ModalOptions struct {
	// TokenID Modal Token ID
	TokenID string

	// TokenSecret Modal Token Secret
	TokenSecret string

	// AppName 应用名称
	AppName string
}

// RunloopOptions Runloop 沙箱选项
type RunloopOptions struct {
	// APIKey Runloop API Key
	APIKey string

	// BaseURL Runloop API Base URL
	BaseURL string
}

// DaytonaOptions Daytona 沙箱选项
type DaytonaOptions struct {
	// APIKey Daytona API Key
	APIKey string

	// BaseURL Daytona API Base URL
	BaseURL string

	// WorkspaceID 工作空间 ID
	WorkspaceID string
}

// DefaultSandboxConfig 返回默认沙箱配置
func DefaultSandboxConfig() *SandboxConfig {
	return &SandboxConfig{
		Image:   "python:3.11-slim",
		Timeout: 5 * time.Minute,
		WorkDir: "/workspace",
		Memory:  512 * 1024 * 1024, // 512MB
		CPUs:    1.0,
		Network: true,
		Env:     make(map[string]string),
		Labels:  make(map[string]string),
	}
}

// MergeConfig 合并配置
func MergeConfig(base, override *SandboxConfig) *SandboxConfig {
	if base == nil {
		base = DefaultSandboxConfig()
	}
	if override == nil {
		return base
	}

	result := *base

	if override.Image != "" {
		result.Image = override.Image
	}
	if override.SetupScript != "" {
		result.SetupScript = override.SetupScript
	}
	if override.Timeout > 0 {
		result.Timeout = override.Timeout
	}
	if override.WorkDir != "" {
		result.WorkDir = override.WorkDir
	}
	if override.Memory > 0 {
		result.Memory = override.Memory
	}
	if override.CPUs > 0 {
		result.CPUs = override.CPUs
	}
	result.Network = override.Network

	// 合并环境变量
	if result.Env == nil {
		result.Env = make(map[string]string)
	}
	for k, v := range override.Env {
		result.Env[k] = v
	}

	// 合并标签
	if result.Labels == nil {
		result.Labels = make(map[string]string)
	}
	for k, v := range override.Labels {
		result.Labels[k] = v
	}

	// 合并卷
	result.Volumes = append(result.Volumes, override.Volumes...)

	return &result
}
