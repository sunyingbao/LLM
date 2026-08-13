//go:build !windows

package byted_sandbox

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/base"

	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/gopkg/thrift"
	"code.byted.org/kite/kitex/client/callopt"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/capcut/business/common/sandbox_model"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/pippit/sandbox/gateway"
	"code.byted.org/overpass/pippit_sandbox_gateway/rpc/pippit_sandbox_gateway"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/utils"
)

var fileNotFoundMsgs = []string{
	"File does not exist",
	"Directory does not exist",
}

const defaultSandboxHomeDir = "/root"

func isFileNotFoundBaseResp(resp *base.BaseResp) bool {
	if resp == nil {
		return false
	}

	for _, msg := range fileNotFoundMsgs {
		if strings.Contains(resp.GetStatusMessage(), msg) {
			return true
		}
	}
	return false
}

type AIInfraSandbox struct {
	meta    *sandbox_model.BizMeta
	workDir string
	shellID string
}

func NewAIInfraSandbox(meta *sandbox_model.BizMeta, workDir string) *AIInfraSandbox {
	return &AIInfraSandbox{meta: meta, workDir: workDir}
}

func (A *AIInfraSandbox) ID() string {
	return fmt.Sprintf("%s_%s", A.meta.GetBizType(), A.meta.GetBizID())
}

func (A *AIInfraSandbox) SetShellID(id string) {
	A.shellID = id
}

func (A *AIInfraSandbox) EnsureWorkDir(ctx context.Context) error {
	workDir := strings.TrimSpace(A.workDir)
	if workDir == "" {
		return nil
	}
	req := &gateway.FileMkdirRequest{
		BizMeta: A.meta,
		Dir:     workDir,
		OptionP: thrift.BoolPtr(true),
	}
	resp, err := pippit_sandbox_gateway.RawCall.FileMkdir(ctx, req)
	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::EnsureWorkDir] failed, err: %v, req:%s resp:%s", err, utils.ToString(req), utils.ToString(resp))
		return err
	}
	if resp.GetBaseResp().GetStatusCode() != 0 {
		err := fmt.Errorf("mkdir workdir status_code=%d status_message=%s", resp.GetBaseResp().GetStatusCode(), resp.GetBaseResp().GetStatusMessage())
		logs.CtxError(ctx, "[AIInfraSandbox::EnsureWorkDir] failed, err: %v, req:%s resp:%s", err, utils.ToString(req), utils.ToString(resp))
		return err
	}
	return nil
}

func (A *AIInfraSandbox) LsInfo(ctx context.Context, path string) ([]backends.FileInfo, error) {
	path, err := A.resolvePath(path)
	if err != nil {
		return nil, err
	}
	resp, err := pippit_sandbox_gateway.RawCall.FileList(ctx, &gateway.FileListRequest{
		BizMeta:            A.meta,
		Path:               path,
		FileTypes:          nil,
		IncludePermissions: nil,
		IncludeSize:        nil,
		Recursive:          nil,
		MaxDepth:           nil,
		ShowHidden:         nil,
		SortBy:             nil,
		SortDesc:           nil,
		ActionPreCheck:     nil,
		Base:               nil,
	})

	if resp != nil && isFileNotFoundBaseResp(resp.GetBaseResp()) {
		return nil, backends.ErrFileNotFound
	}

	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::LsInfo] failed, err: %v, path: %s meta:%sv", err, path, utils.ToString(A.meta))
		return nil, err
	}
	var fileInfos []backends.FileInfo
	for _, file := range resp.Files {

		fileInfos = append(fileInfos, backends.FileInfo{
			Path:       file.GetPath(),
			IsDir:      file.GetIsDirectory(),
			Size:       file.GetSizeInBytes(),
			ModifiedAt: time.Unix(file.GetModifiedTimeInSeconds(), 0),
		})
	}
	return fileInfos, nil
}

func (A *AIInfraSandbox) Read(ctx context.Context, path string, offset, limit *int) (string, error) {
	path, err := A.resolvePath(path)
	if err != nil {
		return "", err
	}
	req := &gateway.FileReadRequest{
		BizMeta: A.meta,
		File:    path,
	}
	// 仅在指定 offset/limit 时才设置 StartLine/EndLine
	// nil 表示读取全部内容，与 pippit_sandbox_gateway.RawCall.FileRead 对齐
	if offset != nil && limit != nil {
		req.StartLine = thrift.Int32Ptr(int32(*offset))
		req.EndLine = thrift.Int32Ptr(int32(*offset + *limit))
	}
	resp, err := pippit_sandbox_gateway.RawCall.FileRead(ctx, req)

	if resp != nil && isFileNotFoundBaseResp(resp.GetBaseResp()) {
		return "", backends.ErrFileNotFound
	}

	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::Read] failed, err: %v, path: %s meta:%sv", err, path, utils.ToString(A.meta))
		if resp != nil && strings.Contains(resp.GetBaseResp().GetStatusMessage(), "File does not exist") {
			return "", backends.ErrFileNotFound
		}
		return "", err
	}

	return resp.GetContent(), nil
}

