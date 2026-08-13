package execute

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func normalizeRequest(input ExecCommandInput, cfg Config) (NormalizedRequest, error) {
	cmd := strings.TrimSpace(input.Cmd)
	if cmd == "" {
		return NormalizedRequest{}, fmt.Errorf("cmd is required")
	}
	workDir, err := normalizeWorkDir(input.WorkDir, cfg.WorkDir)
	if err != nil {
		return NormalizedRequest{}, err
	}
	timeout := cfg.DefaultTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if input.TimeoutMS > 0 {
		timeout = time.Duration(input.TimeoutMS) * time.Millisecond
	}
	maxTimeout := cfg.MaxTimeout
	if maxTimeout <= 0 {
		maxTimeout = defaultMaxTimeout
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	maxTokens := input.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = cfg.MaxOutputTokens
	}
	if maxTokens <= 0 {
		maxTokens = defaultOutputTokens
	}
	return NormalizedRequest{
		RawInput:        input,
		Cmd:             cmd,
		WorkDir:         workDir,
		Timeout:         timeout,
		MaxOutputTokens: maxTokens,
		Justification:   strings.TrimSpace(input.Justification),
	}, nil
}

func normalizeWorkDir(raw, base string) (string, error) {
	raw = strings.TrimSpace(raw)
	base = strings.TrimSpace(base)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		base = cwd
	}
	if raw == "" {
		return filepath.Clean(base), nil
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	return filepath.Clean(filepath.Join(base, raw)), nil
}
