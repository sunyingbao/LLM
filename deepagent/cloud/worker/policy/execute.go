//go:build !windows

package policy

import (
	"context"
	"path/filepath"
	"strings"

	execmw "eino-cli/deepagent/core/middleware/execute"
)

type ApprovalPolicy string

const (
	ApprovalPolicyNormal     ApprovalPolicy = "normal"
	ApprovalPolicyReadOnly   ApprovalPolicy = "readonly"
	ApprovalPolicyPermissive ApprovalPolicy = "permissive"
)

const defaultCommandInstructions = `
Repository search:
- rg is available in this environment.
- Prefer rg for searching repository text and rg --files for finding files.
- Use filesystem tools such as grep and glob when structured tool output is clearer than shell output.

Task discipline:
- Do the requested deliverable directly. Do not create extra variants, README files, or broad demos unless the user asks for them.
- Use one focused validation pass for straightforward implementation work. Do not keep running similar checks after the result is already clear.
- On the main thread, prefer spawning a worker task for bounded code implementation so the main thread stays focused on orchestration and final summary.
`

func ExecutePolicyProfile(approvalPolicy ApprovalPolicy) execmw.PolicyProfile {
	var profile execmw.PolicyProfile
	classifier := commandClassifier{base: execmw.NewDefaultClassifier()}
	switch approvalPolicy {
	case ApprovalPolicyReadOnly:
		profile = execmw.NewReadOnlyPolicyProfile(classifier)
	case ApprovalPolicyPermissive:
		profile = execmw.NewPermissivePolicyProfile(classifier)
	default:
		profile = execmw.NewDefaultPolicyProfile(classifier)
	}
	profile.Instructions = strings.TrimSpace(profile.Instructions + "\n\n" + defaultCommandInstructions)
	return profile
}

type commandClassifier struct {
	base execmw.CommandClassifier
}

func (c commandClassifier) Classify(ctx context.Context, spec execmw.CommandSpec) (execmw.CommandClassification, error) {
	if classification, ok := classifyLookupCommand(spec.RawCommand); ok {
		return classification, nil
	}
	if c.base == nil {
		return execmw.NewDefaultClassifier().Classify(ctx, spec)
	}
	return c.base.Classify(ctx, spec)
}

func classifyLookupCommand(command string) (execmw.CommandClassification, bool) {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\r\n;|&<>`$") {
		return execmw.CommandClassification{}, false
	}
	fields := strings.Fields(command)
	if len(fields) == 2 && filepath.Base(fields[0]) == "which" && safeLookupTarget(fields[1]) {
		return lookupClassification(fields), true
	}
	if len(fields) == 3 && filepath.Base(fields[0]) == "command" && fields[1] == "-v" && safeLookupTarget(fields[2]) {
		return lookupClassification(fields), true
	}
	return execmw.CommandClassification{}, false
}

func lookupClassification(fields []string) execmw.CommandClassification {
	return execmw.CommandClassification{
		Class:          execmw.CommandSafe,
		Reason:         "tool lookup is read-only",
		FirstProgram:   filepath.Base(fields[0]),
		SimpleCommands: []execmw.SimpleCommand{{Argv: fields}},
	}
}

func safeLookupTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "-") || strings.Contains(target, "/") {
		return false
	}
	return !strings.ContainsAny(target, "\\'\"")
}
