package toolerrors

import (
	"strings"

	"github.com/cloudwego/eino/compose"
)

// ShouldReturnAsResult reports whether an Eino local tool error was caused by
// model-produced arguments rather than runtime tool execution.
func ShouldReturnAsResult(err error) bool {
	if err == nil || IsControlFlow(err) {
		return false
	}

	msg := err.Error()
	return strings.HasPrefix(msg, "[LocalFunc] failed to unmarshal arguments") ||
		strings.HasPrefix(msg, "[LocalFunc] invalid type") ||
		strings.HasPrefix(msg, "[EnhancedLocalFunc] failed to unmarshal arguments") ||
		strings.HasPrefix(msg, "[EnhancedLocalFunc] invalid type") ||
		strings.HasPrefix(msg, "[LocalStreamFunc] failed to unmarshal arguments") ||
		strings.HasPrefix(msg, "[LocalStreamFunc] type err") ||
		strings.HasPrefix(msg, "[EnhancedLocalStreamFunc] failed to unmarshal arguments") ||
		strings.HasPrefix(msg, "[EnhancedLocalStreamFunc] type err")
}

func Result(err error) string {
	return "[Error] Tool invocation failed: " + err.Error()
}

func IsControlFlow(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := compose.ExtractInterruptInfo(err); ok {
		return true
	}
	if _, ok := compose.IsInterruptRerunError(err); ok {
		return true
	}
	// Raw StatefulInterrupt errors are produced before the compose graph wraps
	// them into an interruptError.
	return strings.HasPrefix(err.Error(), "interrupt signal:")
}
