package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"unicode"

	"eino-cli/deepagent/core/constant"
	execmw "eino-cli/deepagent/core/middleware/execute"
)

const (
	approvalKeyKindExact   = "exact"
	approvalKeyKindCommand = "command"
	approvalKeyKindPath    = "path"
	approvalKeyKindPathDir = "path_dir"
)

type SessionApprovalStore struct {
	mu      sync.RWMutex
	allowed map[string]map[approvalKey]struct{}
}

type approvalKey struct {
	Kind            string
	ToolName        string
	ArgumentsInJSON string
}

func NewSessionApprovalStore() *SessionApprovalStore {
	return &SessionApprovalStore{
		allowed: make(map[string]map[approvalKey]struct{}),
	}
}

func (s *SessionApprovalStore) Allow(scope string, toolName string, argumentsInJSON string) {
	if s == nil || scope == "" || toolName == "" {
		return
	}
	keys := approvalKeys(toolName, argumentsInJSON)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allowed[scope] == nil {
		s.allowed[scope] = make(map[approvalKey]struct{})
	}
	for _, key := range keys {
		s.allowed[scope][key] = struct{}{}
	}
}

func (s *SessionApprovalStore) IsAllowed(scope string, toolName string, argumentsInJSON string) bool {
	if s == nil || scope == "" || toolName == "" {
		return false
	}
	keys := approvalKeys(toolName, argumentsInJSON)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range keys {
		if _, ok := s.allowed[scope][key]; ok {
			return true
		}
	}
	return false
}

func KeyForLog(toolName string, argumentsInJSON string) string {
	return fmt.Sprintf("%s:%s", toolName, normalizeApprovalArguments(argumentsInJSON))
}

func approvalKeys(toolName string, argumentsInJSON string) []approvalKey {
	keys := []approvalKey{{
		Kind:            approvalKeyKindExact,
		ToolName:        toolName,
		ArgumentsInJSON: normalizeApprovalArguments(argumentsInJSON),
	}}
	switch toolName {
	case constant.ToolWriteFile, constant.ToolEditFile:
		if filePath := approvalFilePath(argumentsInJSON); filePath != "" {
			keys = append(keys, approvalKey{
				Kind:            approvalKeyKindPath,
				ToolName:        toolName,
				ArgumentsInJSON: filePath,
			})
			if dir := approvalFileDir(filePath); dir != "" {
				keys = append(keys, approvalKey{
					Kind:            approvalKeyKindPathDir,
					ToolName:        toolName,
					ArgumentsInJSON: dir,
				})
			}
		}
	}
	if toolName == constant.ToolExecute || toolName == execmw.DefaultToolName {
		if commandName := approvalExecuteCommandName(argumentsInJSON); commandName != "" {
			keys = append(keys, approvalKey{
				Kind:            approvalKeyKindCommand,
				ToolName:        toolName,
				ArgumentsInJSON: commandName,
			})
		}
	}
	return keys
}

func approvalFilePath(argumentsInJSON string) string {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Path)
}

func approvalFileDir(filePath string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" || path.IsAbs(filePath) {
		return ""
	}
	clean := path.Clean(strings.ReplaceAll(filePath, "\\", "/"))
	clean = strings.TrimPrefix(clean, "./")
	if clean == "." || clean == "" {
		return ""
	}
	dir := path.Dir(clean)
	if dir == "." || dir == "/" || dir == "" {
		return ""
	}
	return strings.TrimSuffix(dir, "/") + "/"
}

func approvalExecuteCommandName(argumentsInJSON string) string {
	var payload struct {
		Command string `json:"command"`
		Cmd     string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &payload); err != nil {
		return ""
	}
	command := payload.Command
	if command == "" {
		command = payload.Cmd
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func IsReadOnlyExecute(argumentsInJSON string) bool {
	command := approvalExecuteCommand(argumentsInJSON)
	return isReadOnlyShellCommand(command)
}

func approvalExecuteCommand(argumentsInJSON string) string {
	var payload struct {
		Command string `json:"command"`
		Cmd     string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &payload); err != nil {
		return ""
	}
	if payload.Command != "" {
		return strings.TrimSpace(payload.Command)
	}
	return strings.TrimSpace(payload.Cmd)
}

func isReadOnlyShellCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if strings.ContainsAny(command, "\r\n;`<>&") || strings.Contains(command, "$(") {
		return false
	}
	for _, stage := range strings.Split(command, "|") {
		if !isReadOnlyCommandStage(stage) {
			return false
		}
	}
	return true
}

func isReadOnlyCommandStage(stage string) bool {
	fields := stripReadOnlyCommandPrefixes(strings.Fields(stage))
	if len(fields) == 0 {
		return false
	}
	name := baseCommandName(fields[0])
	if !readOnlyCommandNames[name] {
		return false
	}
	return readOnlyCommandArgsSafe(name, fields[1:])
}

var readOnlyCommandNames = map[string]bool{
	"cat":        true,
	"df":         true,
	"du":         true,
	"file":       true,
	"grep":       true,
	"head":       true,
	"id":         true,
	"ls":         true,
	"nl":         true,
	"ps":         true,
	"pwd":        true,
	"rg":         true,
	"stat":       true,
	"tail":       true,
	"tree":       true,
	"uname":      true,
	"wc":         true,
	"whoami":     true,
	"nvidia-smi": true,
}

func readOnlyCommandArgsSafe(name string, args []string) bool {
	switch name {
	case "tail":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-f") || strings.HasPrefix(arg, "--follow") {
				return false
			}
		}
	case "du":
		for _, arg := range args {
			if arg == "--files0-from=-" || strings.HasPrefix(arg, "--files0-from=-") {
				return false
			}
		}
	}
	return true
}

func stripReadOnlyCommandPrefixes(fields []string) []string {
	for len(fields) > 0 && isShellEnvAssignment(fields[0]) {
		fields = fields[1:]
	}
	for len(fields) > 0 {
		switch baseCommandName(fields[0]) {
		case "command":
			fields = fields[1:]
		case "timeout", "gtimeout":
			fields = stripTimeoutPrefix(fields[1:])
		default:
			return fields
		}
		for len(fields) > 0 && isShellEnvAssignment(fields[0]) {
			fields = fields[1:]
		}
	}
	return fields
}

func stripTimeoutPrefix(fields []string) []string {
	for len(fields) > 0 {
		arg := fields[0]
		if arg == "--" {
			fields = fields[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") {
			fields = fields[1:]
			break
		}
		fields = fields[1:]
		if arg == "-s" || arg == "--signal" || strings.HasPrefix(arg, "--signal=") ||
			arg == "-k" || arg == "--kill-after" || strings.HasPrefix(arg, "--kill-after=") {
			if !strings.Contains(arg, "=") && len(fields) > 0 {
				fields = fields[1:]
			}
		}
	}
	return fields
}

func isShellEnvAssignment(field string) bool {
	idx := strings.IndexByte(field, '=')
	if idx <= 0 {
		return false
	}
	for i, r := range field[:idx] {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func baseCommandName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	command = strings.TrimSuffix(command, ".exe")
	if idx := strings.LastIndex(command, "/"); idx >= 0 {
		command = command[idx+1:]
	}
	return command
}

func normalizeApprovalArguments(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(raw)); err == nil {
		return buf.String()
	}
	return raw
}
