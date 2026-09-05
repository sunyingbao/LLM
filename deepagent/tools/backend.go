package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"eino-cli/deepagent/backend/sandbox"
	"eino-cli/deepagent/backend/sandboxpaths"
	"eino-cli/deepagent/core/backends"
)

// WorkspaceBackend binds core tools to the CLI workspace and session sandbox.
type WorkspaceBackend struct {
	host    *backends.SandboxFilesystemBackend
	manager sandbox.SandboxManager
}

func NewWorkspaceBackend(manager sandbox.SandboxManager) (backend *WorkspaceBackend) {
	return &WorkspaceBackend{
		host:    backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{RootDir: resolveRoot(), VirtualMode: true}),
		manager: manager,
	}
}

func (b *WorkspaceBackend) ID() (id string) { return b.host.ID() }

func (b *WorkspaceBackend) sandboxPath(ctx context.Context, path string, readOnly bool) (box sandbox.Sandbox, virtualPath string, err error) {
	if path == "/" || path == "." {
		path = ""
	}
	if filepath.IsAbs(path) && !strings.Contains(path, "..") {
		path = sandbox.ReverseResolvePath([]sandboxpaths.MountMapping{{HostPath: resolveRoot(), VirtualPath: sandboxpaths.VirtualPathPrefixRepo}}, path)
	}
	virtualPath, err = resolveToolSearchPath(path, readOnly)
	if err != nil {
		return nil, "", err
	}
	box, err = getRequiredSandbox(ctx, b.manager)
	return box, virtualPath, err
}