func (A *AIInfraSandbox) Write(ctx context.Context, path string, content string) (*backends.WriteResult, error) {
	path, err := A.resolvePath(path)
	if err != nil {
		return &backends.WriteResult{Path: path, Error: backends.ErrInvalidPath}, nil
	}
	resp, err := pippit_sandbox_gateway.RawCall.FileWrite(ctx, &gateway.FileWriteRequest{
		BizMeta: A.meta,
		File:    path,
		Content: []byte(content),
	})

	if resp != nil && isFileNotFoundBaseResp(resp.GetBaseResp()) {
		return nil, backends.ErrFileNotFound
	}

	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::Write] failed, err: %v, path: %s meta:%sv", err, path, utils.ToString(A.meta))
		if resp != nil && strings.Contains(resp.GetBaseResp().GetStatusMessage(), "File does not exist") {
			return nil, backends.ErrFileNotFound
		}
		return nil, err
	}
	return &backends.WriteResult{
		Path: path,
	}, nil
}

func (A *AIInfraSandbox) Edit(ctx context.Context, path string, oldString, newString string, replaceAll bool) (*backends.EditResult, error) {
	path, err := A.resolvePath(path)
	if err != nil {
		return &backends.EditResult{Path: path, Error: backends.ErrInvalidPath}, nil
	}
	resp, err := pippit_sandbox_gateway.RawCall.FileReplace(ctx, &gateway.FileReplaceRequest{
		BizMeta: A.meta,
		File:    path,
		OldStr:  oldString,
		NewStr_: newString,
	})

	if resp != nil && isFileNotFoundBaseResp(resp.GetBaseResp()) {
		return nil, backends.ErrFileNotFound
	}

	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::Edit] failed, err: %v, path: %s meta:%sv", err, path, utils.ToString(A.meta))
		if resp != nil && strings.Contains(resp.GetBaseResp().GetStatusMessage(), "File does not exist") {
			return nil, backends.ErrFileNotFound
		}

		return nil, err
	}
	return &backends.EditResult{
		Path:        path,
		Occurrences: int(resp.GetReplacedCount()),
	}, nil
}

func (A *AIInfraSandbox) GrepRaw(ctx context.Context, pattern string, path string, glob string) ([]backends.GrepMatch, error) {
	path, err := A.resolvePath(path)
	if err != nil {
		return nil, err
	}

	req := &gateway.FilesGrepRequest{
		BizMeta:   A.meta,
		Path:      path,
		Regex:     pattern,
		Recursive: thrift.BoolPtr(true),
	}

	if glob != "" {
		req.Include = append(req.Include, glob)
	}

	resp, err := pippit_sandbox_gateway.RawCall.FilesGrep(ctx, req)

	if resp != nil && isFileNotFoundBaseResp(resp.GetBaseResp()) {
		return nil, backends.ErrFileNotFound
	}

	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::GrepRaw] failed, err: %v, path: %s meta:%sv", err, path, utils.ToString(A.meta))
		return nil, err
	}

	var grepMatches []backends.GrepMatch
	for _, match := range resp.Data {
		grepMatches = append(grepMatches, backends.GrepMatch{
			Path: match.GetPath(),
			Line: int(match.GetLineNumber()),
			Text: match.GetMatch(),
		})
	}

	return grepMatches, nil
}

func (A *AIInfraSandbox) GlobInfo(ctx context.Context, pattern string, path string) ([]backends.FileInfo, error) {
	path, err := A.resolvePath(path)
	if err != nil {
		return nil, err
	}

	resp, err := pippit_sandbox_gateway.RawCall.FilesGlob(ctx, &gateway.FilesGlobRequest{
		BizMeta: A.meta,
		Path:    path,
		Glob:    pattern,
	})
	if resp != nil && isFileNotFoundBaseResp(resp.GetBaseResp()) {
		return nil, backends.ErrFileNotFound
	}
	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::GlobInfo] failed, err: %v, path: %s meta:%sv", err, path, utils.ToString(A.meta))
		return nil, err
	}
	var results []backends.FileInfo
	for _, f := range resp.Files {
		results = append(results, backends.FileInfo{
			Path:  f.GetPath(),
			IsDir: f.GetIsDirectory(),
			Size:  f.GetSizeInBytes(),
		})
	}

	return results, nil
}

