package constant

// ==================== 文件系统工具 ====================

const (
	// ToolLs 列出目录内容
	ToolLs = "ls"

	// ToolReadFile 读取文件
	ToolReadFile = "read_file"

	// ToolWriteFile 写入文件
	ToolWriteFile = "write_file"

	// ToolEditFile 编辑文件
	ToolEditFile = "edit_file"

	// ToolGlob 模式匹配文件
	ToolGlob = "glob"

	// ToolGrep 搜索文件内容
	ToolGrep = "grep"

	// ToolExecute 执行命令
	ToolExecute = "execute"

	// ToolUploadFiles 上传文件
	ToolUploadFiles = "upload_files"

	// ToolDownloadFiles 下载文件
	ToolDownloadFiles = "download_files"

	ToolApplyPatch = "apply_patch"
)

// ==================== Planning 工具 ====================

const (
	// ToolUpdatePlan updates the user-visible progress checklist.
	ToolUpdatePlan = "update_plan"

	// ToolWriteTodos 创建任务列表
	ToolWriteTodos = "write_todos"

	// ToolReadTodos 读取任务列表
	ToolReadTodos = "read_todos"

	// ToolUpdateTodo 更新任务状态
	ToolUpdateTodo = "update_todo"

	// ToolDispatchTasks 并行派发子任务
	ToolDispatchTasks = "dispatch_tasks"
)

// ==================== Skills 工具 ====================

const (
	// ToolListSkills 列出技能
	ToolListSkills = "list_skills"

	// ToolActivateSkill 激活技能
	ToolActivateSkill = "activate_skill"

	// ToolDeactivateSkill 停用技能
	ToolDeactivateSkill = "deactivate_skill"
)

// ==================== SubAgent 工具 ====================

const (
	// ToolTask 委托任务给子代理
	ToolTask = "task"

	// ToolListSubAgents 列出子代理
	ToolListSubAgents = "list_subagents"
)

// ==================== Web 工具 ====================

const (
	// ToolFetchURL 获取 URL 内容
	ToolFetchURL = "fetch_url"

	// ToolWebSearch Web 搜索
	ToolWebSearch = "web_search"

	// ToolHTTPRequest HTTP 请求
	ToolHTTPRequest = "http_request"
)

// FilesystemToolNames 文件系统工具名称列表
var FilesystemToolNames = []string{
	ToolLs, ToolReadFile, ToolWriteFile, ToolEditFile,
	ToolApplyPatch, ToolGlob, ToolGrep, ToolExecute, ToolUploadFiles, ToolDownloadFiles,
}

// PlanningToolNames 规划工具名称列表
var PlanningToolNames = []string{
	ToolUpdatePlan, ToolWriteTodos, ToolReadTodos, ToolUpdateTodo, ToolDispatchTasks,
}

// SkillsToolNames 技能工具名称列表
var SkillsToolNames = []string{
	ToolListSkills, ToolActivateSkill, ToolDeactivateSkill,
}

// SubAgentToolNames 子代理工具名称列表
var SubAgentToolNames = []string{
	ToolTask, ToolListSubAgents,
}

// WebToolNames Web 工具名称列表
var WebToolNames = []string{
	ToolFetchURL, ToolWebSearch, ToolHTTPRequest,
}

// AllToolNames 所有内置工具名称（用于验证）
var AllToolNames = []string{
	// Filesystem
	ToolLs, ToolReadFile, ToolWriteFile, ToolEditFile,
	ToolApplyPatch, ToolGlob, ToolGrep, ToolExecute, ToolUploadFiles, ToolDownloadFiles,
	// Planning
	ToolUpdatePlan, ToolWriteTodos, ToolReadTodos, ToolUpdateTodo, ToolDispatchTasks,
	// Skills
	ToolListSkills, ToolActivateSkill, ToolDeactivateSkill,
	// SubAgent
	ToolTask, ToolListSubAgents,
	// Web
	ToolFetchURL, ToolWebSearch, ToolHTTPRequest,
}
