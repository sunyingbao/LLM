package filesystem

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	deeptools "eino-cli/deepagent/core/tools"
)

// FilesystemMiddleware 提供文件系统访问能力和本地环境上下文检测
// 自动检测并注入 Git 信息、项目语言、包管理器、测试命令和目录结构
type FilesystemMiddleware struct {
	middleware.BaseMiddleware
	backend               backends.Backend
	readOnly              bool
	disableUploadDownload bool
	disableExecute        bool
	disableApplyPatch     bool
	workDir               string
	maxTreeDepth          int
	maxTreeEntries        int
	commandTimeout        time.Duration
	extraContext          string
	toolMask              deeptools.Mask
	cachedContext         string
	mu                    sync.RWMutex
}

// FilesystemConfig 文件系统中间件配置
type FilesystemConfig struct {
	// Backend 文件系统后端
	// 如果传入的是 SandboxBackend，则自动启用 execute 工具
	Backend backends.Backend

	// ReadOnly 只读模式，只暴露 ls/read_file/glob/grep 工具
	// 用于 Explore/Plan SubAgent 等只需要读取能力的场景
	ReadOnly bool

	// WorkDir 工作目录，默认为当前目录
	WorkDir string

	// MaxTreeDepth 目录树最大深度，默认 3
	MaxTreeDepth int

	// MaxTreeEntries 目录树最大条目数，默认 20
	MaxTreeEntries int

	// CommandTimeout 命令执行超时，默认 5 秒
	CommandTimeout time.Duration

	// ExtraContext 额外的上下文信息
	// 会拼接到 LocalContext 系统提示词末尾
	// 可用于注入用户动作记录（如已上传文件列表等）
	ExtraContext string

	// DisableUploadDownload 禁用 upload_files 和 download_files 工具
	// 这两个工具功能与 write_file/read_file 重复，可关闭以节省 token
	DisableUploadDownload bool

	// DisableExecute 禁用 execute 工具
	// 即使 Backend 实现了 SandboxBackend 接口，也不暴露 execute 工具
	DisableExecute bool

	// DisableApplyPatch 禁用 apply_patch 工具。
	// 当 Backend 支持 apply_patch 时，禁用后仍回退到 edit_file。
	DisableApplyPatch bool

	// ToolMask 控制该中间件自身工具和工具提示词的可见性。
	ToolMask deeptools.Mask
}

// New 创建文件系统中间件
func New(cfg *FilesystemConfig) *FilesystemMiddleware {
	if cfg == nil {
		cfg = &FilesystemConfig{}
	}

	backend := cfg.Backend
	if backend == nil {
		panic("FilesystemMiddleware: Backend is required")
	}

	workDir := cfg.WorkDir
	if workDir == "" {
		workDir = "/tmp/deep_agent_workspace"
	}

	maxTreeDepth := cfg.MaxTreeDepth
	if maxTreeDepth <= 0 {
		maxTreeDepth = 3
	}

	maxTreeEntries := cfg.MaxTreeEntries
	if maxTreeEntries <= 0 {
		maxTreeEntries = 20
	}

	commandTimeout := cfg.CommandTimeout
	if commandTimeout <= 0 {
		commandTimeout = 5 * time.Second
	}

	return &FilesystemMiddleware{
		backend:               backend,
		readOnly:              cfg.ReadOnly,
		disableUploadDownload: cfg.DisableUploadDownload,
		disableExecute:        cfg.DisableExecute,
		disableApplyPatch:     cfg.DisableApplyPatch,
		workDir:               workDir,
		maxTreeDepth:          maxTreeDepth,
		maxTreeEntries:        maxTreeEntries,
		commandTimeout:        commandTimeout,
		extraContext:          cfg.ExtraContext,
		toolMask:              cfg.ToolMask,
	}
}

func (m *FilesystemMiddleware) Name() string {
	return constant.MiddlewareFilesystem
}

