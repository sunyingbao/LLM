// Package constant 提供 deepagents 包的常量定义
//
// 此包集中管理所有常量，包括：
// - Agent 核心配置常量
// - 中间件名称和配置
// - 工具名称
// - 重试相关常量
// - 错误消息
//
// 使用示例：
//
//	import "eino-cli/deepagent/core/constant"
//
//	// 使用工具名称
//	toolName := constant.ToolReadFile
//
//	// 使用中间件名称
//	name := constant.MiddlewareFilesystem
//
//	// 使用默认配置
//	maxSteps := constant.DefaultMaxSteps
package constant

// Version 包版本
const Version = "1.0.0"
