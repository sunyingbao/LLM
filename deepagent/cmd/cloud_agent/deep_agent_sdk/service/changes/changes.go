package changes

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	cloudbackend "eino-cli/deepagent/cloud/backend"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/deps"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/session"
	"eino-cli/deepagent/core/backends"
)

const (
	maxDiffBytes  = 2 * 1024 * 1024
	maxStatusSize = 1024 * 1024
)

type ListRequest struct {
	SessionID string `json:"session_id"`
}

type ListResponse struct {
	Changes []ChangeInfo `json:"changes"`
}

type DiffRequest struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
}

type DiffResponse struct {
	Path      string `json:"path"`
	Patch     string `json:"patch"`
	Truncated bool   `json:"truncated"`
}

type ChangeInfo struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

func List(ctx context.Context, uid int64, req *ListRequest) (resp *ListResponse, err error) {
	if req == nil || strings.TrimSpace(req.SessionID) == "" {
		return nil, common.InvalidArgument("session_id is required")
	}
	sessionID, err := strconv.ParseInt(strings.TrimSpace(req.SessionID), 10, 64)
	if err != nil || sessionID == 0 {
		return nil, common.InvalidArgument("invalid session_id")
	}
	backend, workDir, err := openWorkspace(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	changes, err := Collect(ctx, backend, workDir)
	if err != nil {
		return nil, err
	}
	return &ListResponse{Changes: changes}, nil
}

func Diff(ctx context.Context, uid int64, req *DiffRequest) (resp *DiffResponse, err error) {
	if req == nil {
		return nil, common.InvalidArgument("request is required")
	}
	sessionID, err := strconv.ParseInt(strings.TrimSpace(req.SessionID), 10, 64)
	if err != nil || sessionID == 0 {
		return nil, common.InvalidArgument("invalid session_id")
	}
	backend, workDir, err := openWorkspace(ctx, uid, sessionID)
	if err != nil {
		return nil, err
	}
	diff, err := CollectDiff(ctx, backend, workDir, req.Path)
	if err != nil {
		return nil, err
	}
	diff.Path = strings.TrimSpace(req.Path)
	return diff, nil
}

func Collect(ctx context.Context, backend backends.SandboxBackend, workDir string) (changes []ChangeInfo, err error) {
	changes = make([]ChangeInfo, 0)
	if backend == nil {
		return changes, common.InvalidArgument("backend is required")
	}
	statusOutput, statusCode, _, err := runGit(ctx, backend, workDir, maxStatusSize, "git -c core.quotepath=false status --porcelain=v1 -z --untracked-files=all")
	if err != nil {
		return nil, common.Internal("collect git status", err)
	}
	if statusCode != 0 {
		if strings.Contains(strings.ToLower(statusOutput), "not a git repository") {
			return changes, nil
		}
		return nil, common.Internal("collect git status", fmt.Errorf("%s", statusOutput))
	}

	pathStatus := parsePorcelainStatus(statusOutput)
	numStats := loadNumStats(ctx, backend, workDir, pathStatus)
	for filePath, status := range pathStatus {
		item := ChangeInfo{Path: filePath, Status: status}
		switch status {
		case "untracked":
			item.Additions, err = readLineCountForUntracked(ctx, backend, filePath)
			if err != nil {
				return nil, common.Internal("collect line count", err)
			}
			item.Deletions = 0
		default:
			stats := numStats[filePath]
			item.Additions = stats.Additions
			item.Deletions = stats.Deletions
		}
		changes = append(changes, item)
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})
	return changes, nil
}

func CollectDiff(ctx context.Context, backend backends.SandboxBackend, workDir string, rawPath string) (resp *DiffResponse, err error) {
	resp = &DiffResponse{}
	relativePath, err := cleanRelativePath(rawPath)
	if err != nil {
		return nil, common.InvalidArgument(err.Error())
	}
	if backend == nil {
		return nil, common.InvalidArgument("backend is required")
	}

	isTracked, err := isTracked(ctx, backend, workDir, relativePath)
	if err != nil {
		return nil, err
	}
	if !isTracked {
		resp.Patch, resp.Truncated, err = buildUntrackedDiff(ctx, backend, relativePath)
		if err != nil {
			return nil, err
		}
		resp.Path = relativePath
		return resp, nil
	}
	output, exitCode, truncated, err := runGit(ctx, backend, workDir, maxDiffBytes, "git diff --no-ext-diff --unified=3 HEAD -- "+shellQuote(relativePath))
	if err != nil {
		return nil, common.Internal("get tracked diff", err)
	}
	if exitCode != 0 {
		return nil, common.Internal("get tracked diff", fmt.Errorf("%s", output))
	}
	resp.Path = relativePath
	resp.Patch = output
	resp.Truncated = truncated
	return resp, nil
}

func openWorkspace(ctx context.Context, uid int64, sessionID int64) (backend backends.SandboxBackend, workDir string, err error) {
	view, err := session.RequireView(ctx, uid, sessionID, false)
	if err != nil {
		return nil, "", err
	}
	projectPath := strings.TrimSpace(view.GetSession().GetProjectPath())
	if projectPath == "" {
		return nil, "", common.InvalidArgument("session project_path is required")
	}
	workspace, err := cloudbackend.Open(ctx, deps.Config().Backend, cloudbackend.Target{
		UID:         uid,
		SessionID:   strconv.FormatInt(sessionID, 10),
		ProjectName: view.GetSession().GetProjectName(),
		ProjectPath: projectPath,
	})
	if err != nil {
		return nil, "", common.Internal("open backend workspace", err)
	}
	return workspace.Backend, workspace.WorkDir, nil
}

