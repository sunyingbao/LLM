// Package tracing 提供追踪和日志能力
package tracing

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Stage 定义执行阶段
type Stage string

const (
	// Agent 生命周期阶段
	StageAgentInit   Stage = "agent_init"
	StageAgentRun    Stage = "agent_run"
	StageAgentStream Stage = "agent_stream"
	StageAgentClose  Stage = "agent_close"

	// Graph 执行阶段
	StageGraphBuild   Stage = "graph_build"
	StageGraphCompile Stage = "graph_compile"
	StageGraphInvoke  Stage = "graph_invoke"

	// LLM 调用阶段
	StageLLMCall     Stage = "llm_call"
	StageLLMStream   Stage = "llm_stream"
	StageLLMResponse Stage = "llm_response"

	// 工具执行阶段
	StageToolExecution Stage = "tool_execution"
	StageToolParsing   Stage = "tool_parsing"

	// 中间件阶段
	StageMiddlewareOnStart Stage = "middleware_on_start"
	StageMiddlewareOnEnd   Stage = "middleware_on_end"
	StageMiddlewareTools   Stage = "middleware_tools"
)

// Source 定义错误来源
type Source string

const (
	SourceEinoLib    Source = "aic_agent_sdk"
	SourceLLMAPI     Source = "llm_api"
	SourceTool       Source = "tool"
	SourceMiddleware Source = "middleware"
	SourceGraph      Source = "graph"
)

// RequestIDKey context key for request ID
type RequestIDKey struct{}

// GetRequestID 从 context 获取 request ID
func GetRequestID(ctx context.Context) string {
	if v := ctx.Value(RequestIDKey{}); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

// WithRequestID 设置 request ID 到 context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey{}, requestID)
}

// Logger 追踪日志记录器
type Logger struct {
	Component string
	RequestID string
}

// NewLogger 创建日志记录器
func NewLogger(component string, requestID string) *Logger {
	return &Logger{
		Component: component,
		RequestID: requestID,
	}
}

// NewLoggerFromContext 从 context 创建日志记录器
func NewLoggerFromContext(ctx context.Context, component string) *Logger {
	return &Logger{
		Component: component,
		RequestID: GetRequestID(ctx),
	}
}

// Start 记录阶段开始，返回计时器
func (l *Logger) Start(stage Stage, extra ...string) *Timer {
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = " | " + extra[0]
	}
	log.Printf("[%s] request_id=%s | stage=%s | status=start%s",
		l.Component, l.RequestID, stage, extraInfo)
	return &Timer{
		logger:    l,
		stage:     stage,
		startTime: time.Now(),
	}
}

// Success 记录成功
func (l *Logger) Success(stage Stage, extra ...string) {
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = " | " + extra[0]
	}
	log.Printf("[%s] request_id=%s | stage=%s | status=success%s",
		l.Component, l.RequestID, stage, extraInfo)
}

// Error 记录错误
func (l *Logger) Error(stage Stage, err error, source Source, extra ...string) {
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = " | " + extra[0]
	}
	log.Printf("[%s] request_id=%s | stage=%s | status=error | source=%s | error=%q%s",
		l.Component, l.RequestID, stage, source, err.Error(), extraInfo)
}

// Info 记录信息
func (l *Logger) Info(message string, extra ...string) {
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = " | " + extra[0]
	}
	log.Printf("[%s] request_id=%s | %s%s",
		l.Component, l.RequestID, message, extraInfo)
}

// Warn 记录警告
func (l *Logger) Warn(message string, extra ...string) {
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = " | " + extra[0]
	}
	log.Printf("[%s] request_id=%s | WARN: %s%s",
		l.Component, l.RequestID, message, extraInfo)
}

// Timer 计时器
type Timer struct {
	logger    *Logger
	stage     Stage
	startTime time.Time
}

// Success 记录成功完成
func (t *Timer) Success(extra ...string) {
	duration := time.Since(t.startTime)
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = " | " + extra[0]
	}
	log.Printf("[%s] request_id=%s | stage=%s | status=success | duration=%v%s",
		t.logger.Component, t.logger.RequestID, t.stage, duration, extraInfo)
}

// Error 记录失败
func (t *Timer) Error(err error, source Source, extra ...string) {
	duration := time.Since(t.startTime)
	extraInfo := ""
	if len(extra) > 0 {
		extraInfo = " | " + extra[0]
	}
	log.Printf("[%s] request_id=%s | stage=%s | status=error | source=%s | duration=%v | error=%q%s",
		t.logger.Component, t.logger.RequestID, t.stage, source, duration, err.Error(), extraInfo)
}

// TracedError 带追踪信息的错误
type TracedError struct {
	Stage     Stage
	Source    Source
	RequestID string
	Cause     error
	Extra     map[string]interface{}
}

func (e *TracedError) Error() string {
	return fmt.Sprintf("[%s@%s] %s (request_id=%s)", e.Stage, e.Source, e.Cause.Error(), e.RequestID)
}

func (e *TracedError) Unwrap() error {
	return e.Cause
}

// WrapError 包装错误
func WrapError(err error, stage Stage, source Source, requestID string) error {
	if err == nil {
		return nil
	}
	return &TracedError{
		Stage:     stage,
		Source:    source,
		RequestID: requestID,
		Cause:     err,
	}
}

// WrapErrorWithExtra 包装错误并添加额外信息
func WrapErrorWithExtra(err error, stage Stage, source Source, requestID string, extra map[string]interface{}) error {
	if err == nil {
		return nil
	}
	return &TracedError{
		Stage:     stage,
		Source:    source,
		RequestID: requestID,
		Cause:     err,
		Extra:     extra,
	}
}