func (m *FilesystemMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	// 只读模式：只暴露 ls/read_file/glob/grep
	if m.readOnly {
		tools := []tool.BaseTool{
			m.newLsTool(),
			m.newReadFileTool(),
			m.newGlobTool(),
			m.newGrepTool(),
		}
		return deeptools.FilterToolsByMask(ctx, tools, m.toolMask, "FilesystemMiddleware::Tools"), nil
	}

	tools := []tool.BaseTool{
		m.newLsTool(),
		m.newReadFileTool(),
		m.newWriteFileTool(),
		m.newGlobTool(),
		m.newGrepTool(),
	}

	if !m.disableUploadDownload {
		tools = append(tools, m.newUploadFilesTool(), m.newDownloadFilesTool())
	}

	// 如果 backend 支持命令执行（实现了 SandboxBackend 接口），添加 execute 工具
	if !m.disableExecute {
		if sb, ok := m.backend.(backends.SandboxBackend); ok {
			tools = append(tools, m.newExecuteTool(sb))
		}
	}

	if patchTool := m.newApplyPatchTool(); patchTool != nil {
		tools = append(tools, patchTool)
	} else {
		tools = append(tools, m.newEditFileTool())
	}

	return deeptools.FilterToolsByMask(ctx, tools, m.toolMask, "FilesystemMiddleware::Tools"), nil
}

func (m *FilesystemMiddleware) applyPatchEnabled() bool {
	if m.disableApplyPatch {
		return false
	}
	patchBackend, ok := m.backend.(backends.ApplyPatchBackend)
	return ok && patchBackend.SupportsApplyPatch()
}

// buildPrompt 构建文件系统系统提示词
func (m *FilesystemMiddleware) buildPrompt(ctx context.Context) string {
	if m.toolMask == nil {
		return m.buildDefaultPrompt()
	}

	visibleTools, err := m.Tools(ctx)
	if err != nil {
		return ""
	}
	visible := deeptools.ToolNameSet(ctx, visibleTools, "FilesystemMiddleware::buildPrompt")
	if len(visible) == 0 {
		return ""
	}
	return m.buildMaskedPrompt(visible)
}

func (m *FilesystemMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	var parts []string
	// 1. 文件系统工具提示词
	if prompt := m.buildPrompt(ctx); prompt != "" {
		parts = append(parts, prompt)
	}
	// 2. 本地上下文提示词（Git、语言、包管理器、测试命令、目录树）
	if lc := m.getCachedLocalContext(ctx); lc != "" {
		parts = append(parts, lc)
	}
	if len(parts) > 0 {
		combined := strings.Join(parts, "\n\n")
		return []*schema.Message{schema.SystemMessage(combined)}, nil
	}
	return nil, nil
}

// IsReadOnly 返回是否为只读模式
func (m *FilesystemMiddleware) IsReadOnly() bool {
	return m.readOnly
}

// Backend 获取后端
func (m *FilesystemMiddleware) Backend() backends.Backend {
	return m.backend
}

// SetWorkDir 设置工作目录并刷新上下文
func (m *FilesystemMiddleware) SetWorkDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workDir = dir
	m.cachedContext = ""
}

// RefreshContext 刷新缓存的上下文
// 可用于工作目录变更后重新检测环境
func (m *FilesystemMiddleware) RefreshContext() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cachedContext = ""
}

// WorkDir 获取当前工作目录
func (m *FilesystemMiddleware) WorkDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workDir
}

// CommandTimeout 获取 execute 工具命令超时配置。
func (m *FilesystemMiddleware) CommandTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.commandTimeout
}

// ==================== ls 工具 ====================

type lsInput struct {
	Path string `json:"path" jsonschema:"description=要列出的目录路径"`
}

func (m *FilesystemMiddleware) newLsTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolLs,
		filesystemToolDesc(constant.ToolLs),
		func(ctx context.Context, input *lsInput) (string, error) {
			path := input.Path
			if path == "" {
				path = "." // 使用当前工作目录而非根目录
			}

			files, err := m.backend.LsInfo(ctx, path)
			if err != nil {
				return backends.CommonToolResult{
					Errmsg: err.Error(),
				}.String(), nil
			}

			return backends.CommonToolResult{
				Data: files,
			}.String(), nil
		},
		filesystemToolOptions(constant.ToolLs)...,
	)
	return t
}

