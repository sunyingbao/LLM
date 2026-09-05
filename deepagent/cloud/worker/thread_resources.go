//go:build !windows

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"code.byted.org/gopkg/logs/v2"
)

func readPromptFile(ctx context.Context, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	buf, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		logs.CtxError(ctx, "[cloudagent] read prompt failed: path=%s err=%v", path, err)
		return ""
	}
	return strings.TrimSpace(string(buf))
}

func nonEmptySkillSources(sources []string) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		if source = strings.TrimSpace(source); source != "" {
			out = append(out, source)
		}
	}
	return out
}

func expandLocalUserPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, expandLocalUserPath(path))
	}
	return out
}

func expandLocalUserPath(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}