func runGit(ctx context.Context, backend backends.SandboxBackend, workDir string, maxOutputBytes int, cmd string) (output string, exitCode int, truncated bool, err error) {
	var result *backends.CommandResult
	result, err = backend.ExecuteCommand(ctx, backends.CommandRequest{
		Command:        cmd,
		WorkDir:        workDir,
		MaxOutputBytes: maxOutputBytes,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		return "", 0, false, err
	}
	return result.Output, result.ExitCode, result.Truncated, nil
}

func isTracked(ctx context.Context, backend backends.SandboxBackend, workDir string, relPath string) (tracked bool, err error) {
	output, exitCode, _, err := runGit(ctx, backend, workDir, maxStatusSize, "git ls-files --error-unmatch -- "+shellQuote(relPath))
	if err != nil {
		return false, common.Internal("check tracked file", err)
	}
	return exitCode == 0 && strings.TrimSpace(output) != "", nil
}

func readLineCountForUntracked(ctx context.Context, backend backends.SandboxBackend, relPath string) (lines int, err error) {
	content, err := backend.Read(ctx, relPath, nil, nil)
	if err != nil {
		return 0, err
	}
	if content == "" {
		return 0, nil
	}
	lines = strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lines++
	}
	return lines, nil
}

type lineCount struct {
	Additions int
	Deletions int
}

func loadNumStats(ctx context.Context, backend backends.SandboxBackend, workDir string, pathStatus map[string]string) (stats map[string]lineCount) {
	stats = make(map[string]lineCount, len(pathStatus))
	for filePath, status := range pathStatus {
		if status == "untracked" {
			continue
		}
		output, exitCode, _, cmdErr := runGit(ctx, backend, workDir, maxStatusSize, "git diff --numstat HEAD -- "+shellQuote(filePath))
		if cmdErr != nil || exitCode != 0 {
			continue
		}
		additions, deletions, parseErr := parseNumStatLine(output)
		if parseErr != nil {
			continue
		}
		stats[filePath] = lineCount{Additions: additions, Deletions: deletions}
	}
	return stats
}

func parseNumStatLine(raw string) (additions int, deletions int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, nil
	}
	parts := strings.Split(raw, "\t")
	if len(parts) < 3 {
		return 0, 0, fmt.Errorf("unexpected numstat output: %q", raw)
	}
	additions, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	deletions, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return additions, deletions, nil
}

func buildUntrackedDiff(ctx context.Context, backend backends.SandboxBackend, relPath string) (patch string, truncated bool, err error) {
	content, err := backend.Read(ctx, relPath, nil, nil)
	if err != nil {
		return "", false, err
	}
	lines := splitLinesForDiff(content)
	var b strings.Builder
	b.WriteString("diff --git a/")
	b.WriteString(relPath)
	b.WriteString(" b/")
	b.WriteString(relPath)
	b.WriteString("\nnew file mode 100644\n--- /dev/null\n+++ b/")
	b.WriteString(relPath)
	fmt.Fprintf(&b, "\n@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		b.WriteString("+")
		b.WriteString(line)
	}
	patch = b.String()
	if len(patch) > maxDiffBytes {
		patch = patch[:maxDiffBytes]
		truncated = true
	}
	return patch, truncated, nil
}

func splitLinesForDiff(content string) (lines []string) {
	lines = strings.SplitAfter(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}

func parsePorcelainStatus(raw string) map[string]string {
	entries := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return entries
	}
	for len(raw) > 0 {
		if len(raw) < 3 {
			break
		}
		statusCode := raw[:2]
		raw = raw[3:]
		nameEnd := strings.IndexByte(raw, 0)
		if nameEnd < 0 {
			break
		}
		primaryPath := raw[:nameEnd]
		raw = raw[nameEnd+1:]
		secondaryPath := ""
		if statusCode[0] == 'R' || statusCode[0] == 'C' {
			secondaryEnd := strings.IndexByte(raw, 0)
			if secondaryEnd < 0 {
				secondaryEnd = len(raw)
			}
			secondaryPath = raw[:secondaryEnd]
			raw = raw[secondaryEnd+1:]
		}
		targetPath := primaryPath
		if secondaryPath != "" {
			targetPath = secondaryPath
		}
		entries[targetPath] = mapStatusCode(statusCode)
	}
	return entries
}

func mapStatusCode(code string) string {
	if code == "??" {
		return "untracked"
	}
	if strings.ContainsRune(code, 'D') {
		return "deleted"
	}
	if strings.ContainsRune(code, 'A') {
		return "added"
	}
	if strings.ContainsRune(code, 'R') {
		return "renamed"
	}
	if strings.ContainsRune(code, 'C') {
		return "copied"
	}
	if strings.ContainsRune(code, 'M') {
		return "modified"
	}
	return "modified"
}

func cleanRelativePath(raw string) (string, error) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if raw == "" || raw == "." {
		return "", fmt.Errorf("path is required")
	}
	if strings.ContainsAny(raw, "\x00\r\n") {
		return "", fmt.Errorf("invalid relative path")
	}
	if path.IsAbs(raw) || strings.HasPrefix(raw, "~") {
		return "", fmt.Errorf("path must be relative")
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == "/" {
		return "", fmt.Errorf("path is required")
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("invalid relative path")
	}
	return cleaned, nil
}

func shellQuote(raw string) string {
	return "'" + strings.ReplaceAll(raw, "'", `'"'"'`) + "'"
}