// ==================== read_file 工具 ====================

type readFileInput struct {
	Path   string `json:"path" jsonschema:"description=文件路径"`
	Offset *int   `json:"offset,omitempty" jsonschema:"description=起始行号（从0开始），不传则从头开始"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"description=读取的行数，不传则读取全部"`
}

func (m *FilesystemMiddleware) newReadFileTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolReadFile,
		filesystemToolDesc(constant.ToolReadFile),
		func(ctx context.Context, input *readFileInput) (string, error) {
			content, err := m.backend.Read(ctx, input.Path, input.Offset, input.Limit)
			if err != nil {
				return backends.CommonToolResult{
					Errmsg: err.Error(),
				}.String(), nil
			}

			return backends.CommonToolResult{
				Data: content,
			}.String(), nil
		},
		filesystemToolOptions(constant.ToolReadFile)...,
	)
	return t
}

// ==================== write_file 工具 ====================

type writeFileInput struct {
	Path    string `json:"path" jsonschema:"description=文件路径"`
	Content string `json:"content" jsonschema:"description=文件内容"`
}

func (m *FilesystemMiddleware) newWriteFileTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolWriteFile,
		filesystemToolDesc(constant.ToolWriteFile),
		func(ctx context.Context, input *writeFileInput) (string, error) {
			result, err := m.backend.Write(ctx, input.Path, input.Content)
			if err != nil {
				return backends.CommonToolResult{
					Errmsg: err.Error(),
				}.String(), nil
			}

			return backends.CommonToolResult{
				Data: result,
			}.String(), nil
		},
		filesystemToolOptions(constant.ToolWriteFile)...,
	)
	return t
}

// ==================== edit_file 工具 ====================

type editFileInput struct {
	Path       string `json:"path" jsonschema:"description=文件路径"`
	OldString  string `json:"old_string" jsonschema:"description=要替换的原始字符串，必须精确匹配且在文件中唯一"`
	NewString  string `json:"new_string" jsonschema:"description=替换后的新字符串"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"description=是否替换所有匹配，默认只替换第一个"`
}

func (m *FilesystemMiddleware) newEditFileTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolEditFile,
		filesystemToolDesc(constant.ToolEditFile),
		func(ctx context.Context, input *editFileInput) (string, error) {
			result, err := m.backend.Edit(ctx, input.Path, input.OldString, input.NewString, input.ReplaceAll)
			if err != nil {
				return backends.CommonToolResult{
					Errmsg: err.Error(),
				}.String(), nil
			}

			return backends.CommonToolResult{
				Data: result,
			}.String(), nil
		},
		filesystemToolOptions(constant.ToolEditFile)...,
	)
	return t
}

type applyPatchInput struct {
	Patch string `json:"patch" jsonschema:"description=Complete patch content. It must start with *** Begin Patch and end with *** End Patch. Use bare @@ on its own line. The hunk body starts on the next line. Every hunk body line has shape <operation><file line text>. The operation is one extra first character and is never part of the file line text. Choose the operation from edit intent, then append the exact file line unchanged; never let a file line's leading - or + serve as the operation. Changing an existing line requires -old followed by +new, not old as context followed by +new. A copied context line has one more leading space than the file line, even for top-level lines. For file line plain: context [space]plain, delete -plain, add +plain. For file line - item: context [space]- item, delete -- item, add +- item. For file line + value: context [space]+ value, delete -+ value, add ++ value. Do not use unified diff ranges like @@ 78,16 or @@ -1,3 +1,4."`
}

func (m *FilesystemMiddleware) newApplyPatchTool() tool.BaseTool {
	if !m.applyPatchEnabled() {
		return nil
	}
	p, ok := m.backend.(backends.ApplyPatchBackend)
	if !ok {
		return nil
	}

	t, _ := utils.InferTool(
		constant.ToolApplyPatch,
		filesystemToolDesc(constant.ToolApplyPatch),
		func(ctx context.Context, input *applyPatchInput) (string, error) {
			res, err := p.ApplyPatch(ctx, input.Patch)
			if err != nil {
				return backends.CommonToolResult{
					Errmsg: err.Error(),
				}.String(), nil
			} else {
				return backends.CommonToolResult{
					Data: res,
				}.String(), nil
			}
		},
		filesystemToolOptions(constant.ToolApplyPatch)...,
	)
	return t
}

