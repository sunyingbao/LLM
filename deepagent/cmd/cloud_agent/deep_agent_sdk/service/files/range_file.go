package files

import (
	"context"
	"errors"
	"time"

	cloudbackend "eino-cli/deepagent/cloud/backend"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/common"
	"eino-cli/deepagent/cmd/cloud_agent/deep_agent_sdk/service/deps"
	"eino-cli/deepagent/core/backends"
)

type RangeFile struct {
	ProjectPath string
	RelPath     string
	MediaType   string
	Size        int64
	ModTime     time.Time

	file *cloudbackend.RangeFile
}

func ResolveRangeFile(ctx context.Context, uid int64, sessionID int64, rawPath string) (*RangeFile, error) {
	if !cloudbackend.SupportsRangeRead(deps.Config().Backend) {
		return nil, cloudbackend.ErrRangeReadUnsupported
	}
	workspace, view, rel, err := resolvePath(ctx, uid, sessionID, rawPath)
	if err != nil {
		return nil, err
	}
	file, err := cloudbackend.OpenRangeFile(ctx, workspace, rel)
	if err != nil {
		return nil, err
	}
	return &RangeFile{
		ProjectPath: view.GetSession().GetProjectPath(),
		RelPath:     rel,
		MediaType:   mediaType(rel),
		Size:        file.Size,
		ModTime:     file.ModTime,
		file:        file,
	}, nil
}

func (f *RangeFile) ReadRange(start, end int64) ([]byte, error) {
	if f == nil || f.file == nil {
		return nil, common.InvalidArgument("invalid file path")
	}
	content, err := f.file.ReadRange(start, end)
	if err == nil {
		return content, nil
	}
	if errors.Is(err, backends.ErrFileNotFound) {
		return nil, common.InvalidArgument("file not found")
	}
	if errors.Is(err, backends.ErrInvalidPath) {
		return nil, common.InvalidArgument("invalid file path")
	}
	return nil, common.Internal("read backend file range", err)
}