func (A *AIInfraSandbox) UploadFiles(ctx context.Context, files []struct {
	Path    string
	Content []byte
}) ([]backends.FileUploadResponse, error) {

	res := make([]backends.FileUploadResponse, len(files))

	var wg sync.WaitGroup
	for i, f := range files {
		idx := i
		content := f.Content
		p, err := A.resolvePath(f.Path)
		if err != nil {
			res[idx] = backends.FileUploadResponse{Path: f.Path, Error: backends.ErrInvalidPath}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := pippit_sandbox_gateway.RawCall.FileUpload(ctx, &gateway.FileUploadRequest{
				BizMeta: A.meta,
				Path:    p,
				Text:    thrift.StringPtr(string(content)),
			})
			if err != nil {
				logs.CtxError(ctx, "[AIInfraSandbox::UploadFiles] failed, err: %v, path: %s meta:%sv", err, f.Path, utils.ToString(A.meta))
				res[idx].Error = backends.ErrSandboxFsFailed
				return
			}
		}()
	}

	wg.Wait()
	return res, nil
}

func (A *AIInfraSandbox) DownloadFiles(ctx context.Context, paths []string) ([]backends.FileDownloadResponse, error) {
	res := make([]backends.FileDownloadResponse, len(paths))
	for i, path := range paths {
		downloadPath, err := A.resolvePath(path)
		if err != nil {
			res[i] = backends.FileDownloadResponse{Path: path, Error: backends.ErrInvalidPath}
			continue
		}
		resp, err := pippit_sandbox_gateway.RawCall.FileDownload(ctx, &gateway.FileDownloadRequest{
			BizMeta: A.meta,
			Path:    downloadPath,
		})
		res[i].Path = path
		if resp != nil && isFileNotFoundBaseResp(resp.GetBaseResp()) {
			res[i].Error = backends.ErrFileNotFound
			continue
		}
		if err != nil {
			logs.CtxError(ctx, "[AIInfraSandbox::DownloadFiles] failed, err: %v, path: %s meta:%sv", err, path, utils.ToString(A.meta))
			return nil, err
		}
		if resp == nil {
			res[i].Error = backends.ErrSandboxFsFailed
			continue
		}
		res[i].Content = resp.GetContent()
	}
	return res, nil
}

// Execute 实现 SandboxBackend 接口，不注入额外的 RPC 超时配置。
func (A *AIInfraSandbox) Execute(ctx context.Context, command string) (*backends.ExecuteResponse, error) {
	return A.ExecuteWithOpts(ctx, command)
}

// ExecuteWithOpts 执行命令，支持传入 Kitex callopt（如 callopt.WithRPCTimeout）
func (A *AIInfraSandbox) ExecuteWithOpts(ctx context.Context, command string, opts ...callopt.Option) (*backends.ExecuteResponse, error) {
	result, err := A.executeCommandWithOpts(ctx, backends.CommandRequest{Command: command}, opts...)
	if err != nil {
		return nil, err
	}
	return &backends.ExecuteResponse{
		Output:         result.Output,
		ExitCode:       result.ExitCode,
		Truncated:      result.Truncated,
		ShellSessionID: result.ShellSessionID,
	}, nil
}

func (A *AIInfraSandbox) ExecuteCommand(ctx context.Context, req backends.CommandRequest) (*backends.CommandResult, error) {
	return A.executeCommandWithOpts(ctx, req)
}

func (A *AIInfraSandbox) executeCommandWithOpts(ctx context.Context, req backends.CommandRequest, opts ...callopt.Option) (*backends.CommandResult, error) {
	execReq := &gateway.BashExecRequest{
		BizMeta: A.meta,
		Command: req.Command,
	}
	if A.shellID != "" {
		execReq.ShellSessionID = thrift.StringPtr(A.shellID)
	}
	execDir := A.workDir
	if req.WorkDir != "" {
		var err error
		execDir, err = A.resolvePath(req.WorkDir)
		if err != nil {
			return nil, err
		}
	}
	if execDir != "" {
		execReq.ExecDir = thrift.StringPtr(execDir)
	}
	if req.Timeout > 0 {
		execReq.HardTimeoutSec = thrift.Int32Ptr(durationSecondsCeil(req.Timeout))
	}
	var callOpts []interface{}
	for _, opt := range opts {
		callOpts = append(callOpts, opt)
	}
	resp, err := pippit_sandbox_gateway.RawCall.BashExec(ctx, execReq, callOpts...)
	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::Execute] failed, err: %v, req:%s", err, utils.ToString(execReq))
		return nil, err
	}

	res := &backends.CommandResult{
		Output:         combineBashOutput(resp),
		ExitCode:       int(resp.GetExitCode()),
		ShellSessionID: resp.GetShellSessionID(),
		TimedOut:       isBashExecTimeout(resp.GetOperationStatus()),
	}
	if req.MaxOutputBytes > 0 && len(res.Output) > req.MaxOutputBytes {
		res.Output = res.Output[:req.MaxOutputBytes]
		res.Truncated = true
	}

	return res, nil
}