// ==================== glob 工具 ====================

type globInput struct {
	Pattern string `json:"pattern" jsonschema:"description=Glob模式，如 *.go 或 **/*.md"`
	Path    string `json:"path,omitempty" jsonschema:"description=搜索起始路径，默认为根目录"`
}

func (m *FilesystemMiddleware) newGlobTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolGlob,
		filesystemToolDesc(constant.ToolGlob),
		func(ctx context.Context, input *globInput) (string, error) {
			path := input.Path
			if path == "" {
				path = "/"
			}

			files, err := m.backend.GlobInfo(ctx, input.Pattern, path)
			if err != nil {
				return backends.CommonToolResult{
					Errmsg: err.Error(),
				}.String(), nil
			}

			return backends.CommonToolResult{
				Data: files,
			}.String(), nil
		},
		filesystemToolOptions(constant.ToolGlob)...,
	)
	return t
}

// ==================== grep 工具 ====================

type grepInput struct {
	Pattern string `json:"pattern" jsonschema:"description=搜索模式（正则表达式）"`
	Path    string `json:"path,omitempty" jsonschema:"description=搜索路径，可以是文件或目录"`
	Glob    string `json:"glob,omitempty" jsonschema:"description=文件名过滤模式，如 *.go"`
}

func (m *FilesystemMiddleware) newGrepTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolGrep,
		filesystemToolDesc(constant.ToolGrep),
		func(ctx context.Context, input *grepInput) (string, error) {
			path := input.Path
			if path == "" {
				path = "/"
			}

			matches, err := m.backend.GrepRaw(ctx, input.Pattern, path, input.Glob)
			if err != nil {
				return backends.CommonToolResult{
					Errmsg: err.Error(),
				}.String(), nil
			}

			return backends.CommonToolResult{
				Data: matches,
			}.String(), nil
		},
		filesystemToolOptions(constant.ToolGrep)...,
	)
	return t
}

// ==================== execute 工具 ====================

type executeInput struct {
	Command string `json:"command" jsonschema:"description=要执行的shell命令"`
}

func (m *FilesystemMiddleware) newExecuteTool(sb backends.SandboxBackend) tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolExecute,
		filesystemToolDesc(constant.ToolExecute),
		func(ctx context.Context, input *executeInput) (string, error) {
			var (
				result *backends.ExecuteResponse
				err    error
			)
			execCtx := ctx
			cancel := func() {}
			if m.commandTimeout > 0 {
				execCtx, cancel = context.WithTimeout(ctx, m.commandTimeout)
			}
			defer cancel()

			result, err = sb.Execute(execCtx, input.Command)
			if err != nil {
				return backends.CommonToolResult{
					Errmsg: err.Error(),
				}.String(), nil
			}

			return backends.CommonToolResult{
				Data: result,
			}.String(), nil
		},
		filesystemToolOptions(constant.ToolExecute)...,
	)
	return t
}

// ==================== upload_files 工具 ====================

type uploadFileItem struct {
	Path     string `json:"path" jsonschema:"description=目标文件路径"`
	Content  string `json:"content" jsonschema:"description=文件内容（文本或base64编码）"`
	IsBase64 bool   `json:"is_base64,omitempty" jsonschema:"description=内容是否为base64编码，默认false"`
}

type uploadFilesInput struct {
	Files []uploadFileItem `json:"files" jsonschema:"description=要上传的文件列表"`
}

