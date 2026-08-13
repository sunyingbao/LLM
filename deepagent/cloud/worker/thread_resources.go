//go:build !windows

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"code.byted.org/gopkg/logs/v2"
	cloudbackend "eino-cli/deepagent/cloud/backend"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/baseprompt"
	skillmw "eino-cli/deepagent/core/middleware/skill"
)

// buildPromptMiddlewares applies global prompts before role prompts. Empty
// inline prompts and unreadable prompt files are skipped.
func (b *threadBuilder) buildPromptMiddlewares(ctx context.Context, profile ResolvedTurnProfile) []middleware.Middleware {
	var out []middleware.Middleware
	for _, source := range profile.Prompt.Sources {
		for _, prompt := range []string{
			source.Text,
			readPromptFile(ctx, source.File),
		} {
			if prompt = strings.TrimSpace(prompt); prompt != "" {
				out = append(out, baseprompt.New(prompt))
			}
		}
	}
	return out
}

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

// buildSkillLoader enables turn-level skills.
func (b *threadBuilder) buildSkillLoader(turnProfile ResolvedTurnProfile, threadProfile ResolvedThreadProfile, backend backends.Backend) skillmw.Loader {
	if turnProfile.Capabilities.Skills.Loader != nil {
		return turnProfile.Capabilities.Skills.Loader
	}
	sources := nonEmptySkillSources(turnProfile.Capabilities.Skills.Sources)
	if len(sources) == 0 {
		return nil
	}
	if threadProfile.Backend.Type == cloudbackend.TypeLocal {
		if len(sources) == 1 {
			return skillmw.NewFileSystemSkillLoader([]string{"."}, backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
				RootDir:     expandLocalUserPath(sources[0]),
				VirtualMode: true,
			}), true, nil)
		}
		return skillmw.NewFileSystemSkillLoader(expandLocalUserPaths(sources), backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     "/",
			VirtualMode: true,
		}), true, nil)
	}
	if backend == nil {
		return nil
	}
	return skillmw.NewFileSystemSkillLoader(sources, backend, true, nil)
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
