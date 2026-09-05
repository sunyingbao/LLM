package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"eino-cli/deepagent/backend/consts"
	"eino-cli/deepagent/backend/sandbox"
	"eino-cli/deepagent/backend/sandboxpaths"
	runtimecontext "eino-cli/deepagent/host/executioncontext"
)

const deleteFileToolDesc = `Delete a file at a workspace path. Directories are refused. Missing files are reported as a normal tool result.`

type deleteFileArgs struct {
	FilePath string `json:"file_path" jsonschema:"required,description=Absolute or workspace-relative file path to delete"`
}

// GetDeleteFileTool returns the delete_file tool.
func GetDeleteFileTool(sandboxManager sandbox.SandboxManager) (baseTool tool.BaseTool, err error) {
	return utils.InferTool("delete_file", deleteFileToolDesc,
		func(ctx context.Context, in deleteFileArgs) (output string, err error) {
			if msg, denied := denyOnPlanMode(ctx); denied {
				return msg, nil
			}
			var path, displayPath string
			if hasSandboxManager(sandboxManager) {
				virtualPath, err := resolveToolPath(in.FilePath, false)
				if err != nil {
					return "", err
				}
				sessionID := runtimecontext.GetSessionID(ctx)
				if sessionID == "" {
					sessionID = consts.DefaultSessionID
				}
				mappings, err := sandboxpaths.BuildMountMappings(sessionID)
				if err != nil {
					return "", err
				}
				path, err = sandboxpaths.GetHostPath(mappings, virtualPath)
				if err != nil {
					return "", err
				}
				displayPath = virtualPath
			} else {
				path, err = getResolvedPath(in.FilePath)
				if err != nil {
					return "", err
				}
				displayPath = path
			}
			info, err := os.Lstat(path)
			if err != nil {
				if os.IsNotExist(err) {
					return "File does not exist: " + displayPath, nil
				}
				return "", err
			}
			if info.IsDir() {
				return "", fmt.Errorf("refusing to delete directory: %s", displayPath)
			}
			if err := os.Remove(path); err != nil {
				return "", err
			}
			return "Deleted file " + displayPath, nil
		})
}
