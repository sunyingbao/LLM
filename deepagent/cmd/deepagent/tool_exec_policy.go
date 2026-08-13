package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"

	"eino-cli/deepagent/core/constant"
	execmw "eino-cli/deepagent/core/middleware/execute"
	"eino-cli/deepagent/core/tools"
)

const (
	approvalKeyKindExact   = "exact"
	approvalKeyKindPath    = "path"
	approvalKeyKindPathDir = "path_dir"
)

type approvalKey struct {
	Kind            string
	ToolName        string
	ArgumentsInJSON string
}

// ToolExecPolicy manages session-scoped tool approval reuse.
type ToolExecPolicy struct {
	needApproveTools map[string]struct{}

	mu      sync.RWMutex
	allowed map[string]map[approvalKey]struct{}
}

func NewToolExecPolicy() *ToolExecPolicy {
	return &ToolExecPolicy{
		needApproveTools: map[string]struct{}{
			constant.ToolWriteFile: {},
			constant.ToolEditFile:  {},
		},
		allowed: make(map[string]map[approvalKey]struct{}),
	}
}

func (p *ToolExecPolicy) BuildNeedApprovalMap(sessionID string) map[string]tools.NeedApproval {
	result := make(map[string]tools.NeedApproval)
	scope := approvalScope(sessionID)
	for toolName := range p.needApproveTools {
		name := toolName
		result[name] = func(ctx context.Context, info *tools.ApprovalInfo) bool {
			if info == nil {
				return true
			}
			if _, ok := p.needApproveTools[info.ToolName]; !ok {
				return false
			}
			return !p.IsAllowed(scope, info.ToolName, info.ArgumentsInJSON)
		}
	}
	return result
}

func (p *ToolExecPolicy) AllowSession(sessionID string, toolName string, argumentsInJSON string) {
	scope := approvalScope(sessionID)
	if p == nil || scope == "" || toolName == "" {
		return
	}
	keys := approvalKeys(toolName, argumentsInJSON)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.allowed[scope] == nil {
		p.allowed[scope] = make(map[approvalKey]struct{})
	}
	for _, key := range keys {
		p.allowed[scope][key] = struct{}{}
	}
}

func (p *ToolExecPolicy) WrapPolicyGate(sessionID string, gate tools.ToolPolicyGate) tools.ToolPolicyGate {
	basePolicy := gate.Policy
	scope := approvalScope(sessionID)
	gate.Policy = func(ctx context.Context, info *tools.ApprovalInfo) (tools.ToolCallDecision, error) {
		if basePolicy == nil {
			return tools.ToolCallDecision{Action: tools.ToolCallAllow}, nil
		}
		decision, err := basePolicy(ctx, info)
		if err != nil {
			return decision, err
		}
		if decision.Action == tools.ToolCallRequireApproval && p != nil && info != nil && p.IsAllowed(scope, info.ToolName, info.ArgumentsInJSON) {
			decision.Action = tools.ToolCallAllow
		}
		return decision, nil
	}
	return gate
}

func (p *ToolExecPolicy) IsAllowed(scope string, toolName string, argumentsInJSON string) bool {
	if p == nil || scope == "" || toolName == "" {
		return false
	}
	keys := approvalKeys(toolName, argumentsInJSON)
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, key := range keys {
		if _, ok := p.allowed[scope][key]; ok {
			return true
		}
	}
	return false
}

func approvalScope(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return "session:" + sessionID
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
	return keys
}

func approvalFilePath(argumentsInJSON string) string {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &payload); err != nil {
		return ""
	}
	return normalizeApprovalPath(payload.Path)
}

func approvalFileDir(filePath string) string {
	filePath = normalizeApprovalPath(filePath)
	if filePath == "" {
		return ""
	}
	dir := path.Dir(filePath)
	if dir == "." || dir == "/" || dir == "" {
		return ""
	}
	return strings.TrimSuffix(dir, "/") + "/"
}

func normalizeApprovalPath(filePath string) string {
	filePath = strings.TrimSpace(strings.ReplaceAll(filePath, "\\", "/"))
	if filePath == "" {
		return ""
	}
	clean := path.Clean(filePath)
	if clean == "." || clean == "" {
		return ""
	}
	return strings.TrimPrefix(clean, "./")
}

func normalizeApprovalArguments(argumentsInJSON string) string {
	var v any
	decoder := json.NewDecoder(strings.NewReader(argumentsInJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&v); err != nil {
		return strings.TrimSpace(argumentsInJSON)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(argumentsInJSON)
	}
	return string(bytes.TrimSpace(data))
}

func approvalSessionScopeLabel(pending *pendingApproval) string {
	if pending == nil {
		return "similar calls"
	}
	switch pending.ToolName {
	case constant.ToolWriteFile, constant.ToolEditFile:
		if dir := approvalFileDir(approvalFilePath(pending.ArgumentsInJSON)); dir != "" {
			return fmt.Sprintf("%s under %s", pending.ToolName, dir)
		}
		if filePath := approvalFilePath(pending.ArgumentsInJSON); filePath != "" {
			return fmt.Sprintf("%s on %s", pending.ToolName, filePath)
		}
	case execmw.DefaultToolName:
		return "this exact exec_command"
	}
	return "similar calls"
}
