package constant

import "time"

// ==================== HTTP 客户端默认值 ====================

// DefaultUserAgent 默认 User-Agent
const DefaultUserAgent = "Mozilla/5.0 (compatible; DeepAgent/1.0)"

// DefaultHTTPTimeout 默认 HTTP 请求超时
const DefaultHTTPTimeout = 30 * time.Second

// DefaultMaxRedirects 默认最大重定向次数
const DefaultMaxRedirects = 10

// ==================== 文件大小限制 ====================

// DefaultMaxBodySize 默认最大响应体大小 (5MB)
const DefaultMaxBodySize = 5 * 1024 * 1024

// DefaultHTTPMaxBodySize HTTP 请求默认最大响应体大小 (10MB)
const DefaultHTTPMaxBodySize = 10 * 1024 * 1024

// ==================== Web 搜索默认值 ====================

// DefaultWebSearchMaxResults 默认 Web 搜索最大结果数
const DefaultWebSearchMaxResults = 5

// DefaultWebSearchTopic 默认 Web 搜索主题
const DefaultWebSearchTopic = "general"

// ==================== 模板字符串 ====================

// DefaultAgentsMDTemplate 默认 AGENTS.md 模板
const DefaultAgentsMDTemplate = `# Agent 记忆

## 用户偏好

<!-- 在这里记录用户的偏好设置 -->

## 项目上下文

<!-- 在这里记录项目相关的重要信息 -->

## 学习笔记

<!-- 在这里记录从交互中学到的知识 -->

## 常用命令

<!-- 在这里记录经常使用的命令 -->
`

// ==================== 技能激活消息格式 ====================

// SkillActivatedMessageFormat 技能激活成功消息格式
// 使用 fmt.Sprintf(SkillActivatedMessageFormat, skillName, skillDir, skillDir, content)
const SkillActivatedMessageFormat = `技能 %s 已激活。

**重要**: 技能目录路径: %s
%s`

// SkillActivatedSimpleFormat 技能激活简单消息格式
// 使用 fmt.Sprintf(SkillActivatedSimpleFormat, skillName, skillDir)
const SkillActivatedSimpleFormat = "技能 %s 已激活\n技能目录: %s"
