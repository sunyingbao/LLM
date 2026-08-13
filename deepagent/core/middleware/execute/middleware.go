package execute

import (
	"context"
	"strings"
	"time"

	"eino-cli/deepagent/core/backends"
	deepmiddleware "eino-cli/deepagent/core/middleware"
	sdkutils "eino-cli/deepagent/core/utils"
	"github.com/cloudwego/eino/components/tool"
	einoutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

const middlewareName = "execute"

type Config struct {
	Executor backends.CommandExecutor

	// PolicyProfile is the single configuration entry for command policy. Empty
	// means NewDefaultPolicyProfile(nil).
	PolicyProfile PolicyProfile

	WorkDir string
	Shell   ShellConfig

	DefaultTimeout  time.Duration
	MaxTimeout      time.Duration
	MaxOutputBytes  int
	MaxOutputTokens int

	ToolName        string
	ToolDescription string
}

type ExecuteMiddleware struct {
	deepmiddleware.BaseMiddleware

	cfg           Config
	builder       CommandBuilder
	policyProfile PolicyProfile
	formatter     OutputFormatter
}

func New(cfg Config) *ExecuteMiddleware {
	builder := NewShellCommandBuilder(cfg.Shell)
	return &ExecuteMiddleware{
		cfg:           cfg,
		builder:       builder,
		policyProfile: ensurePolicyProfile(cfg.PolicyProfile),
		formatter:     NewJSONOutputFormatter(),
	}
}

func (m *ExecuteMiddleware) Name() string {
	return middlewareName
}

func (m *ExecuteMiddleware) Tools(ctx context.Context) ([]tool.BaseTool, error) {
	if m == nil || m.cfg.Executor == nil {
		return nil, nil
	}
	return []tool.BaseTool{m.newExecCommandTool()}, nil
}

func (m *ExecuteMiddleware) BuildInitialContext(ctx context.Context) ([]*schema.Message, error) {
	if m == nil || m.cfg.Executor == nil {
		return nil, nil
	}
	return []*schema.Message{schema.SystemMessage(m.systemPrompt())}, nil
}

func (m *ExecuteMiddleware) newExecCommandTool() tool.BaseTool {
	t, _ := einoutils.InferTool(
		m.cfg.toolName(),
		m.cfg.toolDescription(),
		func(ctx context.Context, input *ExecCommandInput) (string, error) {
			if input == nil {
				return m.formatDenied(nil, "", "input is required"), nil
			}
			result, err := m.run(ctx, *input)
			if err != nil {
				return m.formatDenied(nil, "", err.Error()), nil
			}
			return m.formatter.Format(ctx, result), nil
		},
	)
	return t
}

func (m *ExecuteMiddleware) run(ctx context.Context, input ExecCommandInput) (ExecCommandOutput, error) {
	normalized, err := normalizeRequest(input, m.cfg)
	if err != nil {
		return ExecCommandOutput{}, err
	}
	command, err := m.builder.Build(ctx, normalized)
	if err != nil {
		return ExecCommandOutput{}, err
	}
	execCtx := ctx
	cancel := func() {}
	if command.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, command.Timeout)
	}
	defer cancel()
	result, err := m.cfg.Executor.ExecuteCommand(execCtx, backends.CommandRequest{
		Command:        command.RawCommand,
		WorkDir:        command.WorkDir,
		Env:            command.Env,
		Timeout:        command.Timeout,
		MaxOutputBytes: maxOutputBytes(m.cfg),
	})
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return ExecCommandOutput{
				Command:  command.Argv,
				WorkDir:  command.WorkDir,
				ExitCode: 124,
				TimedOut: true,
				Reason:   "command timed out",
			}, nil
		}
		return ExecCommandOutput{}, err
	}
	output := result.Output
	truncated := result.Truncated
	if limit := maxOutputBytes(m.cfg); limit > 0 && len(output) > limit {
		output = truncateHeadTail(output, limit)
		truncated = true
	}
	return ExecCommandOutput{
		Command:   command.Argv,
		WorkDir:   command.WorkDir,
		ExitCode:  result.ExitCode,
		Output:    output,
		TimedOut:  result.TimedOut,
		Truncated: truncated,
	}, nil
}

func (m *ExecuteMiddleware) formatDenied(command []string, workDir string, reason string) string {
	return sdkutils.ToString(ExecCommandOutput{
		Command:  command,
		WorkDir:  workDir,
		Denied:   true,
		Reason:   reason,
		ExitCode: 1,
	})
}

func (m *ExecuteMiddleware) systemPrompt() string {
	parts := []string{strings.TrimSpace(executePrompt)}
	if instructions := strings.TrimSpace(m.policyProfile.Instructions); instructions != "" {
		parts = append(parts, instructions)
	}
	return strings.Join(parts, "\n\n")
}

func (cfg Config) toolName() string {
	if strings.TrimSpace(cfg.ToolName) != "" {
		return strings.TrimSpace(cfg.ToolName)
	}
	return DefaultToolName
}

func (cfg Config) toolDescription() string {
	if strings.TrimSpace(cfg.ToolDescription) != "" {
		return strings.TrimSpace(cfg.ToolDescription)
	}
	return "Execute a shell command with structured timeout, output limits, and command policy enforcement."
}

const executePrompt = `
You have access to exec_command for shell commands.
- Use exec_command for inspection commands, builds, tests, and other shell tasks allowed by the current execution policy.
- Do not use exec_command to edit files when file editing tools are available.
- Set timeout_ms for commands that may run for a long time.
- If exec_command returns denied=true, do not repeat the same command. Choose an allowed alternative or explain the limitation.
- Some agents may have a read-only execution policy; in that case use only read-only investigation commands.
`
