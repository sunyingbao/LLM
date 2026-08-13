//go:build !windows

package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"eino-cli/deepagent/core/backends"
)

var ErrRangeReadUnsupported = errors.New("backend range read unsupported")

func SupportsRangeRead(cfg Config) bool {
	return Normalize(cfg).Type == TypeLocal
}

// RangeFile exposes byte-range reads for backend implementations that can open
// a local file without downloading the whole object first.
type RangeFile struct {
	Size    int64
	ModTime time.Time

	path string
}

func OpenRangeFile(ctx context.Context, workspace *Workspace, rel string) (*RangeFile, error) {
	if workspace == nil || workspace.Backend == nil {
		return nil, backends.ErrInvalidPath
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if _, ok := workspace.Backend.(*backends.SandboxFilesystemBackend); !ok {
		return nil, ErrRangeReadUnsupported
	}
	return openLocalRangeFile(workspace.WorkDir, rel)
}

func openLocalRangeFile(workDir string, rel string) (*RangeFile, error) {
	workDir = filepath.Clean(workDir)
	rel = filepath.FromSlash(strings.TrimSpace(rel))
	if workDir == "" || rel == "" || filepath.IsAbs(rel) {
		return nil, backends.ErrInvalidPath
	}
	abs := filepath.Clean(filepath.Join(workDir, rel))
	if abs != workDir && !strings.HasPrefix(abs, workDir+string(filepath.Separator)) {
		return nil, backends.ErrInvalidPath
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, backends.ErrFileNotFound
		}
		if os.IsPermission(err) {
			return nil, backends.ErrPermissionDenied
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, backends.ErrInvalidPath
	}
	if info.IsDir() {
		return nil, backends.ErrIsDirectory
	}
	return &RangeFile{
		Size:    info.Size(),
		ModTime: info.ModTime(),
		path:    abs,
	}, nil
}

func (f *RangeFile) ReadRange(start, end int64) ([]byte, error) {
	if f == nil || start < 0 || end < start || end >= f.Size {
		return nil, backends.ErrInvalidPath
	}
	file, err := os.Open(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, backends.ErrFileNotFound
		}
		if os.IsPermission(err) {
			return nil, backends.ErrPermissionDenied
		}
		return nil, err
	}
	defer file.Close()
	size := end - start + 1
	if size > int64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("byte range is too large")
	}
	buf := make([]byte, int(size))
	if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read file range: %w", err)
	}
	return buf, nil
}
