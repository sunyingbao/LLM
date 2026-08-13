package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepairJSON(t *testing.T) {
	t.Run("valid JSON unchanged", func(t *testing.T) {
		input := `{"key": "value"}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	t.Run("valid JSON array unchanged", func(t *testing.T) {
		input := `[1, 2, 3]`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	t.Run("remove FunctionCallBegin delimiter", func(t *testing.T) {
		input := `<|FunctionCallBegin|>{"key": "value"}`
		result := RepairJSON(input)
		assert.Equal(t, `{"key": "value"}`, result)
	})

	t.Run("remove FunctionCallEnd delimiter", func(t *testing.T) {
		input := `{"key": "value"}<|FunctionCallEnd|>`
		result := RepairJSON(input)
		assert.Equal(t, `{"key": "value"}`, result)
	})

	t.Run("remove think tags", func(t *testing.T) {
		input := `<think>{"key": "value"}</think>`
		result := RepairJSON(input)
		assert.Equal(t, `{"key": "value"}`, result)
	})

	t.Run("empty input returns empty object", func(t *testing.T) {
		result := RepairJSON("")
		assert.Equal(t, "{}", result)
	})

	t.Run("whitespace only returns empty object", func(t *testing.T) {
		result := RepairJSON("   \n\t  ")
		assert.Equal(t, "{}", result)
	})

	t.Run("extract JSON from text", func(t *testing.T) {
		input := `Some text before {"key": "value"} some text after`
		result := RepairJSON(input)
		assert.Equal(t, `{"key": "value"}`, result)
	})

	t.Run("repair unclosed object", func(t *testing.T) {
		input := `{"key": "value"`
		result := RepairJSON(input)
		assert.True(t, strings.HasSuffix(result, "}"))
	})

	t.Run("repair unclosed array", func(t *testing.T) {
		input := `[1, 2, 3`
		result := RepairJSON(input)
		assert.True(t, strings.HasSuffix(result, "]"))
	})

	t.Run("repair nested unclosed", func(t *testing.T) {
		input := `{"arr": [1, 2`
		result := RepairJSON(input)
		assert.True(t, strings.HasSuffix(result, "]}"))
	})

	t.Run("handle strings with brackets", func(t *testing.T) {
		input := `{"text": "hello { world }"}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	t.Run("handle escaped quotes in strings", func(t *testing.T) {
		input := `{"text": "say \"hello\""}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	t.Run("no JSON found", func(t *testing.T) {
		input := `plain text without json`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	t.Run("repair trailing comma with jsonrepair", func(t *testing.T) {
		input := `{"a": 1,}`
		result := RepairJSON(input)
		assert.Equal(t, `{"a": 1}`, result)
		assert.True(t, json.Valid([]byte(result)))
	})

	t.Run("repair single quoted strings with jsonrepair", func(t *testing.T) {
		input := `{'a': 1, 'b': 'x'}`
		result := RepairJSON(input)
		assert.Equal(t, `{"a": 1, "b": "x"}`, result)
		assert.True(t, json.Valid([]byte(result)))
	})

	t.Run("repair single quoted strings with brace without truncating", func(t *testing.T) {
		input := `{'query': 'foo } bar', 'limit': 1}`
		result := RepairJSON(input)
		assert.Equal(t, `{"query": "foo } bar", "limit": 1}`, result)
		assert.True(t, json.Valid([]byte(result)))
	})

	t.Run("repair unquoted keys with jsonrepair", func(t *testing.T) {
		input := `{a: 1, b: "x"}`
		result := RepairJSON(input)
		assert.Equal(t, `{"a": 1, "b": "x"}`, result)
		assert.True(t, json.Valid([]byte(result)))
	})

	t.Run("repair line comments with jsonrepair", func(t *testing.T) {
		input := "{\"a\": 1, // comment\n\"b\": 2}"
		result := RepairJSON(input)
		assert.Equal(t, "{\"a\": 1, \n\"b\": 2}", result)
		assert.True(t, json.Valid([]byte(result)))
	})

	t.Run("preserve null semantics for Infinity and NaN", func(t *testing.T) {
		input := `{"a": Infinity, "b": -Infinity, "c": NaN}`
		result := RepairJSON(input)
		assert.Equal(t, `{"a": null, "b": null, "c": null}`, result)
		assert.True(t, json.Valid([]byte(result)))
	})

	// Tests for invalid JSON values (Infinity, NaN, undefined)
	t.Run("replace Infinity with null", func(t *testing.T) {
		input := `{"id": Infinity, "status": "in_progress"}`
		result := RepairJSON(input)
		assert.Equal(t, `{"id": null, "status": "in_progress"}`, result)
	})

	t.Run("replace -Infinity with null", func(t *testing.T) {
		input := `{"min": -Infinity, "max": 100}`
		result := RepairJSON(input)
		assert.Equal(t, `{"min": null, "max": 100}`, result)
	})

	t.Run("replace NaN with null", func(t *testing.T) {
		input := `{"value": NaN, "name": "test"}`
		result := RepairJSON(input)
		assert.Equal(t, `{"value": null, "name": "test"}`, result)
	})

	t.Run("replace undefined with null", func(t *testing.T) {
		input := `{"key": undefined, "other": "value"}`
		result := RepairJSON(input)
		assert.Equal(t, `{"key": null, "other": "value"}`, result)
	})

	t.Run("preserve Infinity inside string", func(t *testing.T) {
		input := `{"text": "Infinity is a concept", "id": 1}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	t.Run("preserve invalid value tokens inside single quoted strings", func(t *testing.T) {
		input := `{'text': 'undefined', 'query': 'NaN', 'limit': Infinity}`
		result := RepairJSON(input)
		assert.Equal(t, `{"text": "undefined", "query": "NaN", "limit": null}`, result)
		assert.True(t, json.Valid([]byte(result)))
	})

	t.Run("replace multiple invalid values", func(t *testing.T) {
		input := `{"a": Infinity, "b": -Infinity, "c": NaN, "d": undefined}`
		result := RepairJSON(input)
		assert.Equal(t, `{"a": null, "b": null, "c": null, "d": null}`, result)
	})

	t.Run("handle InfinityWar as valid word", func(t *testing.T) {
		// Should not replace Infinity when it's part of a larger word
		input := `{"movie": "InfinityWar", "count": 5}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	// Tests for unescaped quotes in content
	t.Run("escape unescaped quotes in content", func(t *testing.T) {
		// LLM often outputs content with unescaped quotes like: "content": "text with "quotes" inside"
		input := `{"path": "/test.txt", "content": "text with "quotes" inside"}`
		result := RepairJSON(input)
		// The inner quotes should be escaped
		assert.Contains(t, result, `\"quotes\"`)
		assert.True(t, strings.HasPrefix(result, "{"))
		assert.True(t, strings.HasSuffix(result, "}"))
	})

	t.Run("preserve properly escaped quotes", func(t *testing.T) {
		input := `{"content": "text with \"properly escaped\" quotes"}`
		result := RepairJSON(input)
		assert.Equal(t, input, result)
	})

	t.Run("escape multiple unescaped quotes", func(t *testing.T) {
		// More realistic case: content with quotes that aren't at the very end
		input := `{"content": "He said "hello" and "world" today"}`
		result := RepairJSON(input)
		assert.Contains(t, result, `\"hello\"`)
		assert.Contains(t, result, `\"world\"`)
	})
}

func TestRepairJSONWithError(t *testing.T) {
	t.Run("returns repaired json", func(t *testing.T) {
		got, err := RepairJSONWithError(`{"a": 1,}`)
		assert.NoError(t, err)
		assert.Equal(t, `{"a": 1}`, got)
	})

	t.Run("returns error when no json object or array exists", func(t *testing.T) {
		got, err := RepairJSONWithError(`plain text without json`)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidJSONInput))
		assert.Equal(t, `plain text without json`, got)
	})

	t.Run("returns error instead of truncating malformed string content", func(t *testing.T) {
		input := `{"text":"say "hello } world"", "limit":1}`
		got, err := RepairJSONWithError(input)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidJSONInput))
		assert.Equal(t, input, got)
	})

	t.Run("returns valid json prefix before explanatory text", func(t *testing.T) {
		got, err := RepairJSONWithError(`{"a":1} Explanation: "ok"`)
		assert.NoError(t, err)
		assert.Equal(t, `{"a":1}`, got)
	})

	t.Run("repairs malformed json prefix before quoted explanatory text", func(t *testing.T) {
		got, err := RepairJSONWithError(`{a:1} Explanation: "ok"`)
		assert.NoError(t, err)
		assert.Equal(t, `{"a":1}`, got)
	})

	t.Run("repairs trailing comma prefix before quoted explanatory text", func(t *testing.T) {
		got, err := RepairJSONWithError(`{"a":1,} Explanation: "ok"`)
		assert.NoError(t, err)
		assert.Equal(t, `{"a":1}`, got)
	})

	t.Run("does not repair object with comma prose into array", func(t *testing.T) {
		got, err := RepairJSONWithError(`{"a":1}, Explanation: ok`)
		assert.NoError(t, err)
		assert.Equal(t, `{"a":1}`, got)
	})

	t.Run("returns error instead of dropping adjacent json continuation", func(t *testing.T) {
		input := `{"a":1}{"b":2}`
		got, err := RepairJSONWithError(input)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidJSONInput))
		assert.Equal(t, input, got)
	})

	t.Run("returns error instead of dropping adjacent json continuation after repair", func(t *testing.T) {
		input := `{"a":1,}{"b":2}`
		got, err := RepairJSONWithError(input)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidJSONInput))
		assert.Equal(t, input, got)
	})

	t.Run("returns error instead of repairing comma json continuation into array", func(t *testing.T) {
		input := `{"a":1}, "b"`
		got, err := RepairJSONWithError(input)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidJSONInput))
		assert.Equal(t, input, got)
	})
}

