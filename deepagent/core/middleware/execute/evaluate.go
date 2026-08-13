package execute

import "context"

// evaluation is the pre-execution result used by ExecuteMiddleware.PolicyGate.
//
// It intentionally stops before CommandExecutor.ExecuteCommand. The goal is to
// make approval-time and execution-time decisions use the exact same pipeline:
//
//	ExecCommandInput -> NormalizedRequest -> CommandSpec -> Decision
//
// NormalizedRequest contains defaults and validation results, CommandSpec is
// the concrete command shape a runner would execute, and Decision is the policy
// answer for that command.
type evaluation struct {
	input      ExecCommandInput
	normalized NormalizedRequest
	command    CommandSpec
	decision   Decision
}

// evaluate prepares and classifies an exec_command call without running it. The
// execute tool itself must not consume Decision.Action; allow / approval / deny
// is enforced by the HITL policy wrapper before the command reaches run.
func evaluate(ctx context.Context, input ExecCommandInput, cfg Config, builder CommandBuilder, policy Policy) (evaluation, error) {
	normalized, err := normalizeRequest(input, cfg)
	if err != nil {
		return evaluation{}, err
	}
	command, err := builder.Build(ctx, normalized)
	if err != nil {
		return evaluation{}, err
	}
	decision, err := policy.Decide(ctx, PolicyRequest{
		Command: command,
	})
	if err != nil {
		return evaluation{}, err
	}
	return evaluation{
		input:      input,
		normalized: normalized,
		command:    command,
		decision:   decision,
	}, nil
}