func (b *WorkspaceBackend) LsInfo(ctx context.Context, path string) (files []backends.FileInfo, err error) {
	if b.manager == nil {
		return b.host.LsInfo(ctx, path)
	}
	box, path, err := b.sandboxPath(ctx, path, true)
	if err != nil {
		return nil, err
	}
	entries, err := box.ListDir(ctx, path, 1)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		files = append(files, backends.FileInfo{Path: strings.TrimSuffix(entry, "/"), IsDir: strings.HasSuffix(entry, "/")})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func (b *WorkspaceBackend) Read(ctx context.Context, path string, offset, limit *int) (content string, err error) {
	if b.manager == nil {
		return b.host.Read(ctx, path, offset, limit)
	}
	box, path, err := b.sandboxPath(ctx, path, true)
	if err != nil {
		return "", err
	}
	content, err = box.ReadFile(ctx, path)
	if err != nil {
		return "", err
	}
	if len(content) > backends.MaxFileSizeMB*1024*1024 {
		return "", fmt.Errorf("文件过大（超过 %dMB 限制）", backends.MaxFileSizeMB)
	}
	return backends.ReadFileLines(content, offset, limit), nil
}

func (b *WorkspaceBackend) Write(ctx context.Context, path, content string) (result *backends.WriteResult, err error) {
	if msg, denied := denyOnPlanMode(ctx); denied {
		return nil, fmt.Errorf("%s", msg)
	}
	if b.manager == nil {
		return b.host.Write(ctx, path, content)
	}
	box, path, err := b.sandboxPath(ctx, path, false)
	if err != nil {
		return nil, err
	}
	if err = box.WriteFile(ctx, path, content, false); err != nil {
		return nil, err
	}
	return &backends.WriteResult{Path: path}, nil
}

func (b *WorkspaceBackend) Edit(ctx context.Context, path, oldString, newString string, replaceAll bool) (result *backends.EditResult, err error) {
	if msg, denied := denyOnPlanMode(ctx); denied {
		return nil, fmt.Errorf("%s", msg)
	}
	if b.manager == nil {
		return b.host.Edit(ctx, path, oldString, newString, replaceAll)
	}
	box, path, err := b.sandboxPath(ctx, path, false)
	if err != nil {
		return nil, err
	}
	content, err := box.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	updated, count, err := backends.ReplaceFileText(content, oldString, newString, replaceAll)
	result = &backends.EditResult{Path: path, Occurrences: count}
	if err != nil {
		return result, err
	}
	if err = box.WriteFile(ctx, path, updated, false); err != nil {
		return result, err
	}
	return result, nil
}

func (b *WorkspaceBackend) GrepRaw(ctx context.Context, pattern, path, glob string) (matches []backends.GrepMatch, err error) {
	if b.manager == nil {
		return b.host.GrepRaw(ctx, pattern, path, glob)
	}
	box, path, err := b.sandboxPath(ctx, path, true)
	if err != nil {
		return nil, err
	}
	hits, _, err := box.Grep(ctx, path, pattern, sandbox.GrepOpts{Glob: glob, CaseSensitive: true, MaxResults: 100})
	if err != nil {
		return nil, err
	}
	for _, hit := range hits {
		matches = append(matches, backends.GrepMatch{Path: hit.Path, Line: hit.LineNumber, Text: hit.Line})
	}
	return matches, nil
}

func (b *WorkspaceBackend) GlobInfo(ctx context.Context, pattern, path string) (files []backends.FileInfo, err error) {
	if b.manager == nil {
		return b.host.GlobInfo(ctx, pattern, path)
	}
	box, path, err := b.sandboxPath(ctx, path, true)
	if err != nil {
		return nil, err
	}
	entries, _, err := box.Glob(ctx, path, pattern, sandbox.GlobOpts{IncludeDirs: true, MaxResults: 1000})
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		files = append(files, backends.FileInfo{Path: strings.TrimSuffix(entry, "/"), IsDir: strings.HasSuffix(entry, "/")})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func (b *WorkspaceBackend) UploadFiles(ctx context.Context, files []struct {
	Path    string
	Content []byte
}) (results []backends.FileUploadResponse, err error) {
	for _, file := range files {
		result, writeErr := b.Write(ctx, file.Path, string(file.Content))
		if writeErr != nil {
			return results, writeErr
		}
		results = append(results, backends.FileUploadResponse{Path: result.Path, Error: result.Error})
	}
	return results, nil
}

func (b *WorkspaceBackend) ChangeDir(ctx context.Context, path string) (err error) {
	return fmt.Errorf("CLI workspace directory is fixed; set the command working directory instead")
}

func (b *WorkspaceBackend) Execute(ctx context.Context, command string) (result *backends.ExecuteResponse, err error) {
	output, err := b.ExecuteCommand(ctx, backends.CommandRequest{Command: command, MaxOutputBytes: backends.ToolResultTokenLimit * 4})
	if err != nil {
		return nil, err
	}
	return &backends.ExecuteResponse{Output: output.Output, ExitCode: output.ExitCode, Truncated: output.Truncated, ShellSessionID: output.ShellSessionID}, nil
}

func (b *WorkspaceBackend) ExecuteCommand(ctx context.Context, req backends.CommandRequest) (result *backends.CommandResult, err error) {
	if msg, denied := denyOnPlanMode(ctx); denied {
		return nil, fmt.Errorf("%s", msg)
	}
	if !allowsIsolatedExec(b.manager) {
		if msg, denied := denyOnRollbackProtected(ctx); denied {
			return nil, fmt.Errorf("%s", msg)
		}
	}
	if b.manager == nil {
		return b.host.ExecuteCommand(ctx, req)
	}
	box, err := getRequiredSandbox(ctx, b.manager)
	if err != nil {
		return nil, err
	}
	command := req.Command
	if req.WorkDir != "" {
		path, pathErr := resolveToolSearchPath(req.WorkDir, true)
		if pathErr != nil {
			return nil, pathErr
		}
		command = "cd " + shellQuote(path) + " && " + command
	}
	if len(req.Env) > 0 {
		return nil, fmt.Errorf("session sandbox command environment overrides are not supported")
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	output, err := box.ExecuteCommand(ctx, command)
	if err != nil {
		return nil, err
	}
	result = &backends.CommandResult{Output: output}
	if _, suffix, found := strings.Cut(output, "\nExit Code: "); found {
		if code, parseErr := strconv.Atoi(strings.TrimSpace(suffix)); parseErr == nil {
			result.ExitCode = code
		}
	}
	if req.MaxOutputBytes > 0 && len(result.Output) > req.MaxOutputBytes {
		result.Output = result.Output[:req.MaxOutputBytes]
		result.Truncated = true
	}
	return result, nil
}

var _ backends.SandboxBackend = (*WorkspaceBackend)(nil)
