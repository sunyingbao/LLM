package tools

import (
	"context"

	"code.byted.org/gopkg/logs/v2"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ToolInfoVisible reports whether a tool should be exposed under mask.
// It keeps the existing fail-open semantics used by global ToolMask filtering.
func ToolInfoVisible(ctx context.Context, mask Mask, info *schema.ToolInfo, logPrefix string) bool {
	if mask == nil {
		return true
	}
	if info == nil {
		logs.CtxWarn(ctx, "[%s] tool info is nil, keep tool by default", logPrefix)
		return true
	}
	return mask(ctx, info)
}

func ToolNameVisible(ctx context.Context, mask Mask, name string, logPrefix string) bool {
	return ToolInfoVisible(ctx, mask, &schema.ToolInfo{Name: name}, logPrefix)
}

// FilterToolsByMask filters tools using their actual ToolInfo.
// Info failures keep the tool visible to match the framework-level fallback.
func FilterToolsByMask(ctx context.Context, toolList []tool.BaseTool, mask Mask, logPrefix string) []tool.BaseTool {
	if mask == nil || len(toolList) == 0 {
		return toolList
	}

	filtered := make([]tool.BaseTool, 0, len(toolList))
	for _, t := range toolList {
		info, err := t.Info(ctx)
		if err != nil {
			logs.CtxError(ctx, "[%s] get tool info failed, keep tool by default, err: %v", logPrefix, err)
			filtered = append(filtered, t)
			continue
		}
		if ToolInfoVisible(ctx, mask, info, logPrefix) {
			filtered = append(filtered, t)
			continue
		}
		logs.CtxInfo(ctx, "[%s] masked tool: %s", logPrefix, info.Name)
	}

	return filtered
}

// ToolNameSet returns the names of tools whose ToolInfo can be read.
func ToolNameSet(ctx context.Context, toolList []tool.BaseTool, logPrefix string) map[string]bool {
	names := make(map[string]bool, len(toolList))
	for _, t := range toolList {
		info, err := t.Info(ctx)
		if err != nil {
			logs.CtxError(ctx, "[%s] get tool info failed, skip tool name, err: %v", logPrefix, err)
			continue
		}
		if info == nil || info.Name == "" {
			continue
		}
		names[info.Name] = true
	}
	return names
}
