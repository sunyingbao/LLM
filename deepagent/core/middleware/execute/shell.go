package execute

import (
	"context"
	"strings"
)

type ShellConfig struct {
	Program string
	Args    []string
}

func defaultShellConfig() ShellConfig {
	return ShellConfig{
		Program: "/bin/bash",
		Args:    []string{"-lc"},
	}
}

type CommandBuilder interface {
	Build(ctx context.Context, req NormalizedRequest) (CommandSpec, error)
}

type ShellCommandBuilder struct {
	Shell ShellConfig
}

func NewShellCommandBuilder(shell ShellConfig) *ShellCommandBuilder {
	if strings.TrimSpace(shell.Program) == "" {
		shell = defaultShellConfig()
	}
	if len(shell.Args) == 0 {
		shell.Args = []string{"-lc"}
	}
	return &ShellCommandBuilder{Shell: shell}
}

func (b *ShellCommandBuilder) Build(ctx context.Context, req NormalizedRequest) (CommandSpec, error) {
	shell := b.Shell
	argv := make([]string, 0, 1+len(shell.Args)+1)
	argv = append(argv, shell.Program)
	argv = append(argv, shell.Args...)
	argv = append(argv, req.Cmd)
	return CommandSpec{
		Argv:          argv,
		Display:       req.Cmd,
		RawCommand:    req.Cmd,
		WorkDir:       req.WorkDir,
		Timeout:       req.Timeout,
		Env:           map[string]string{},
		Justification: req.Justification,
	}, nil
}
