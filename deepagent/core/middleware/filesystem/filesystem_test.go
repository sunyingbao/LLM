package filesystem

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	jsonschema "github.com/eino-contrib/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
)

// ==================== helpers ====================

func invokeTool(t *testing.T, base tool.BaseTool, payload string) string {
	t.Helper()
	invokable, ok := base.(tool.InvokableTool)
	require.Truef(t, ok, "tool %T is not invokable", base)
	got, err := invokable.InvokableRun(context.Background(), payload)
	require.NoError(t, err)
	return got
}

func findTool(t *testing.T, tools []tool.BaseTool, name string) tool.BaseTool {
	t.Helper()
	for _, tl := range tools {
		info, _ := tl.Info(context.Background())
		if info.Name == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func toolNames(tools []tool.BaseTool) []string {
	names := make([]string, len(tools))
	for i, tl := range tools {
		info, _ := tl.Info(context.Background())
		names[i] = info.Name
	}
	return names
}

func parseResult(t *testing.T, raw string) backends.CommonToolResult {
	t.Helper()
	var result backends.CommonToolResult
	err := json.Unmarshal([]byte(raw), &result)
	require.NoError(t, err, "failed to parse CommonToolResult: %s", raw)
	return result
}

func newFilesystemTestBackend(t *testing.T, files map[string]*backends.FileData) *backends.FilesystemBackend {
	t.Helper()
	backend := backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
	for path, file := range files {
		content := ""
		if file != nil {
			content = strings.Join(file.Content, "\n")
		}
		_, err := backend.Write(context.Background(), path, content)
		require.NoError(t, err)
	}
	return backend
}

func toolInfo(t *testing.T, base tool.BaseTool) *schema.ToolInfo {
	t.Helper()
	info, err := base.Info(context.Background())
	require.NoError(t, err)
	return info
}

func toolJSONSchema(t *testing.T, base tool.BaseTool) *jsonschema.Schema {
	t.Helper()
	info := toolInfo(t, base)
	require.NotNil(t, info.ParamsOneOf)
	js, err := info.ParamsOneOf.ToJSONSchema()
	require.NoError(t, err)
	require.NotNil(t, js)
	return js
}

func schemaProp(t *testing.T, js *jsonschema.Schema, name string) *jsonschema.Schema {
	t.Helper()
	require.NotNil(t, js)
	require.NotNil(t, js.Properties)
	prop, ok := js.Properties.Get(name)
	require.Truef(t, ok, "schema property %q not found", name)
	return prop
}

func hasHan(s string) bool {
	for _, r := range s {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func schemaHasHanDescription(js *jsonschema.Schema) bool {
	if js == nil {
		return false
	}
	if hasHan(js.Description) {
		return true
	}
	if js.Items != nil && schemaHasHanDescription(js.Items) {
		return true
	}
	if js.AdditionalProperties != nil && schemaHasHanDescription(js.AdditionalProperties) {
		return true
	}
	if js.Properties != nil {
		for pair := js.Properties.Oldest(); pair != nil; pair = pair.Next() {
			if schemaHasHanDescription(pair.Value) {
				return true
			}
		}
	}
	for _, child := range js.OneOf {
		if schemaHasHanDescription(child) {
			return true
		}
	}
	for _, child := range js.AnyOf {
		if schemaHasHanDescription(child) {
			return true
		}
	}
	for _, child := range js.AllOf {
		if schemaHasHanDescription(child) {
			return true
		}
	}
	return false
}

func newTestMiddleware(t *testing.T, files map[string]*backends.FileData, readOnly bool) *FilesystemMiddleware {
	t.Helper()
	return New(&FilesystemConfig{
		Backend:  newFilesystemTestBackend(t, files),
		ReadOnly: readOnly,
		WorkDir:  "/workspace",
	})
}

func sampleFiles() map[string]*backends.FileData {
	return map[string]*backends.FileData{
		"/hello.txt": {
			Content: []string{"hello", "world"},
		},
		"/src/main.go": {
			Content: []string{"package main", "", "func main() {", "}"},
		},
		"/src/util.go": {
			Content: []string{"package main", "", "func helper() {}"},
		},
	}
}

// ==================== New ====================

func TestNew_Defaults(t *testing.T) {
	m := New(&FilesystemConfig{
		Backend: newFilesystemTestBackend(t, nil),
	})
	assert.Equal(t, "/tmp/deep_agent_workspace", m.WorkDir())
	assert.False(t, m.IsReadOnly())
	assert.Equal(t, constant.MiddlewareFilesystem, m.Name())
}

func TestNew_PanicWithoutBackend(t *testing.T) {
	assert.Panics(t, func() {
		New(&FilesystemConfig{})
	})
}

// ==================== Tools (readonly vs readwrite) ====================

func TestTools_ReadOnly(t *testing.T) {
	m := newTestMiddleware(t, nil, true)
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)

	names := toolNames(tools)
	assert.Contains(t, names, constant.ToolLs)
	assert.Contains(t, names, constant.ToolReadFile)
	assert.Contains(t, names, constant.ToolGlob)
	assert.Contains(t, names, constant.ToolGrep)
	assert.NotContains(t, names, constant.ToolWriteFile)
	assert.NotContains(t, names, constant.ToolEditFile)
	assert.NotContains(t, names, constant.ToolExecute)
}

func TestTools_ReadWrite(t *testing.T) {
	m := newTestMiddleware(t, nil, false)
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)

	names := toolNames(tools)
	assert.Contains(t, names, constant.ToolLs)
	assert.Contains(t, names, constant.ToolReadFile)
	assert.Contains(t, names, constant.ToolWriteFile)
	assert.Contains(t, names, constant.ToolEditFile)
	assert.Contains(t, names, constant.ToolGlob)
	assert.Contains(t, names, constant.ToolGrep)
	assert.Contains(t, names, constant.ToolUploadFiles)
	assert.Contains(t, names, constant.ToolDownloadFiles)
	// FilesystemBackend 不实现 SandboxBackend，所以没有 execute
	assert.NotContains(t, names, constant.ToolExecute)
}

// ==================== ls tool ====================

func TestLsTool_Success(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	lsTool := findTool(t, tools, constant.ToolLs)

	raw := invokeTool(t, lsTool, `{"path": "/"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

func TestLsTool_EmptyDir(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	lsTool := findTool(t, tools, constant.ToolLs)

	raw := invokeTool(t, lsTool, `{"path": "/nonexistent"}`)
	result := parseResult(t, raw)
	assert.NotEmpty(t, result.Errmsg)
}

func TestLsTool_DefaultPath(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	lsTool := findTool(t, tools, constant.ToolLs)

	raw := invokeTool(t, lsTool, `{"path": ""}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
}

// ==================== read_file tool ====================

func TestReadFileTool_Success(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	readTool := findTool(t, tools, constant.ToolReadFile)

	raw := invokeTool(t, readTool, `{"path": "/hello.txt"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
	assert.Contains(t, result.Data, "hello")
}

func TestReadFileTool_NotFound(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	readTool := findTool(t, tools, constant.ToolReadFile)

	raw := invokeTool(t, readTool, `{"path": "/no_such_file.txt"}`)
	result := parseResult(t, raw)
	assert.NotEmpty(t, result.Errmsg)
	assert.Nil(t, result.Data)
}

func TestReadFileTool_WithOffsetAndLimit(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	readTool := findTool(t, tools, constant.ToolReadFile)

	raw := invokeTool(t, readTool, `{"path": "/src/main.go", "offset": 1, "limit": 2}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

// ==================== write_file tool ====================

func TestWriteFileTool_Success(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	writeTool := findTool(t, tools, constant.ToolWriteFile)

	raw := invokeTool(t, writeTool, `{"path": "/new_file.txt", "content": "test content"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)

	dataJSON, _ := json.Marshal(result.Data)
	assert.Contains(t, string(dataJSON), "/new_file.txt")
}

func TestWriteFileTool_Overwrite(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	writeTool := findTool(t, tools, constant.ToolWriteFile)

	raw := invokeTool(t, writeTool, `{"path": "/hello.txt", "content": "new content"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

// ==================== edit_file tool ====================

func TestEditFileTool_Success(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	editTool := findTool(t, tools, constant.ToolEditFile)

	raw := invokeTool(t, editTool, `{"path": "/hello.txt", "old_string": "hello", "new_string": "hi"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

func TestEditFileTool_StringNotFound(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	editTool := findTool(t, tools, constant.ToolEditFile)

	raw := invokeTool(t, editTool, `{"path": "/hello.txt", "old_string": "notexist", "new_string": "x"}`)
	result := parseResult(t, raw)
	assert.NotEmpty(t, result.Errmsg)
}

func TestEditFileTool_FileNotExist(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	editTool := findTool(t, tools, constant.ToolEditFile)

	raw := invokeTool(t, editTool, `{"path": "/nosuchfile.txt", "old_string": "x", "new_string": "y"}`)
	result := parseResult(t, raw)
	assert.NotNil(t, result.Data)
}

func TestEditFileTool_ReplaceAll(t *testing.T) {
	files := map[string]*backends.FileData{
		"/repeat.txt": {
			Content: []string{"aaa bbb aaa bbb aaa"},
		},
	}
	m := newTestMiddleware(t, files, false)
	tools, _ := m.Tools(context.Background())
	editTool := findTool(t, tools, constant.ToolEditFile)

	raw := invokeTool(t, editTool, `{"path": "/repeat.txt", "old_string": "aaa", "new_string": "ccc", "replace_all": true}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

// ==================== glob tool ====================

func TestGlobTool_Success(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	globTool := findTool(t, tools, constant.ToolGlob)

	raw := invokeTool(t, globTool, `{"pattern": "**/*.go"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

func TestGlobTool_NoMatch(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	globTool := findTool(t, tools, constant.ToolGlob)

	raw := invokeTool(t, globTool, `{"pattern": "*.rs"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
}

func TestGlobTool_WithPath(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	globTool := findTool(t, tools, constant.ToolGlob)

	raw := invokeTool(t, globTool, `{"pattern": "*.go", "path": "/src"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

// ==================== grep tool ====================

func TestGrepTool_Success(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	grepTool := findTool(t, tools, constant.ToolGrep)

	raw := invokeTool(t, grepTool, `{"pattern": "func"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

func TestGrepTool_NoMatch(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	grepTool := findTool(t, tools, constant.ToolGrep)

	raw := invokeTool(t, grepTool, `{"pattern": "zzzzzzz"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
}

func TestGrepTool_InvalidRegex(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	grepTool := findTool(t, tools, constant.ToolGrep)

	raw := invokeTool(t, grepTool, `{"pattern": "[invalid"}`)
	result := parseResult(t, raw)
	assert.NotEmpty(t, result.Errmsg)
}

func TestGrepTool_WithGlobFilter(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	grepTool := findTool(t, tools, constant.ToolGrep)

	raw := invokeTool(t, grepTool, `{"pattern": "package", "glob": "*.go"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

// ==================== upload_files tool ====================

func TestUploadFilesTool_Success(t *testing.T) {
	m := newTestMiddleware(t, nil, false)
	tools, _ := m.Tools(context.Background())
	uploadTool := findTool(t, tools, constant.ToolUploadFiles)

	raw := invokeTool(t, uploadTool, `{"files": [{"path": "/upload1.txt", "content": "file1"}, {"path": "/upload2.txt", "content": "file2"}]}`)
	assert.Contains(t, raw, `"success": 2`)
	assert.Contains(t, raw, `"total": 2`)
}

func TestUploadFilesTool_EmptyList(t *testing.T) {
	m := newTestMiddleware(t, nil, false)
	tools, _ := m.Tools(context.Background())
	uploadTool := findTool(t, tools, constant.ToolUploadFiles)

	raw := invokeTool(t, uploadTool, `{"files": []}`)
	assert.Contains(t, raw, constant.ErrMsgEmptyFileList)
}

func TestUploadFilesTool_Base64Content(t *testing.T) {
	m := newTestMiddleware(t, nil, false)
	tools, _ := m.Tools(context.Background())
	uploadTool := findTool(t, tools, constant.ToolUploadFiles)

	// "aGVsbG8=" is base64 of "hello"
	raw := invokeTool(t, uploadTool, `{"files": [{"path": "/b64.txt", "content": "aGVsbG8=", "is_base64": true}]}`)
	assert.Contains(t, raw, `"success": 1`)
}

func TestUploadFilesTool_InvalidBase64(t *testing.T) {
	m := newTestMiddleware(t, nil, false)
	tools, _ := m.Tools(context.Background())
	uploadTool := findTool(t, tools, constant.ToolUploadFiles)

	raw := invokeTool(t, uploadTool, `{"files": [{"path": "/bad.txt", "content": "not-valid-base64!!!", "is_base64": true}]}`)
	assert.Contains(t, raw, `"success": 0`)
	assert.Contains(t, raw, "base64")
}

// ==================== download_files tool ====================

func TestDownloadFilesTool_Success(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	downloadTool := findTool(t, tools, constant.ToolDownloadFiles)

	raw := invokeTool(t, downloadTool, `{"paths": ["/hello.txt"]}`)
	assert.Contains(t, raw, `"success": 1`)
	assert.Contains(t, raw, "hello")
}

func TestDownloadFilesTool_NotFound(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	downloadTool := findTool(t, tools, constant.ToolDownloadFiles)

	raw := invokeTool(t, downloadTool, `{"paths": ["/no_file.txt"]}`)
	assert.Contains(t, raw, `"success": 0`)
}

func TestDownloadFilesTool_EmptyList(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	downloadTool := findTool(t, tools, constant.ToolDownloadFiles)

	raw := invokeTool(t, downloadTool, `{"paths": []}`)
	assert.Contains(t, raw, constant.ErrMsgEmptyPathList)
}

func TestDownloadFilesTool_AsBase64(t *testing.T) {
	m := newTestMiddleware(t, sampleFiles(), false)
	tools, _ := m.Tools(context.Background())
	downloadTool := findTool(t, tools, constant.ToolDownloadFiles)

	raw := invokeTool(t, downloadTool, `{"paths": ["/hello.txt"], "as_base64": true}`)
	assert.Contains(t, raw, `"is_base64": true`)
	assert.Contains(t, raw, `"success": 1`)
}

// ==================== execute tool (with SandboxBackend) ====================

type mockSandboxBackend struct {
	*backends.FilesystemBackend
	lastCtx   context.Context
	executeFn func(ctx context.Context, command string) (*backends.ExecuteResponse, error)
}

func (m *mockSandboxBackend) Execute(ctx context.Context, command string) (*backends.ExecuteResponse, error) {
	m.lastCtx = ctx
	if m.executeFn != nil {
		return m.executeFn(ctx, command)
	}
	return &backends.ExecuteResponse{
		Output:   "mock output: " + command,
		ExitCode: 0,
	}, nil
}

func (m *mockSandboxBackend) ExecuteCommand(ctx context.Context, req backends.CommandRequest) (*backends.CommandResult, error) {
	res, err := m.Execute(ctx, req.Command)
	if err != nil {
		return nil, err
	}
	return &backends.CommandResult{
		Output:         res.Output,
		ExitCode:       res.ExitCode,
		Truncated:      res.Truncated,
		ShellSessionID: res.ShellSessionID,
	}, nil
}

func (m *mockSandboxBackend) ID() string {
	return "mock-sandbox"
}

type legacySandboxBackend struct {
	*backends.FilesystemBackend
	lastCtx context.Context
}

func (l *legacySandboxBackend) Execute(ctx context.Context, command string) (*backends.ExecuteResponse, error) {
	l.lastCtx = ctx
	return &backends.ExecuteResponse{Output: command, ExitCode: 0}, nil
}

func (l *legacySandboxBackend) ExecuteCommand(ctx context.Context, req backends.CommandRequest) (*backends.CommandResult, error) {
	return &backends.CommandResult{Output: "unexpected", ExitCode: 0}, nil
}

func (l *legacySandboxBackend) ID() string {
	return "legacy-sandbox"
}

type mockApplyPatchBackend struct {
	*backends.FilesystemBackend
	supports bool
}

func (m *mockApplyPatchBackend) SupportsApplyPatch() bool {
	return m.supports
}

func (m *mockApplyPatchBackend) ApplyPatch(ctx context.Context, patch string) (string, error) {
	return "patched: " + patch, nil
}

func TestExecuteTool_WithSandboxBackend(t *testing.T) {
	sb := &mockSandboxBackend{FilesystemBackend: newFilesystemTestBackend(t, nil)}
	mw := New(&FilesystemConfig{
		Backend: sb,
		WorkDir: "/workspace",
	})

	tools, err := mw.Tools(context.Background())
	require.NoError(t, err)

	names := toolNames(tools)
	assert.Contains(t, names, constant.ToolExecute)

	executeTool := findTool(t, tools, constant.ToolExecute)
	raw := invokeTool(t, executeTool, `{"command": "echo hello"}`)
	result := parseResult(t, raw)
	assert.Empty(t, result.Errmsg)
	assert.NotNil(t, result.Data)
}

func TestExecuteTool_UsesContextTimeout(t *testing.T) {
	sb := &mockSandboxBackend{FilesystemBackend: newFilesystemTestBackend(t, nil)}
	mw := New(&FilesystemConfig{
		Backend:        sb,
		WorkDir:        "/workspace",
		CommandTimeout: 3 * time.Second,
	})

	tools, err := mw.Tools(context.Background())
	require.NoError(t, err)

	executeTool := findTool(t, tools, constant.ToolExecute)
	raw := invokeTool(t, executeTool, `{"command": "echo hello"}`)
	result := parseResult(t, raw)

	assert.Empty(t, result.Errmsg)
	require.NotNil(t, sb.lastCtx)
	deadline, ok := sb.lastCtx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(3*time.Second), deadline, time.Second)
}

func TestExecuteTool_DefaultTimeoutFallback(t *testing.T) {
	sb := &mockSandboxBackend{FilesystemBackend: newFilesystemTestBackend(t, nil)}
	mw := New(&FilesystemConfig{
		Backend: sb,
		WorkDir: "/workspace",
	})

	tools, err := mw.Tools(context.Background())
	require.NoError(t, err)

	executeTool := findTool(t, tools, constant.ToolExecute)
	raw := invokeTool(t, executeTool, `{"command": "echo hello"}`)
	result := parseResult(t, raw)

	assert.Empty(t, result.Errmsg)
	require.NotNil(t, sb.lastCtx)
	_, ok := sb.lastCtx.Deadline()
	require.True(t, ok)
}

func TestExecuteTool_UsesLegacyExecuteEntry(t *testing.T) {
	legacy := &legacySandboxBackend{FilesystemBackend: newFilesystemTestBackend(t, nil)}
	mw := New(&FilesystemConfig{
		Backend:        legacy,
		WorkDir:        "/workspace",
		CommandTimeout: 2 * time.Second,
	})

	tools, err := mw.Tools(context.Background())
	require.NoError(t, err)

	executeTool := findTool(t, tools, constant.ToolExecute)
	raw := invokeTool(t, executeTool, `{"command": "echo hello"}`)
	result := parseResult(t, raw)

	assert.Empty(t, result.Errmsg)
	require.NotNil(t, legacy.lastCtx)
}

// ==================== BuildInitialContext ====================

func TestBuildInitialContext(t *testing.T) {
	m := newTestMiddleware(t, nil, false)
	msgs, err := m.BuildInitialContext(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.Contains(t, msgs[0].Content, "## 文件系统访问")
	assert.Contains(t, msgs[0].Content, "edit_file")
}

func TestBuildInitialContext_EnglishPromptEnv(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	m := newTestMiddleware(t, nil, false)
	msgs, err := m.BuildInitialContext(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.Contains(t, msgs[0].Content, "## Filesystem Access")
	assert.Contains(t, msgs[0].Content, "edit_file")
	assert.False(t, hasHan(msgs[0].Content), msgs[0].Content)
}

func TestFilesystemMiddleware_EnglishPromptWithToolMask(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	ctx := context.Background()
	m := New(&FilesystemConfig{
		Backend: newFilesystemTestBackend(t, nil),
		WorkDir: "/workspace",
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolReadFile
		},
	})

	msgs, err := m.BuildInitialContext(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.Contains(t, msgs[0].Content, "## Filesystem Access")
	assert.NotContains(t, msgs[0].Content, "read_file")
	assert.Contains(t, msgs[0].Content, "ls")
	assert.False(t, hasHan(msgs[0].Content), msgs[0].Content)
}

func TestFilesystemTools_DefaultPromptLanguageCompatibility(t *testing.T) {
	m := newTestMiddleware(t, nil, false)
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)

	readFileTool := findTool(t, tools, constant.ToolReadFile)
	readFileInfo := toolInfo(t, readFileTool)
	assert.Contains(t, readFileInfo.Desc, "读取")
	readFileSchema := toolJSONSchema(t, readFileTool)
	assert.Contains(t, schemaProp(t, readFileSchema, "path").Description, "文件路径")
}

func TestFilesystemTools_EnglishDescriptionsAndSchema(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	m := New(&FilesystemConfig{
		Backend: &mockSandboxBackend{FilesystemBackend: newFilesystemTestBackend(t, nil)},
		WorkDir: "/workspace",
	})
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)

	for _, baseTool := range tools {
		info := toolInfo(t, baseTool)
		assert.False(t, hasHan(info.Desc), "tool %s desc = %s", info.Name, info.Desc)
		assert.False(t, schemaHasHanDescription(toolJSONSchema(t, baseTool)), "tool %s schema has Chinese descriptions", info.Name)
	}

	readFileTool := findTool(t, tools, constant.ToolReadFile)
	readFileInfo := toolInfo(t, readFileTool)
	assert.Equal(t, "Read the contents of a file, optionally with a line range.", readFileInfo.Desc)
	readFileSchema := toolJSONSchema(t, readFileTool)
	assert.Equal(t, "File path.", schemaProp(t, readFileSchema, "path").Description)
	assert.Equal(t, "Starting line number, zero-based. If omitted, reading starts from the beginning.", schemaProp(t, readFileSchema, "offset").Description)
	assert.Equal(t, "Number of lines to read. If omitted, read the full file.", schemaProp(t, readFileSchema, "limit").Description)

	editSchema := toolJSONSchema(t, findTool(t, tools, constant.ToolEditFile))
	assert.Equal(t, "Original string to replace. It must match exactly and be unique in the file.", schemaProp(t, editSchema, "old_string").Description)

	uploadSchema := toolJSONSchema(t, findTool(t, tools, constant.ToolUploadFiles))
	filesSchema := schemaProp(t, uploadSchema, "files")
	require.NotNil(t, filesSchema.Items)
	assert.Equal(t, "Files to upload.", filesSchema.Description)
	assert.Equal(t, "Target file path.", schemaProp(t, filesSchema.Items, "path").Description)
	assert.Equal(t, "File content as text or base64.", schemaProp(t, filesSchema.Items, "content").Description)
	assert.Equal(t, "Whether content is base64 encoded. Defaults to false.", schemaProp(t, filesSchema.Items, "is_base64").Description)

	executeInfo := toolInfo(t, findTool(t, tools, constant.ToolExecute))
	assert.Equal(t, "Run a shell command and return its output.", executeInfo.Desc)
	executeSchema := toolJSONSchema(t, findTool(t, tools, constant.ToolExecute))
	assert.Equal(t, "Shell command to run.", schemaProp(t, executeSchema, "command").Description)

	assert.False(t, hasHan(readFileInfo.Desc))
}

func TestFilesystemTools_EnglishApplyPatchSchema(t *testing.T) {
	t.Setenv(constant.PromptLangEnv, "en")

	m := New(&FilesystemConfig{
		Backend:  &mockApplyPatchBackend{FilesystemBackend: newFilesystemTestBackend(t, nil), supports: true},
		ReadOnly: false,
		WorkDir:  "/workspace",
	})
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)

	applyPatchTool := findTool(t, tools, constant.ToolApplyPatch)
	info := toolInfo(t, applyPatchTool)
	assert.Contains(t, info.Desc, "Use apply_patch to edit files.")
	assert.Contains(t, info.Desc, "Use bare @@ on its own line")
	assert.Contains(t, info.Desc, "Every hunk body line has this shape")
	assert.Contains(t, info.Desc, "The operation is exactly one extra first character")
	assert.Contains(t, info.Desc, "The old line must be a - line")
	assert.Contains(t, info.Desc, "one more leading space than the file line")
	assert.Contains(t, info.Desc, `not "topLevel:"`)
	assert.Contains(t, info.Desc, `"-plain"`)
	assert.Contains(t, info.Desc, `"+- item"`)
	assert.Contains(t, info.Desc, `"++ value"`)
	assert.False(t, hasHan(info.Desc), info.Desc)

	applyPatchSchema := toolJSONSchema(t, applyPatchTool)
	patchDesc := schemaProp(t, applyPatchSchema, "patch").Description
	assert.Contains(t, patchDesc, "Complete patch content.")
	assert.Contains(t, patchDesc, "The hunk body starts on the next line")
	assert.Contains(t, patchDesc, "Every hunk body line has shape")
	assert.Contains(t, patchDesc, "one extra first character")
	assert.Contains(t, patchDesc, "never let a file line's leading - or + serve as the operation")
	assert.Contains(t, patchDesc, "requires -old followed by +new")
	assert.Contains(t, patchDesc, "one more leading space than the file line")
	assert.Contains(t, patchDesc, "even for top-level lines")
	assert.Contains(t, patchDesc, "delete -plain")
	assert.Contains(t, patchDesc, "delete -- item")
	assert.Contains(t, patchDesc, "delete -+ value")
	assert.False(t, hasHan(patchDesc), patchDesc)
}

func TestTools_ReadWrite_WithApplyPatchBackend(t *testing.T) {
	m := New(&FilesystemConfig{
		Backend:  &mockApplyPatchBackend{FilesystemBackend: newFilesystemTestBackend(t, nil), supports: true},
		ReadOnly: false,
		WorkDir:  "/workspace",
	})
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)

	names := toolNames(tools)
	assert.Contains(t, names, constant.ToolApplyPatch)
	assert.NotContains(t, names, constant.ToolEditFile)
}

func TestTools_ReadWrite_FallsBackToEditFileWhenApplyPatchUnsupported(t *testing.T) {
	m := New(&FilesystemConfig{
		Backend:  &mockApplyPatchBackend{FilesystemBackend: newFilesystemTestBackend(t, nil), supports: false},
		ReadOnly: false,
		WorkDir:  "/workspace",
	})
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)

	names := toolNames(tools)
	assert.NotContains(t, names, constant.ToolApplyPatch)
	assert.Contains(t, names, constant.ToolEditFile)
}

func TestTools_ReadWrite_FallsBackToEditFileWhenApplyPatchDisabled(t *testing.T) {
	m := New(&FilesystemConfig{
		Backend:           &mockApplyPatchBackend{FilesystemBackend: newFilesystemTestBackend(t, nil), supports: true},
		ReadOnly:          false,
		WorkDir:           "/workspace",
		DisableApplyPatch: true,
	})
	tools, err := m.Tools(context.Background())
	require.NoError(t, err)

	names := toolNames(tools)
	assert.NotContains(t, names, constant.ToolApplyPatch)
	assert.Contains(t, names, constant.ToolEditFile)
}

func TestBuildInitialContext_WithApplyPatchBackend(t *testing.T) {
	m := New(&FilesystemConfig{
		Backend:  &mockApplyPatchBackend{FilesystemBackend: newFilesystemTestBackend(t, nil), supports: true},
		ReadOnly: false,
		WorkDir:  "/workspace",
	})
	msgs, err := m.BuildInitialContext(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.Contains(t, msgs[0].Content, "apply_patch")
	assert.NotContains(t, msgs[0].Content, "apply_path")
}

func TestBuildInitialContext_DisableApplyPatchUsesEditFilePrompt(t *testing.T) {
	m := New(&FilesystemConfig{
		Backend:           &mockApplyPatchBackend{FilesystemBackend: newFilesystemTestBackend(t, nil), supports: true},
		ReadOnly:          false,
		WorkDir:           "/workspace",
		DisableApplyPatch: true,
	})
	msgs, err := m.BuildInitialContext(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.NotContains(t, msgs[0].Content, "apply_patch")
	assert.Contains(t, msgs[0].Content, "edit_file")
}

func TestBuildInitialContext_ReadOnly(t *testing.T) {
	m := newTestMiddleware(t, nil, true)
	msgs, err := m.BuildInitialContext(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	for _, msg := range msgs {
		assert.NotContains(t, msg.Content, "execute")
	}
}

func TestBuildInitialContext_WithSandbox(t *testing.T) {
	sb := &mockSandboxBackend{FilesystemBackend: newFilesystemTestBackend(t, nil)}
	mw := New(&FilesystemConfig{
		Backend: sb,
		WorkDir: "/workspace",
	})
	msgs, err := mw.BuildInitialContext(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.Contains(t, msgs[0].Content, "execute")
}

// ==================== SetWorkDir / RefreshContext ====================

func TestSetWorkDir(t *testing.T) {
	m := newTestMiddleware(t, nil, false)
	assert.Equal(t, "/workspace", m.WorkDir())

	m.SetWorkDir("/new_dir")
	assert.Equal(t, "/new_dir", m.WorkDir())
}

// ==================== CommonToolResult 格式一致性 ====================

func TestCommonToolResult_Format(t *testing.T) {
	successResult := backends.CommonToolResult{
		Data: "test data",
	}
	s := successResult.String()
	var parsed map[string]interface{}
	err := json.Unmarshal([]byte(s), &parsed)
	require.NoError(t, err)
	assert.Equal(t, "test data", parsed["data"])
	assert.Empty(t, parsed["errmsg"])

	errResult := backends.CommonToolResult{
		Errmsg: "something failed",
	}
	s = errResult.String()
	err = json.Unmarshal([]byte(s), &parsed)
	require.NoError(t, err)
	assert.Equal(t, "something failed", parsed["errmsg"])
	assert.Nil(t, parsed["data"])
}
