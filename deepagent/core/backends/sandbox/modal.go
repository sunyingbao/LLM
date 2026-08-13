package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ModalSandbox Modal 沙箱实现
// Modal 是一个云端代码执行平台
type ModalSandbox struct {
	config    *SandboxConfig
	options   *ModalOptions
	sandboxID string
	status    *SandboxStatus
	client    *http.Client
	mu        sync.RWMutex
}

// ModalSandboxFactory Modal 沙箱工厂
type ModalSandboxFactory struct {
	options *ModalOptions
}

// NewModalSandboxFactory 创建 Modal 沙箱工厂
func NewModalSandboxFactory(options *ModalOptions) *ModalSandboxFactory {
	if options == nil {
		options = &ModalOptions{}
	}
	return &ModalSandboxFactory{
		options: options,
	}
}

// Type 返回沙箱类型
func (f *ModalSandboxFactory) Type() SandboxType {
	return TypeModal
}

// Create 创建 Modal 沙箱
func (f *ModalSandboxFactory) Create(ctx context.Context, config *SandboxConfig) (Sandbox, error) {
	sandbox := NewModalSandbox(f.options)
	if err := sandbox.Create(ctx, config); err != nil {
		return nil, err
	}
	return sandbox, nil
}

// NewModalSandbox 创建 Modal 沙箱
func NewModalSandbox(options *ModalOptions) *ModalSandbox {
	if options == nil {
		options = &ModalOptions{}
	}
	return &ModalSandbox{
		options: options,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		status: &SandboxStatus{
			State:     StateCreating,
			CreatedAt: time.Now(),
		},
	}
}

// Modal API 相关结构