func (A *AIInfraSandbox) ApplyPatch(ctx context.Context, patch string) (string, error) {
	command := buildApplyPatchCommand(A.workDir, patch)
	rsp, err := A.ExecuteCommand(ctx, backends.CommandRequest{Command: command})
	if err != nil {
		logs.CtxError(ctx, "[AIInfraSandbox::ApplyPatch] failed, err: %v, patch: %s meta:%sv", err, patch, utils.ToString(A.meta))
		return "", err
	}

	if rsp.ExitCode != 0 {
		logs.CtxError(ctx, "[AIInfraSandbox::ApplyPatch] failed, exit code: %d,output:%s patch: %s meta:%sv", rsp.ExitCode, rsp.Output, patch, utils.ToString(A.meta))
		return "", fmt.Errorf("exit code: %d ,output:%s", rsp.ExitCode, rsp.Output)
	}

	return rsp.Output, nil
}

func (A *AIInfraSandbox) SupportsApplyPatch() bool {
	return true
}

func buildApplyPatchCommand(workDir string, patch string) string {
	delim := chooseApplyPatchDelimiter(patch)

	var sb strings.Builder
	if workDir != "" {
		sb.WriteString("cd ")
		sb.WriteString(shellSingleQuote(workDir))
		sb.WriteString(" && ")
	}
	sb.WriteString("/opt/apply_patch_rs/apply_patch <<'")
	sb.WriteString(delim)
	sb.WriteString("'\n")
	sb.WriteString(patch)
	if !strings.HasSuffix(patch, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString(delim)
	return sb.String()
}

func chooseApplyPatchDelimiter(patch string) string {
	const base = "APPLY_PATCH_EOF"

	if !containsStandaloneLine(patch, base) {
		return base
	}

	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if !containsStandaloneLine(patch, candidate) {
			return candidate
		}
	}
}

func containsStandaloneLine(s string, line string) bool {
	for _, current := range strings.Split(s, "\n") {
		if current == line {
			return true
		}
	}
	return false
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func (A *AIInfraSandbox) ChangeDir(ctx context.Context, path string) error {
	next, err := A.resolvePath(path)
	if err != nil {
		return err
	}
	A.workDir = next
	return nil
}

func (A *AIInfraSandbox) resolvePath(path string) (string, error) {
	workDir := filepath.Clean(A.workDir)
	raw := strings.TrimSpace(path)
	if raw == "" || raw == "." {
		return workDir, nil
	}
	if strings.Contains(raw, "..") {
		return "", backends.ErrInvalidPath
	}
	if raw == "~" {
		return defaultSandboxHomeDir, nil
	}
	if strings.HasPrefix(raw, "~/") {
		return filepath.Clean(filepath.Join(defaultSandboxHomeDir, strings.TrimPrefix(raw, "~/"))), nil
	}

	var absPath string
	if filepath.IsAbs(raw) {
		absPath = raw
	} else {
		absPath = filepath.Join(workDir, raw)
	}
	absPath = filepath.Clean(absPath)
	return absPath, nil
}

func durationSecondsCeil(d time.Duration) int32 {
	sec := d / time.Second
	if d%time.Second != 0 {
		sec++
	}
	if sec < 1 {
		sec = 1
	}
	return int32(sec)
}

func combineBashOutput(resp *gateway.BashExecResponse) string {
	output := resp.GetStdout()
	if stderr := resp.GetStderr(); stderr != "" {
		if output != "" {
			output += "\n"
		}
		output += stderr
	}
	if output == "" {
		output = resp.GetHint()
	}
	return output
}

func isBashExecTimeout(status sandbox_model.OperationStatus) bool {
	return status == sandbox_model.OperationStatus_HARD_TIMEOUT ||
		status == sandbox_model.OperationStatus_NO_CHANGE_TIMEOUT
}