func TestFindMatchingBracket(t *testing.T) {
	t.Run("simple object", func(t *testing.T) {
		s := `{"key": "value"}`
		result := findMatchingBracket(s, 0)
		assert.Equal(t, len(s)-1, result)
	})

	t.Run("simple array", func(t *testing.T) {
		s := `[1, 2, 3]`
		result := findMatchingBracket(s, 0)
		assert.Equal(t, len(s)-1, result)
	})

	t.Run("nested object", func(t *testing.T) {
		s := `{"outer": {"inner": "value"}}`
		result := findMatchingBracket(s, 0)
		assert.Equal(t, len(s)-1, result)
	})

	t.Run("unclosed bracket", func(t *testing.T) {
		s := `{"key": "value"`
		result := findMatchingBracket(s, 0)
		assert.Equal(t, -1, result)
	})

	t.Run("invalid start position", func(t *testing.T) {
		s := `{"key": "value"}`
		result := findMatchingBracket(s, 100)
		assert.Equal(t, -1, result)
	})

	t.Run("not a bracket at start", func(t *testing.T) {
		s := `"key": "value"`
		result := findMatchingBracket(s, 0)
		assert.Equal(t, -1, result)
	})

	t.Run("brackets inside string", func(t *testing.T) {
		s := `{"text": "{}"}`
		result := findMatchingBracket(s, 0)
		assert.Equal(t, len(s)-1, result)
	})

	t.Run("brackets inside single quoted string", func(t *testing.T) {
		s := `{'text': '} still text', 'limit': 1}`
		result := findMatchingBracket(s, 0)
		assert.Equal(t, len(s)-1, result)
	})
}

