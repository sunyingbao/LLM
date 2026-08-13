package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type ApprovalInfo struct {
	ToolName        string
	ArgumentsInJSON string
}

type ApprovalResult struct {
	Approved         bool
	DisapproveReason *string
}

func (ai *ApprovalInfo) String() string {
	return fmt.Sprintf("tool '%s' interrupted with arguments '%s', waiting for your approval, "+
		"please answer with Y/N",
		ai.ToolName, ai.ArgumentsInJSON)
}

func init() {
	schema.Register[*ApprovalInfo]()
}

type NeedApproval func(ctx context.Context, info *ApprovalInfo) bool

type ToolCallAction string

const (
	ToolCallAllow           ToolCallAction = "allow"
	ToolCallRequireApproval ToolCallAction = "require_approval"
	ToolCallDeny            ToolCallAction = "deny"
)

type ToolCallDecision struct {
	// Action is the policy result for this tool call. Empty is treated as allow.
	Action ToolCallAction

	// Reason is human/model-facing context for approval and deny paths.
	Reason string
}

// ToolCallPolicy decides whether one concrete tool call should run, request
// human approval, or be denied before the wrapped tool is invoked.
type ToolCallPolicy func(ctx context.Context, info *ApprovalInfo) (ToolCallDecision, error)

// ToolDenyFormatter formats a denied tool call into that tool's native output
// schema. The policy wrapper does not guess a generic schema for all tools.
type ToolDenyFormatter func(ctx context.Context, info *ApprovalInfo, decision ToolCallDecision) (string, error)

type ToolPolicyGate struct {
	// Policy is required. It makes the allow / require_approval / deny decision
	// for a concrete tool call.
	Policy ToolCallPolicy

	// DenyFormatter is required for deny decisions. It returns the model-facing
	// tool output without invoking the wrapped tool.
	DenyFormatter ToolDenyFormatter
}

type InvokableApprovableTool struct {
	tool.InvokableTool
	gate ToolPolicyGate
}

func NewInvokableApprovableTool(tool tool.InvokableTool, needApproval NeedApproval) InvokableApprovableTool {
	return NewInvokablePolicyTool(tool, ToolPolicyGate{
		Policy: func(ctx context.Context, info *ApprovalInfo) (ToolCallDecision, error) {
			if needApproval != nil && needApproval(ctx, info) {
				return ToolCallDecision{Action: ToolCallRequireApproval}, nil
			}
			return ToolCallDecision{Action: ToolCallAllow}, nil
		},
	})
}

func NewInvokablePolicyTool(tool tool.InvokableTool, gate ToolPolicyGate) InvokableApprovableTool {
	return InvokableApprovableTool{InvokableTool: tool, gate: gate}
}

func (i InvokableApprovableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return i.InvokableTool.Info(ctx)
}

func (i InvokableApprovableTool) InvokableRun(ctx context.Context, argumentsInJSON string,
	opts ...tool.Option) (string, error) {

	toolInfo, err := i.Info(ctx)
	if err != nil {
		return "", err
	}

	wasInterrupted, _, storedArguments := tool.GetInterruptState[string](ctx)
	if !wasInterrupted {
		// The policy is evaluated only before the first interrupt. On resume,
		// GetInterruptState lets this wrapper continue from the stored call
		// arguments without re-running policy logic that may depend on time or
		// external state.
		info := &ApprovalInfo{
			ToolName:        toolInfo.Name,
			ArgumentsInJSON: argumentsInJSON,
		}
		decision, err := i.decide(ctx, info)
		if err != nil {
			return "", err
		}
		switch decision.Action {
		case ToolCallAllow:
			return i.InvokableTool.InvokableRun(ctx, argumentsInJSON, opts...)
		case ToolCallRequireApproval:
			return "", tool.StatefulInterrupt(ctx, &ApprovalInfo{
				ToolName:        toolInfo.Name,
				ArgumentsInJSON: argumentsInJSON,
			}, argumentsInJSON)
		case ToolCallDeny:
			return i.formatDenied(ctx, info, decision)
		default:
			action := decision.Action
			decision.Action = ToolCallDeny
			decision.Reason = fmt.Sprintf("unknown tool policy action: %s", action)
			return i.formatDenied(ctx, info, decision)
		}

	}

	isResumeTarget, hasData, data := tool.GetResumeContext[*ApprovalResult](ctx)
	if isResumeTarget && hasData {
		if data.Approved {
			return i.InvokableTool.InvokableRun(ctx, storedArguments, opts...)
		}

		return formatHumanRejectedToolResult(toolInfo.Name, data.DisapproveReason), nil
	}

	isResumeTarget, _, _ = tool.GetResumeContext[any](ctx)
	if !isResumeTarget {
		return "", tool.StatefulInterrupt(ctx, &ApprovalInfo{
			ToolName:        toolInfo.Name,
			ArgumentsInJSON: storedArguments,
		}, storedArguments)
	}

	return i.InvokableTool.InvokableRun(ctx, storedArguments, opts...)
}

func (i InvokableApprovableTool) decide(ctx context.Context, info *ApprovalInfo) (ToolCallDecision, error) {
	if i.gate.Policy == nil {
		return ToolCallDecision{Action: ToolCallAllow}, nil
	}
	decision, err := i.gate.Policy(ctx, info)
	if err != nil {
		return ToolCallDecision{Action: ToolCallDeny, Reason: err.Error()}, nil
	}
	if decision.Action == "" {
		decision.Action = ToolCallAllow
	}
	return decision, nil
}

func formatHumanRejectedToolResult(toolName string, reason *string) string {
	msg := fmt.Sprintf("Human rejected tool %q.", toolName)
	if reason != nil && strings.TrimSpace(*reason) != "" {
		msg += fmt.Sprintf(" Human feedback: %s. Treat this feedback as the latest user instruction for the current turn.", strings.TrimSpace(*reason))
	}
	msg += " Do not retry this tool call or route around the rejection unless the human explicitly asks."
	return msg
}

func (i InvokableApprovableTool) formatDenied(ctx context.Context, info *ApprovalInfo, decision ToolCallDecision) (string, error) {
	if i.gate.DenyFormatter == nil {
		return "", fmt.Errorf("tool policy denied %q but no deny formatter is configured", info.ToolName)
	}
	return i.gate.DenyFormatter(ctx, info, decision)
}