type uploadResult struct {
	Path    string `json:"path"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Size    int    `json:"size,omitempty"`
}

func (m *FilesystemMiddleware) newUploadFilesTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolUploadFiles,
		filesystemToolDesc(constant.ToolUploadFiles),
		func(ctx context.Context, input *uploadFilesInput) (string, error) {
			if len(input.Files) == 0 {
				// 不返回 error，将错误信息作为字符串返回，避免图停止运行
				return fmt.Sprintf("[Error] upload_files failed: %s", constant.ErrMsgEmptyFileList), nil
			}

			results := make([]uploadResult, 0, len(input.Files))

			for _, file := range input.Files {
				result := uploadResult{Path: file.Path}

				content := file.Content
				if file.IsBase64 {
					// 解码 base64
					decoded, err := decodeBase64(content)
					if err != nil {
						result.Success = false
						result.Error = fmt.Sprintf("base64 解码失败: %v", err)
						results = append(results, result)
						continue
					}
					content = string(decoded)
				}

				// 写入文件
				writeResult, err := m.backend.Write(ctx, file.Path, content)
				if err != nil {
					result.Success = false
					result.Error = err.Error()
				} else if writeResult.Error != "" {
					result.Success = false
					result.Error = string(writeResult.Error)
				} else {
					result.Success = true
					result.Size = len(content)
				}

				results = append(results, result)
			}

			// 格式化输出
			output, _ := json.MarshalIndent(map[string]interface{}{
				"total":   len(input.Files),
				"success": countSuccessful(results),
				"results": results,
			}, "", "  ")
			return string(output), nil
		},
		filesystemToolOptions(constant.ToolUploadFiles)...,
	)
	return t
}

// ==================== download_files 工具 ====================

type downloadFilesInput struct {
	Paths    []string `json:"paths" jsonschema:"description=要下载的文件路径列表"`
	AsBase64 bool     `json:"as_base64,omitempty" jsonschema:"description=是否将二进制内容转为base64，默认false"`
}

type downloadResult struct {
	Path     string `json:"path"`
	Success  bool   `json:"success"`
	Content  string `json:"content,omitempty"`
	Size     int    `json:"size,omitempty"`
	Error    string `json:"error,omitempty"`
	IsBase64 bool   `json:"is_base64,omitempty"`
}

func (m *FilesystemMiddleware) newDownloadFilesTool() tool.BaseTool {
	// 使用 InferTool 而非 NewTool，确保生成有效的 ParamsOneOf
	// 这样工具定义中的 parameters 字段不会是 null
	t, _ := utils.InferTool(
		constant.ToolDownloadFiles,
		filesystemToolDesc(constant.ToolDownloadFiles),
		func(ctx context.Context, input *downloadFilesInput) (string, error) {
			if len(input.Paths) == 0 {
				// 不返回 error，将错误信息作为字符串返回，避免图停止运行
				return fmt.Sprintf("[Error] download_files failed: %s", constant.ErrMsgEmptyPathList), nil
			}

			results := make([]downloadResult, 0, len(input.Paths))

			for _, path := range input.Paths {
				result := downloadResult{Path: path}

				// 读取文件
				content, err := m.backend.Read(ctx, path, nil, nil) // nil 表示读取全部
				if err != nil {
					result.Success = false
					result.Error = err.Error()
				} else {
					result.Success = true
					result.Size = len(content)

					if input.AsBase64 {
						result.Content = encodeBase64([]byte(content))
						result.IsBase64 = true
					} else {
						// 检查是否是二进制内容
						if isBinaryContent(content) {
							result.Content = encodeBase64([]byte(content))
							result.IsBase64 = true
						} else {
							result.Content = content
						}
					}
				}

				results = append(results, result)
			}

			// 格式化输出
			output, _ := json.MarshalIndent(map[string]interface{}{
				"total":   len(input.Paths),
				"success": countDownloadSuccessful(results),
				"results": results,
			}, "", "  ")
			return string(output), nil
		},
		filesystemToolOptions(constant.ToolDownloadFiles)...,
	)
	return t
}

// 辅助函数

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func isBinaryContent(content string) bool {
	// 简单检查：如果包含大量非文本字符，则认为是二进制
	nonText := 0
	for _, r := range content {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			nonText++
		}
	}
	return nonText > len(content)/10 // 超过 10% 的非文本字符
}

func countSuccessful(results []uploadResult) int {
	count := 0
	for _, r := range results {
		if r.Success {
			count++
		}
	}
	return count
}

func countDownloadSuccessful(results []downloadResult) int {
	count := 0
	for _, r := range results {
		if r.Success {
			count++
		}
	}
	return count
}
