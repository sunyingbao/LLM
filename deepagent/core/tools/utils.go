package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/kaptinlin/jsonrepair"
)

var ErrInvalidJSONInput = errors.New("invalid json input")

// RepairJSON 修复 LLM 输出的畸形 JSON
// 处理常见的 LLM 输出问题：
// - 去除特殊分隔符 (<|FunctionCallBegin|>, <think> 等)
// - 修复不完整的 JSON
// - 修复无效的 JSON 值（Infinity、NaN 等 JavaScript 特有值）
// - 修复字符串内未转义的特殊字符（换行符、制表符等）
func RepairJSON(input string) string {
	repaired, _ := RepairJSONWithError(input)
	return repaired
}

// RepairJSONWithError repairs common malformed tool-call JSON while reporting
// inputs that still cannot become valid JSON.
func RepairJSONWithError(input string) (string, error) {
	input, ok := normalizeJSONCandidate(input)
	if !ok {
		return input, fmt.Errorf("%w: no json object or array found", ErrInvalidJSONInput)
	}
	extracted, extractedOK, blocked := extractBalancedJSONCandidate(input)
	if blocked {
		return input, fmt.Errorf("%w: trailing json continuation after balanced value", ErrInvalidJSONInput)
	}
	if extractedOK {
		repaired, extractedErr := repairJSONCandidate(extracted)
		if extractedErr == nil {
			return repaired, nil
		}
	}
	repaired, err := repairJSONCandidate(input)
	if err == nil {
		return repaired, nil
	}
	return input, err
}

func repairJSONCandidate(input string) (string, error) {
	if json.Valid([]byte(input)) {
		return input, nil
	}

	input = repairInvalidJSONValues(input)
	if json.Valid([]byte(input)) {
		return input, nil
	}

	repaired, err := jsonrepair.JSONRepair(input)
	if err != nil {
		return input, fmt.Errorf("%w: %v", ErrInvalidJSONInput, err)
	}
	repaired = strings.TrimSpace(repaired)
	if !json.Valid([]byte(repaired)) {
		return repaired, fmt.Errorf("%w: repaired output is not valid json", ErrInvalidJSONInput)
	}
	return repaired, nil
}

func normalizeJSONCandidate(input string) (string, bool) {
	// 去除常见的模型特定分隔符
	input = strings.TrimPrefix(input, "<|FunctionCallBegin|>")
	input = strings.TrimSuffix(input, "<|FunctionCallEnd|>")
	input = strings.TrimPrefix(input, "<think>")
	input = strings.TrimSuffix(input, "</think>")

	// 去除首尾空白
	input = strings.TrimSpace(input)

	// 如果为空，返回空 JSON 对象
	if input == "" {
		return "{}", true
	}

	if json.Valid([]byte(input)) {
		return input, true
	}

	// 查找第一个 { 或 [
	start := strings.IndexAny(input, "{[")
	if start == -1 {
		return input, false
	}

	return input[start:], true
}

func extractBalancedJSONCandidate(input string) (candidate string, ok bool, blocked bool) {
	if input == "" {
		return "", false, false
	}
	end := findMatchingBracket(input, 0)
	if end == -1 || end == len(input)-1 {
		return "", false, false
	}

	candidate = input[:end+1]
	trailing := strings.TrimSpace(input[end+1:])
	if trailing == "" {
		return "", false, false
	}
	if json.Valid([]byte(candidate)) {
		if startsLikeJSONContinuation(trailing) {
			return "", false, true
		}
		return candidate, true, false
	}
	if startsLikeJSONContinuation(trailing) {
		return "", false, true
	}
	return candidate, true, false
}

func startsLikeJSONContinuation(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch s[0] {
	case '{', '[', ':', '"', '\'':
		return true
	case ',':
		return commaContinuesJSON(s[1:])
	default:
		return false
	}
}

func commaContinuesJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch s[0] {
	case '{', '[', '"', '\'', '-', '+':
		return true
	}
	if s[0] >= '0' && s[0] <= '9' {
		return true
	}
	return strings.HasPrefix(s, "true") ||
		strings.HasPrefix(s, "false") ||
		strings.HasPrefix(s, "null") ||
		strings.HasPrefix(s, "Infinity") ||
		strings.HasPrefix(s, "NaN")
}

// repairUnescapedControlChars 修复字符串内未转义的控制字符
// JSON 规范要求字符串内的控制字符（如换行、制表符）必须转义
func repairUnescapedControlChars(input string) string {
	var result strings.Builder
	result.Grow(len(input) + 100) // 预分配空间

	inString := false
	escaped := false

	for i := 0; i < len(input); i++ {
		ch := input[i]

		if escaped {
			escaped = false
			result.WriteByte(ch)
			continue
		}

		if ch == '\\' && inString {
			escaped = true
			result.WriteByte(ch)
			continue
		}

		if ch == '"' {
			// 判断这个引号是否真的是字符串边界
			if inString && !isStringBoundary(input, i) {
				// 不是真正的字符串边界，转义这个引号
				result.WriteString("\\\"")
				continue
			}
			inString = !inString
			result.WriteByte(ch)
			continue
		}

		// 在字符串内部，转义控制字符
		if inString {
			switch ch {
			case '\n':
				result.WriteString("\\n")
			case '\r':
				result.WriteString("\\r")
			case '\t':
				result.WriteString("\\t")
			case '\b':
				result.WriteString("\\b")
			case '\f':
				result.WriteString("\\f")
			default:
				// 其他控制字符（0x00-0x1F）使用 \uXXXX 格式
				if ch < 0x20 {
					result.WriteString(fmt.Sprintf("\\u%04x", ch))
				} else {
					result.WriteByte(ch)
				}
			}
		} else {
			result.WriteByte(ch)
		}
	}

	return result.String()
}

