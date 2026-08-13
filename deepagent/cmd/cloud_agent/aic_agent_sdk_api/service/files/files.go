package files

import (
	"context"
	"errors"
	"mime"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	cloudbackend "eino-cli/deepagent/cloud/backend"
	httpcommon "eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/hertz_gen/aic_agent_sdk_common"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/deps"
	"eino-cli/deepagent/cmd/cloud_agent/aic_agent_sdk_api/service/session"
	"eino-cli/deepagent/core/backends"
)

const maxListEntries = 300

type ListRequest struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
}

type ListResponse struct {
	Files []*FileInfo `json:"files"`
}

type FileInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	IsSymlink bool   `json:"is_symlink,omitempty"`
	Size      int64  `json:"size,omitempty"`
	ModTimeMS int64  `json:"mod_time_ms,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type ResolvedFile struct {
	ProjectPath string
	RelPath     string
	MediaType   string
	Size        int64
	Content     []byte
}

func List(ctx context.Context, uid int64, req *ListRequest) (*ListResponse, error) {
	if req == nil || strings.TrimSpace(req.SessionID) == "" {
		return nil, common.InvalidArgument("session_id is required")
	}
	sessionID, err := strconv.ParseInt(strings.TrimSpace(req.SessionID), 10, 64)
	if err != nil || sessionID == 0 {
		return nil, common.InvalidArgument("invalid session_id")
	}
	workspace, _, rel, err := resolvePath(ctx, uid, sessionID, req.Path)
	if err != nil {
		return nil, err
	}
	entries, err := workspace.Backend.LsInfo(ctx, rel)
	if err != nil {
		if errors.Is(err, backends.ErrFileNotFound) {
			return &ListResponse{Files: nil}, nil
		}
		if errors.Is(err, backends.ErrInvalidPath) {
			return nil, common.InvalidArgument("path must be a directory")
		}
		return nil, common.Internal("list backend directory", err)
	}
	if len(entries) > maxListEntries {
		entries = entries[:maxListEntries]
	}
	files := make([]*FileInfo, 0, len(entries))
	for _, entry := range entries {
		entryRel := displayPath(workspace.WorkDir, entry.Path)
		files = append(files, &FileInfo{
			Name:      path.Base(entryRel),
			Path:      cleanSlashPath(entryRel),
			IsDir:     entry.IsDir,
			IsSymlink: entry.IsSymlink,
			Size:      entry.Size,
			ModTimeMS: entry.ModifiedAt.UnixMilli(),
			MediaType: mediaType(entryRel),
		})
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	return &ListResponse{Files: files}, nil
}

func ResolveFile(ctx context.Context, uid int64, sessionID int64, rawPath string) (*ResolvedFile, error) {
	if sessionID == 0 {
		return nil, common.InvalidArgument("session_id is required")
	}
	workspace, view, rel, err := resolvePath(ctx, uid, sessionID, rawPath)
	if err != nil {
		return nil, err
	}
	content, err := cloudbackend.ReadFile(ctx, workspace.Backend, rel)
	if err != nil {
		if errors.Is(err, backends.ErrFileNotFound) {
			return nil, common.InvalidArgument("file not found")
		}
		if errors.Is(err, backends.ErrIsDirectory) {
			return nil, common.InvalidArgument("path is a directory")
		}
		if errors.Is(err, backends.ErrInvalidPath) {
			return nil, common.InvalidArgument("invalid file path")
		}
		return nil, common.Internal("read backend file", err)
	}
	return &ResolvedFile{
		ProjectPath: view.GetSession().GetProjectPath(),
		RelPath:     rel,
		MediaType:   mediaType(rel),
		Size:        int64(len(content)),
		Content:     content,
	}, nil
}

func resolvePath(ctx context.Context, uid int64, sessionID int64, rawPath string) (*cloudbackend.Workspace, *httpcommon.AgentSessionView, string, error) {
	view, err := session.RequireView(ctx, uid, sessionID, false)
	if err != nil {
		return nil, nil, "", err
	}
	rel, err := cleanRelativePath(rawPath)
	if err != nil {
		return nil, nil, "", err
	}
	projectPath := strings.TrimSpace(view.GetSession().GetProjectPath())
	if projectPath == "" {
		return nil, nil, "", common.InvalidArgument("session project_path is required")
	}
	workspace, err := cloudbackend.Open(ctx, deps.Config().Backend, cloudbackend.Target{
		UID:         uid,
		SessionID:   strconv.FormatInt(sessionID, 10),
		ProjectName: view.GetSession().GetProjectName(),
		ProjectPath: projectPath,
	})
	if err != nil {
		return nil, nil, "", common.Internal("open backend workspace", err)
	}
	return workspace, view, rel, nil
}

func cleanRelativePath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" || raw == "." || raw == "/" {
		return ".", nil
	}
	if path.IsAbs(raw) || strings.HasPrefix(raw, "~") {
		return "", common.InvalidArgument("path must be relative")
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == ".." {
			return "", common.InvalidArgument("invalid relative path")
		}
	}
	clean := path.Clean("/" + raw)
	if clean == "/" {
		return ".", nil
	}
	rel := strings.TrimPrefix(clean, "/")
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || rel == ".." || strings.Contains(rel, "/../") {
		return "", common.InvalidArgument("invalid relative path")
	}
	return cleanSlashPath(rel), nil
}

func cleanSlashPath(p string) string {
	p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
	if p == "." || p == "/" {
		return "."
	}
	return strings.TrimPrefix(p, "/")
}

func displayPath(workDir string, raw string) string {
	raw = filepath.ToSlash(filepath.Clean(raw))
	workDir = filepath.ToSlash(filepath.Clean(workDir))
	if path.IsAbs(raw) && workDir != "" {
		if raw == workDir {
			return "."
		}
		if strings.HasPrefix(raw, workDir+"/") {
			return strings.TrimPrefix(raw, workDir+"/")
		}
	}
	return raw
}

func mediaType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return "application/octet-stream"
	}
	if mt := mime.TypeByExtension(ext); mt != "" {
		return mt
	}
	switch ext {
	case ".md", ".txt", ".log", ".json", ".yaml", ".yml", ".go", ".js", ".ts", ".css", ".html", ".py", ".sh", ".c", ".cc", ".cpp", ".cu", ".h", ".hpp":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
