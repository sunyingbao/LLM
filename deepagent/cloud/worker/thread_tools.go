//go:build !windows

package worker

import (
	"context"
	deepagents "eino-cli/deepagent/core"
	"eino-cli/deepagent/core/constant"
	"eino-cli/deepagent/core/middleware"
	deeptools "eino-cli/deepagent/core/tools"
	"eino-cli/deepagent/worker/tasktool"
	"strings"

	"github.com/cloudwego/eino/schema"
)

type roleTurnLevelConfig struct {
	Filesystem      *deepagents.FilesystemConfig
	Middlewares     []middleware.Middleware
	ToolMask        deeptools.Mask
	ToolPolicyGates map[string]deeptools.ToolPolicyGate
}

func roleToolMask(role string) deeptools.Mask {
	switch strings.TrimSpace(role) {
	case "explorer", "worker":
		return hideCollabTools
	default:
		return nil
	}
}

func hideCollabTools(_ context.Context, info *schema.ToolInfo) bool {
	if info == nil {
		return true
	}
	switch info.Name {
	case tasktool.ToolSpawnTask, tasktool.ToolSendMessage, tasktool.ToolWaitMessage, tasktool.ToolCloseTask:
		return false
	default:
		return true
	}
}

func planToolMask(_ context.Context, info *schema.ToolInfo) bool {
	if info == nil {
		return true
	}
	switch info.Name {
	case constant.ToolWriteFile,
		constant.ToolEditFile,
		constant.ToolExecute,
		constant.ToolUploadFiles,
		constant.ToolApplyPatch,
		constant.ToolUpdatePlan,
		constant.ToolWriteTodos,
		constant.ToolUpdateTodo,
		constant.ToolDispatchTasks,
		constant.ToolTask,
		tasktool.ToolSpawnTask,
		tasktool.ToolSendMessage,
		tasktool.ToolWaitMessage,
		tasktool.ToolCloseTask:
		return false
	default:
		return true
	}
}
