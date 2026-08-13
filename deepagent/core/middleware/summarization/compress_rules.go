package summarization

import "eino-cli/deepagent/core/constant"

// BuiltinToolCompressRules 内置工具的压缩规则
// 这些规则不可被用户覆盖
var BuiltinToolCompressRules = map[string]*ToolCompressRule{
	// ===== 文件系统工具 =====
	constant.ToolLs: {
		ToolName: constant.ToolLs,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"path"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 500},
	},
	constant.ToolReadFile: {
		ToolName: constant.ToolReadFile,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"path", "offset", "limit"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModeFull}, // 文件内容全量压缩
	},
	constant.ToolWriteFile: {
		ToolName: constant.ToolWriteFile,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"path"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModeFull},
	},
	constant.ToolEditFile: {
		ToolName: constant.ToolEditFile,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"path"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix},
	},
	constant.ToolGlob: {
		ToolName: constant.ToolGlob,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"pattern", "path"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModeFull},
	},
	constant.ToolGrep: {
		ToolName: constant.ToolGrep,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"pattern", "path", "include"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModeFull},
	},
	constant.ToolExecute: {
		ToolName: constant.ToolExecute,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"command"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 500},
	},

	// ===== 文件传输工具 =====
	constant.ToolUploadFiles: {
		ToolName: constant.ToolUploadFiles,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"paths"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 300},
	},
	constant.ToolDownloadFiles: {
		ToolName: constant.ToolDownloadFiles,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"urls", "destination"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 300},
	},

	// ===== TODO 工具 =====
	constant.ToolWriteTodos: {
		ToolName: constant.ToolWriteTodos,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeFull},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 200},
	},
	constant.ToolReadTodos: {
		ToolName: constant.ToolReadTodos,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeFull},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 500},
	},
	constant.ToolUpdateTodo: {
		ToolName: constant.ToolUpdateTodo,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"id", "status"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 200},
	},

	// ===== Skills 工具 =====
	constant.ToolListSkills: {
		ToolName: constant.ToolListSkills,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeFull},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 500},
	},
	constant.ToolActivateSkill: {
		ToolName: constant.ToolActivateSkill,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"name"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModeFull}, // 技能内容可能很长
	},
	constant.ToolDeactivateSkill: {
		ToolName: constant.ToolDeactivateSkill,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"name"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 100},
	},

	// ===== 子代理工具 =====
	constant.ToolTask: {
		ToolName: constant.ToolTask,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"agent", "task"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 1000},
	},
	constant.ToolListSubAgents: {
		ToolName: constant.ToolListSubAgents,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeFull},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 500},
	},

	// ===== 网络工具 =====
	constant.ToolFetchURL: {
		ToolName: constant.ToolFetchURL,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"url"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 500},
	},
	constant.ToolWebSearch: {
		ToolName: constant.ToolWebSearch,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"query"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 1000},
	},
	constant.ToolHTTPRequest: {
		ToolName: constant.ToolHTTPRequest,
		Args:     &ArgsCompressRule{Mode: ArgsCompressModeKeepFields, Fields: []string{"method", "url"}},
		Result:   &ResultCompressRule{Mode: ResultCompressModePrefix, PrefixLen: 500},
	},
}

// IsBuiltinTool 判断是否为内置工具
func IsBuiltinTool(toolName string) bool {
	_, ok := BuiltinToolCompressRules[toolName]
	return ok
}
