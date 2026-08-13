package execute

import (
	"context"

	deeptools "eino-cli/deepagent/core/tools"
	sdkutils "eino-cli/deepagent/core/utils"
)

func (m *ExecuteMiddleware) PolicyGate() deeptools.ToolPolicyGate {
	return deeptools.ToolPolicyGate{
		Policy: func(ctx context.Context, info *deeptools.ApprovalInfo) (deeptools.ToolCallDecision, error) {
			if m == nil || info == nil {
				return deeptools.ToolCallDecision{Action: deeptools.ToolCallDeny, Reason: "approval info is required"}, nil
			}
			if info.ToolName != m.cfg.toolName() {
				return deeptools.ToolCallDecision{Action: deeptools.ToolCallAllow}, nil
			}
			input, err := sdkutils.ToStruct[ExecCommandInput](info.ArgumentsInJSON)
			if err != nil {
				return deeptools.ToolCallDecision{}, err
			}
			ev, err := evaluate(ctx, input, m.cfg, m.builder, m.policyProfile.Policy)
			if err != nil {
				return deeptools.ToolCallDecision{}, err
			}
			switch ev.decision.Action {
			case ActionAllow:
				return deeptools.ToolCallDecision{Action: deeptools.ToolCallAllow, Reason: ev.decision.Reason}, nil
			case ActionRequireApproval:
				return deeptools.ToolCallDecision{Action: deeptools.ToolCallRequireApproval, Reason: ev.decision.Reason}, nil
			case ActionDeny:
				return deeptools.ToolCallDecision{Action: deeptools.ToolCallDeny, Reason: ev.decision.Reason}, nil
			default:
				return deeptools.ToolCallDecision{Action: deeptools.ToolCallDeny, Reason: "unknown execute policy action"}, nil
			}
		},
		DenyFormatter: func(ctx context.Context, info *deeptools.ApprovalInfo, decision deeptools.ToolCallDecision) (string, error) {
			if m == nil {
				return sdkutils.ToString(ExecCommandOutput{Denied: true, Reason: decision.Reason, ExitCode: 1}), nil
			}
			if decision.Reason == "" {
				decision.Reason = "command denied by policy"
			}
			return m.formatDenied(nil, "", decision.Reason), nil
		},
	}
}