// isStringBoundary 判断位置 i 处的引号是否是 JSON 字符串的真正边界
// 通过检查引号后面的字符来判断：
// - 如果后面是 JSON 结构字符（: , } ] 或空白后接这些），则是真正的边界
// - 否则可能是字符串内部未转义的引号
func isStringBoundary(s string, i int) bool {
	// 跳过引号本身
	j := i + 1

	// 跳过空白字符
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
		j++
	}

	// 检查是否到达字符串末尾
	if j >= len(s) {
		return true // 到达末尾，认为是边界
	}

	// 检查下一个非空白字符
	nextChar := s[j]
	switch nextChar {
	case ':', ',', '}', ']':
		return true // 这些是 JSON 结构字符，说明字符串结束了
	case '"':
		// 下一个是引号，可能是下一个字符串的开始（如数组中的字符串）
		// 需要检查前面是否有逗号
		// 简单起见，认为是边界
		return true
	default:
		// 其他字符，可能是字符串内部的引号
		return false
	}
}

// repairInvalidJSONValues 修复无效的 JSON 值
// 将 JavaScript 特有的值转换为有效的 JSON
func repairInvalidJSONValues(input string) string {
	// 替换 Infinity 和 -Infinity 为 null（或者可以用一个大数字）
	// 注意：需要避免替换字符串内的内容

	result := input

	// 处理 Infinity（不在字符串内）
	// 使用简单的替换策略，因为这些值通常不会出现在字符串中
	// 顺序很重要：先处理 -Infinity，再处理 Infinity
	result = replaceOutsideStrings(result, "-Infinity", "null")
	result = replaceOutsideStrings(result, "Infinity", "null")
	result = replaceOutsideStrings(result, "NaN", "null")

	// 处理 undefined
	result = replaceOutsideStrings(result, "undefined", "null")

	return result
}

// replaceOutsideStrings 替换不在 JSON 字符串内的内容
func replaceOutsideStrings(input, old, new string) string {
	if !strings.Contains(input, old) {
		return input
	}

	var result strings.Builder
	var quote byte
	escaped := false
	i := 0

	for i < len(input) {
		ch := input[i]

		if escaped {
			escaped = false
			result.WriteByte(ch)
			i++
			continue
		}

		if ch == '\\' && quote != 0 {
			escaped = true
			result.WriteByte(ch)
			i++
			continue
		}

		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			result.WriteByte(ch)
			i++
			continue
		}

		if ch == '"' || ch == '\'' {
			quote = ch
			result.WriteByte(ch)
			i++
			continue
		}

		// 如果不在字符串内，检查是否匹配要替换的内容
		if quote == 0 && i+len(old) <= len(input) && input[i:i+len(old)] == old {
			// 确保不是单词的一部分（前后不是字母数字）
			validBefore := i == 0 || !isAlphaNumeric(input[i-1])
			validAfter := i+len(old) >= len(input) || !isAlphaNumeric(input[i+len(old)])
			if validBefore && validAfter {
				result.WriteString(new)
				i += len(old)
				continue
			}
		}

		result.WriteByte(ch)
		i++
	}

	return result.String()
}

// isAlphaNumeric 检查字符是否是字母或数字
func isAlphaNumeric(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

// findMatchingBracket 查找匹配的结束括号
func findMatchingBracket(s string, start int) int {
	if start >= len(s) {
		return -1
	}

	openChar := rune(s[start])
	var closeChar rune
	switch openChar {
	case '{':
		closeChar = '}'
	case '[':
		closeChar = ']'
	default:
		return -1
	}

	depth := 0
	var quote byte
	escaped := false

	for i := start; i < len(s); i++ {
		ch := s[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && quote != 0 {
			escaped = true
			continue
		}

		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}

		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}

		if rune(ch) == openChar {
			depth++
		} else if rune(ch) == closeChar {
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// repairUnclosed 修复未闭合的 JSON
func repairUnclosed(s string) string {
	// 统计未闭合的括号
	var stack []rune

	inString := false
	escaped := false

	for _, ch := range s {
		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && inString {
			escaped = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		switch ch {
		case '{', '[':
			stack = append(stack, ch)
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			}
		}
	}

	// 添加缺失的闭合括号
	result := s
	for i := len(stack) - 1; i >= 0; i-- {
		switch stack[i] {
		case '{':
			result += "}"
		case '[':
			result += "]"
		}
	}

	return result
}

// TruncateString 截断字符串到指定长度
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}

// CleanOutput 清理输出内容
// 去除控制字符，保留可打印字符
func CleanOutput(s string) string {
	var result strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) || r == '\n' || r == '\t' {
			result.WriteRune(r)
		}
	}
	return result.String()
}