func TestRepairUnclosed(t *testing.T) {
	t.Run("add missing closing brace", func(t *testing.T) {
		s := `{"key": "value"`
		result := repairUnclosed(s)
		assert.Equal(t, `{"key": "value"}`, result)
	})

	t.Run("add missing closing bracket", func(t *testing.T) {
		s := `[1, 2, 3`
		result := repairUnclosed(s)
		assert.Equal(t, `[1, 2, 3]`, result)
	})

	t.Run("add multiple missing closings", func(t *testing.T) {
		s := `{"arr": [1, 2`
		result := repairUnclosed(s)
		assert.Equal(t, `{"arr": [1, 2]}`, result)
	})

	t.Run("deeply nested", func(t *testing.T) {
		s := `{"a": {"b": {"c": 1`
		result := repairUnclosed(s)
		assert.Equal(t, `{"a": {"b": {"c": 1}}}`, result)
	})

	t.Run("already complete", func(t *testing.T) {
		s := `{"key": "value"}`
		result := repairUnclosed(s)
		assert.Equal(t, s, result)
	})
}

func TestTruncateString(t *testing.T) {
	t.Run("short string unchanged", func(t *testing.T) {
		s := "hello"
		result := TruncateString(s, 10)
		assert.Equal(t, s, result)
	})

	t.Run("exact length unchanged", func(t *testing.T) {
		s := "hello"
		result := TruncateString(s, 5)
		assert.Equal(t, s, result)
	})

	t.Run("long string truncated", func(t *testing.T) {
		s := "hello world this is a long string"
		result := TruncateString(s, 10)
		assert.Equal(t, "hello worl...[truncated]", result)
	})

	t.Run("empty string", func(t *testing.T) {
		result := TruncateString("", 10)
		assert.Equal(t, "", result)
	})

	t.Run("zero max length", func(t *testing.T) {
		result := TruncateString("hello", 0)
		assert.Equal(t, "...[truncated]", result)
	})
}

func TestCleanOutput(t *testing.T) {
	t.Run("printable characters unchanged", func(t *testing.T) {
		s := "Hello, World!"
		result := CleanOutput(s)
		assert.Equal(t, s, result)
	})

	t.Run("preserves newlines", func(t *testing.T) {
		s := "line1\nline2\nline3"
		result := CleanOutput(s)
		assert.Equal(t, s, result)
	})

	t.Run("preserves tabs", func(t *testing.T) {
		s := "col1\tcol2\tcol3"
		result := CleanOutput(s)
		assert.Equal(t, s, result)
	})

	t.Run("removes control characters", func(t *testing.T) {
		s := "hello\x00\x01\x02world"
		result := CleanOutput(s)
		assert.Equal(t, "helloworld", result)
	})

	t.Run("removes bell character", func(t *testing.T) {
		s := "alert\x07sound"
		result := CleanOutput(s)
		assert.Equal(t, "alertsound", result)
	})

	t.Run("empty string", func(t *testing.T) {
		result := CleanOutput("")
		assert.Equal(t, "", result)
	})

	t.Run("unicode characters", func(t *testing.T) {
		s := "你好世界 🌍"
		result := CleanOutput(s)
		assert.Equal(t, s, result)
	})
}
