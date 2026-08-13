package constant

// ==================== 错误消息 ====================

const (

	// ErrMsgContextManagerRequired 上下文管理器必需错误
	ErrMsgContextManagerRequired = "context manager is required"

	// ErrMsgModelRequired 模型必需错误
	ErrMsgModelRequired = "model is required"

	// ErrMsgToolNotAvailable 工具不可用错误
	ErrMsgToolNotAvailable = "工具不可用"

	// ErrMsgEmptyFileList 空文件列表错误
	ErrMsgEmptyFileList = "文件列表不能为空"

	// ErrMsgEmptyPathList 空路径列表错误
	ErrMsgEmptyPathList = "文件路径列表不能为空"

	// ErrMsgMissingTaskID 缺少任务ID错误
	ErrMsgMissingTaskID = "缺少任务ID参数 (todo_id 或 id)"

	// ErrMsgTaskNotFound 任务未找到错误
	ErrMsgTaskNotFound = "未找到任务"

	// ErrMsgSubAgentNotFound 子代理未找到错误
	ErrMsgSubAgentNotFound = "未找到子代理"

	// ErrMsgSkillNotFound 技能未找到错误
	ErrMsgSkillNotFound = "技能不存在"

	// ErrMsgApprovalDenied 审批拒绝错误
	ErrMsgApprovalDenied = "工具执行被拒绝"

	// ErrMsgNoCallbackNoApprove 无回调且不批准错误
	ErrMsgNoCallbackNoApprove = "未配置审批回调且默认不批准"

	// ErrMsgSkillMissingName 技能缺少名称错误
	ErrMsgSkillMissingName = "技能缺少名称"

	// ErrMsgSkillNameTooLong 技能名称过长
	ErrMsgSkillNameTooLong = "技能名称超过 64 字符限制"

	// ErrMsgSkillNameInvalid 技能名称格式无效
	ErrMsgSkillNameInvalid = "技能名称只能包含小写字母、数字和连字符，且不能以连字符开头或结尾"

	// ErrMsgSkillNameMismatch 技能名称与目录名不匹配
	ErrMsgSkillNameMismatch = "技能名称必须与目录名一致"

	// ErrMsgSkillFileTooLarge 技能文件过大
	ErrMsgSkillFileTooLarge = "技能文件超过 10MB 大小限制"

	// ErrMsgSkillMissingDescription 技能缺少描述
	ErrMsgSkillMissingDescription = "技能缺少描述"

	// ErrMsgSubAgentNoModel 子代理无模型错误
	ErrMsgSubAgentNoModel = "子代理没有配置模型"

	// ErrMsgSubAgentExecFailed 子代理执行失败错误
	ErrMsgSubAgentExecFailed = "子代理执行失败"

	// ErrMsgExecuteNotAvailable execute 命令不可用
	ErrMsgExecuteNotAvailable = "execute 命令不可用"

	// ErrMsgTodoNotInProgress 任务不在 in_progress 状态
	ErrMsgTodoNotInProgress = "任务不在 in_progress 状态，无法派发子任务"

	// ErrMsgEmptySubtasks 空子任务列表
	ErrMsgEmptySubtasks = "子任务列表不能为空"

	// ErrMsgNoSubAgentMiddleware 未配置子代理中间件
	ErrMsgNoSubAgentMiddleware = "未配置子代理中间件，无法执行 dispatch_tasks"
)

// ==================== 用户提示消息 ====================

const (
	// MsgDirectoryEmpty 目录为空
	MsgDirectoryEmpty = "目录为空"

	// MsgNoMatchingFiles 未找到匹配文件
	MsgNoMatchingFiles = "未找到匹配的文件"

	// MsgNoMatchingContent 未找到匹配内容
	MsgNoMatchingContent = "未找到匹配"

	// MsgNoTasks 没有任务
	MsgNoTasks = "当前没有任务"

	// MsgNoSubAgents 没有子代理
	MsgNoSubAgents = "当前没有可用的子代理"

	// MsgNoSkills 没有技能
	MsgNoSkills = "当前没有可用的技能"

	// MsgMaxStepsReached 达到最大步数
	MsgMaxStepsReached = "达到最大执行步数限制"

	// MsgToolCallCancelled 工具调用已取消（需要 fmt.Sprintf）
	MsgToolCallCancelled = "工具调用 %s (ID: %s) 已取消 - 在完成之前收到了新消息"

	// MsgFileWritten 文件已写入（需要 fmt.Sprintf）
	MsgFileWritten = "文件已写入: %s (%d 字节)"

	// MsgReplacedN 已替换N处（需要 fmt.Sprintf）
	MsgReplacedN = "已替换 %d 处匹配"

	// MsgNoReplacement 未找到要替换的字符串
	MsgNoReplacement = "未找到要替换的字符串"

	// MsgTaskUpdated 任务已更新（需要 fmt.Sprintf）
	MsgTaskUpdated = "任务 %s 已更新为 %s"

	// MsgSkillActivated 技能已激活（需要 fmt.Sprintf）
	MsgSkillActivated = "技能 %s 已激活"

	// MsgSkillDeactivated 技能已停用（需要 fmt.Sprintf）
	MsgSkillDeactivated = "技能 %s 已停用"

	// MsgSkillNotActive 技能未激活（需要 fmt.Sprintf）
	MsgSkillNotActive = "技能 %s 未激活"

	// MsgResultsTruncated 结果已截断
	MsgResultsTruncated = "\n... 结果已截断，共 100+ 匹配\n"

	// MsgCreatedNTasks 已创建N个任务（需要 fmt.Sprintf）
	MsgCreatedNTasks = "已创建 %d 个任务:\n"
)