type modalCreateRequest struct {
	Image   string            `json:"image"`
	AppName string            `json:"app_name,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

type modalCreateResponse struct {
	SandboxID string `json:"sandbox_id"`
	Status    string `json:"status"`
}

type modalExecRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type modalExecResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Duration int    `json:"duration_ms"`
}

// Create 创建沙箱环境
func (s *ModalSandbox) Create(ctx context.Context, config *SandboxConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.options.TokenID == "" || s.options.TokenSecret == "" {
		return fmt.Errorf("Modal credentials not configured: TokenID and TokenSecret required")
	}

	s.config = MergeConfig(nil, config)
	s.status.Image = s.config.Image

	// 调用 Modal API 创建沙箱
	reqBody := modalCreateRequest{
		Image:   s.config.Image,
		AppName: s.options.AppName,
		Env:     s.config.Env,
		Timeout: int(s.config.Timeout.Seconds()),
	}

	resp, err := s.doRequest(ctx, "POST", "/v1/sandboxes", reqBody)
	if err != nil {
		s.status.State = StateError
		s.status.Error = fmt.Sprintf("failed to create sandbox: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		s.status.State = StateError
		s.status.Error = fmt.Sprintf("Modal API error: %s", string(body))
		return fmt.Errorf("Modal API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var createResp modalCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		s.status.State = StateError
		s.status.Error = fmt.Sprintf("failed to decode response: %v", err)
		return err
	}

	s.sandboxID = createResp.SandboxID
	s.status.ID = s.sandboxID
	s.status.State = StateRunning
	s.status.StartedAt = time.Now()

	// 执行初始化脚本
	if s.config.SetupScript != "" {
		result, err := s.executeInternal(ctx, s.config.SetupScript)
		if err != nil || result.ExitCode != 0 {
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
func (s *ModalSandbox) Execute(ctx context.Context, command string) (*ExecuteResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sandboxID == "" {
		return &ExecuteResult{
			ExitCode: -1,
			Error:    "sandbox not created",
		}, fmt.Errorf("sandbox not created")
	}

	return s.executeInternal(ctx, command)
}

// executeInternal 内部执行命令（不加锁）
func (s *ModalSandbox) executeInternal(ctx context.Context, command string) (*ExecuteResult, error) {
	start := time.Now()

	timeout := s.config.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	reqBody := modalExecRequest{
		Command: command,
		Timeout: int(timeout.Seconds()),
	}

	resp, err := s.doRequest(ctx, "POST", fmt.Sprintf("/v1/sandboxes/%s/exec", s.sandboxID), reqBody)
	if err != nil {
		return &ExecuteResult{
			ExitCode: -1,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, err
	}
	defer resp.Body.Close()

	var execResp modalExecResponse
	if err := json.NewDecoder(resp.Body).Decode(&execResp); err != nil {
		return &ExecuteResult{
			ExitCode: -1,
			Error:    fmt.Sprintf("failed to decode response: %v", err),
			Duration: time.Since(start),
		}, err
	}

	return &ExecuteResult{
		ExitCode: execResp.ExitCode,
		Stdout:   execResp.Stdout,
		Stderr:   execResp.Stderr,
		Duration: time.Duration(execResp.Duration) * time.Millisecond,
	}, nil
}

// Upload 上传文件到沙箱
func (s *ModalSandbox) Upload(ctx context.Context, localPath, remotePath string) error {
	// Modal 使用不同的方式处理文件，这里通过 base64 编码
	return fmt.Errorf("Upload not implemented for Modal sandbox - use WriteFile instead")
}

// Download 从沙箱下载文件
func (s *ModalSandbox) Download(ctx context.Context, remotePath, localPath string) error {
	return fmt.Errorf("Download not implemented for Modal sandbox - use ReadFile instead")
}

// WriteFile 直接写入内容到沙箱中的文件
func (s *ModalSandbox) WriteFile(ctx context.Context, remotePath string, content []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sandboxID == "" {
		return fmt.Errorf("sandbox not created")
	}

	// 使用 echo 和 base64 解码写入文件
	// 注意：这种方式有大小限制
	encoded := encodeBase64ForModal(content)
	command := fmt.Sprintf("echo '%s' | base64 -d > %s", encoded, remotePath)

	result, err := s.executeInternal(ctx, command)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("failed to write file: %s", result.Stderr)
	}

	return nil
}

// ReadFile 读取沙箱中的文件内容
func (s *ModalSandbox) ReadFile(ctx context.Context, remotePath string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sandboxID == "" {
		return nil, fmt.Errorf("sandbox not created")
	}

	// 使用 base64 编码读取文件
	command := fmt.Sprintf("base64 %s", remotePath)
	result, err := s.executeInternal(ctx, command)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("failed to read file: %s", result.Stderr)
	}

	return decodeBase64ForModal(result.Stdout)
}

// Status 获取沙箱状态
func (s *ModalSandbox) Status(ctx context.Context) (*SandboxStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 调用 API 获取实际状态
	if s.sandboxID != "" {
		resp, err := s.doRequest(ctx, "GET", fmt.Sprintf("/v1/sandboxes/%s", s.sandboxID), nil)
		if err == nil {
			defer resp.Body.Close()
			var statusResp struct {
				Status string `json:"status"`
			}
			if json.NewDecoder(resp.Body).Decode(&statusResp) == nil {
				switch statusResp.Status {
				case "running":
					s.status.State = StateRunning
				case "stopped", "terminated":
					s.status.State = StateStopped
				case "error":
					s.status.State = StateError
				}
			}
		}
	}

	status := *s.status
	return &status, nil
}

func asciiURL(b ...byte) string {
	return string(b)
}

// Close 关闭并销毁沙箱
func (s *ModalSandbox) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sandboxID == "" {
		return nil
	}

	// 调用 API 终止沙箱
	_, _ = s.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/sandboxes/%s", s.sandboxID), nil)

	s.status.State = StateStopped
	s.sandboxID = ""

	return nil
}

// doRequest 执行 HTTP 请求
func (s *ModalSandbox) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	baseURL := asciiURL(104, 116, 116, 112, 115, 58, 47, 47, 97, 112, 105, 46, 109, 111, 100, 97, 108, 46, 99, 111, 109)

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Token %s:%s", s.options.TokenID, s.options.TokenSecret))

	return s.client.Do(req)
}

// SandboxID 获取沙箱 ID
func (s *ModalSandbox) SandboxID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sandboxID
}

// 辅助函数

func encodeBase64ForModal(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func decodeBase64ForModal(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
