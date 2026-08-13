package execute

import (
	"context"
	"strings"
)

type Policy interface {
	Decide(ctx context.Context, req PolicyRequest) (Decision, error)
}

type PolicyProfile struct {
	// Policy is the hard execution gate for a concrete command.
	Policy Policy

	// Instructions are model-facing guidance that matches Policy's behavior.
	// They are appended to ExecuteMiddleware's system prompt.
	Instructions string
}

type classifierPolicy struct {
	classifier CommandClassifier
	mode       string
}

func NewReadOnlyPolicy(classifier CommandClassifier) Policy {
	return &classifierPolicy{classifier: ensureClassifier(classifier), mode: "readonly"}
}

func NewDefaultPolicy(classifier CommandClassifier) Policy {
	return &classifierPolicy{classifier: ensureClassifier(classifier), mode: "default"}
}

func NewPermissivePolicy(classifier CommandClassifier) Policy {
	return &classifierPolicy{classifier: ensureClassifier(classifier), mode: "permissive"}
}

func NewReadOnlyPolicyProfile(classifier CommandClassifier) PolicyProfile {
	return PolicyProfile{
		Policy: NewReadOnlyPolicy(classifier),
		Instructions: strings.TrimSpace(`
This agent has read-only shell access.
- Use commands for inspection only, such as rg, grep, sed -n, git diff, git status, ls, find without -delete or -exec, cat, head, tail, wc, pwd, and echo.
- Do not run commands that write files, change git state, install packages, start services, run builds or tests, or execute scripts.
- If a needed command is denied, explain the limitation instead of retrying variants.
`),
	}
}

func NewDefaultPolicyProfile(classifier CommandClassifier) PolicyProfile {
	return PolicyProfile{
		Policy: NewDefaultPolicy(classifier),
		Instructions: strings.TrimSpace(`
This agent has default shell access.
- Use known read-only inspection commands directly.
- For commands that may write files, change state, install dependencies, run scripts, start services, or otherwise have side effects, provide a clear justification and expect approval.
- If a command is denied, do not retry small variants of the same command; explain the limitation or choose a safer command.
`),
	}
}

func NewPermissivePolicyProfile(classifier CommandClassifier) PolicyProfile {
	return PolicyProfile{
		Policy: NewPermissivePolicy(classifier),
		Instructions: strings.TrimSpace(`
This agent has permissive shell access.
- Use shell commands when they are the clearest way to complete the task.
- Avoid destructive commands unless explicitly requested by the user.
- If a command is denied, do not retry small variants of the same command; explain the limitation or choose a safer command.
`),
	}
}

func (p *classifierPolicy) Decide(ctx context.Context, req PolicyRequest) (Decision, error) {
	classification, err := p.classifier.Classify(ctx, req.Command)
	if err != nil {
		return Decision{}, err
	}
	keys := approvalKeysForClassification(req.Command, classification)
	decision := Decision{
		Class:        classification.Class,
		Reason:       classification.Reason,
		ApprovalKeys: keys,
	}
	switch p.mode {
	case "readonly":
		if classification.Class == CommandSafe {
			decision.Action = ActionAllow
		} else {
			decision.Action = ActionDeny
		}
	case "permissive":
		if classification.Class == CommandForbidden {
			decision.Action = ActionDeny
		} else {
			decision.Action = ActionAllow
		}
	default:
		if classification.Class == CommandSafe {
			decision.Action = ActionAllow
		} else if classification.Class == CommandForbidden {
			decision.Action = ActionDeny
		} else {
			decision.Action = ActionRequireApproval
		}
	}
	return decision, nil
}

func ensurePolicyProfile(profile PolicyProfile) PolicyProfile {
	if profile.Policy != nil {
		return profile
	}
	return NewDefaultPolicyProfile(nil)
}

func ensureClassifier(classifier CommandClassifier) CommandClassifier {
	if classifier != nil {
		return classifier
	}
	return NewDefaultClassifier()
}

func approvalKeysForClassification(command CommandSpec, classification CommandClassification) []ApprovalKey {
	keys := []ApprovalKey{{Kind: "command_exact", Value: command.Display}}
	if classification.FirstProgram != "" {
		keys = append(keys, ApprovalKey{Kind: "program", Value: classification.FirstProgram})
	}
	return keys
}
