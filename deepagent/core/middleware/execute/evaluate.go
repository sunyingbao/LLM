package execute

import "context"

// evaluate prepares and classifies an exec_command call without running it. The
// execute tool itself must not consume Decision.Action; allow / approval / deny
// is enforced by the HITL policy wrapper before the command reaches run.
func evaluate(ctx context.Context, input ExecCommandInput, cfg Config, builder CommandBuilder, policy Policy) (decision Decision, err error) {
	normalized, err := normalizeRequest(input, cfg)
	if err != nil {
		return Decision{}, err
	}
	command, err := builder.Build(ctx, normalized)
	if err != nil {
		return Decision{}, err
	}
	decision, err = policy.Decide(ctx, PolicyRequest{
		Command: command,
	})
	if err != nil {
		return Decision{}, err
	}
	return decision, nil
}
