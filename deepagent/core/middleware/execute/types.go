package execute

import "time"

const (
	DefaultToolName = "exec_command"

	defaultTimeout       = 10 * time.Second
	defaultMaxTimeout    = 10 * time.Minute
	defaultMaxOutputSize = 1024 * 1024
	defaultOutputTokens  = 20000
)

type ExecCommandInput struct {
	// Cmd is the shell command text provided by the model, for example
	// "rg foo | wc -l". It is required and becomes CommandSpec.RawCommand after
	// normalization.
	Cmd string `json:"cmd" jsonschema:"description=Shell command to execute,required"`

	// WorkDir is the command working directory requested by the model. Relative
	// paths are resolved against Config.WorkDir. Empty means Config.WorkDir.
	WorkDir string `json:"workdir,omitempty" jsonschema:"description=Working directory for the command. Defaults to the middleware workdir."`

	// TimeoutMS is the command timeout in milliseconds. Empty or non-positive
	// means Config.DefaultTimeout. Values above Config.MaxTimeout are capped.
	TimeoutMS int64 `json:"timeout_ms,omitempty" jsonschema:"description=Maximum command runtime in milliseconds. Defaults to the middleware timeout."`

	// MaxOutputTokens is the model-facing output budget. The first version keeps
	// this as a normalized request field; byte-level truncation is enforced by
	// Config.MaxOutputBytes.
	MaxOutputTokens int `json:"max_output_tokens,omitempty" jsonschema:"description=Maximum output token budget returned to the model."`

	// Justification is an optional human-facing reason for risky commands. It is
	// carried into CommandSpec for approval UI, but it is not used to decide
	// whether a command is safe.
	Justification string `json:"justification,omitempty" jsonschema:"description=Optional reason shown when the command requires approval."`

	// Future LLM-facing fields, intentionally not exposed in the first version:
	//   YieldTimeMS int64  `json:"yield_time_ms,omitempty"`
	//   Login       *bool  `json:"login,omitempty"`
	//   TTY         bool   `json:"tty,omitempty"`
	//
	// Codex exposes these because it supports PTY sessions, write_stdin, yield
	// timing, and shell selection. This middleware does not support those yet,
	// so exposing the fields would mislead the model.
}

type NormalizedRequest struct {
	RawInput ExecCommandInput

	Cmd             string
	WorkDir         string
	Timeout         time.Duration
	MaxOutputTokens int
	Justification   string
}

type ExecCommandOutput struct {
	Command   []string `json:"command,omitempty"`
	WorkDir   string   `json:"workdir,omitempty"`
	ExitCode  int      `json:"exit_code"`
	Output    string   `json:"output,omitempty"`
	TimedOut  bool     `json:"timed_out,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Denied    bool     `json:"denied,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

// CommandSpec is the executable command produced by CommandBuilder.
//
// RawCommand is the shell script exactly as supplied by the model after
// whitespace normalization, for example "rg foo | wc -l". CommandClassifier
// classifies RawCommand because it must inspect shell operators, redirection,
// and command substitutions.
//
// Argv is the process argv used by a runner, for example
// []string{"/bin/bash", "-lc", "rg foo | wc -l"}. It is not the primary
// classifier input because joining argv back into text loses shell intent.
//
// Display is the user-facing command shown in tool output and approval UI. For
// the default shell builder it is the same as RawCommand.
type CommandSpec struct {
	// Argv is the actual process argv that a runner may execute. With the
	// default shell builder this is typically ["/bin/bash", "-lc", RawCommand].
	Argv []string

	// Display is the command shown in tool output and approval UI. It should be
	// stable and human-readable. For the default shell builder it equals
	// RawCommand.
	Display string

	// RawCommand is the normalized shell script supplied by the model. The
	// command classifier treats this field as the source of truth.
	RawCommand string

	// WorkDir is the normalized working directory for execution.
	WorkDir string

	// Timeout is the normalized runtime limit for this command.
	Timeout time.Duration

	// Env contains extra environment variables for this command.
	Env map[string]string

	// Justification is the optional human-facing reason carried from input for
	// approval UI. It does not affect classification.
	Justification string
}

type Action string

const (
	ActionAllow           Action = "allow"
	ActionRequireApproval Action = "require_approval"
	ActionDeny            Action = "deny"
)

type ApprovalKey struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Decision struct {
	Action       Action
	Reason       string
	Class        CommandClass
	ApprovalKeys []ApprovalKey
}

// PolicyRequest is the command-level input to Policy.
//
// Earlier drafts carried ExecCommandInput and NormalizedRequest as well, but
// that made the policy boundary unclear. The policy should decide on the
// concrete command shape that would be executed; command text, workdir,
// timeout, env, and justification are already represented in CommandSpec.
type PolicyRequest struct {
	Command CommandSpec
}

type CommandClass string

const (
	CommandSafe      CommandClass = "safe"
	CommandRisky     CommandClass = "risky"
	CommandDangerous CommandClass = "dangerous"
	CommandForbidden CommandClass = "forbidden"
	CommandUnknown   CommandClass = "unknown"
)

// SimpleCommand is one plain command extracted from a shell script.
//
// For "rg foo | wc -l", the classifier records two simple commands:
// ["rg", "foo"] and ["wc", "-l"]. This is only a conservative parser for
// policy classification; it is not a complete shell AST.
type SimpleCommand struct {
	Argv []string
}

type CommandClassification struct {
	// Class is the final classification for the whole shell script. When the
	// script contains multiple simple commands, this is the most severe class
	// among them.
	Class CommandClass

	// Reason explains why Class was chosen. It is intended for tool output,
	// approval UI, and tests; it is not parsed as policy input.
	Reason string

	// FirstProgram is the basename of the first parsed simple command, for
	// example "rg" for "rg foo | wc -l" or "git" for "/usr/bin/git diff".
	// It is useful as a coarse approval key or display hint. It is not a safety
	// summary: later simple commands can still make the whole script dangerous.
	FirstProgram string

	// SimpleCommands are the conservative top-level commands parsed from the
	// shell script. For "rg foo | wc -l" this contains ["rg", "foo"] and
	// ["wc", "-l"]. The parser only supports the simple shapes needed for
	// policy classification and is not a complete shell AST.
	SimpleCommands []SimpleCommand
}
