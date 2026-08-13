package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/schema"

	"eino-cli/backend/config"
	"eino-cli/backend/consts"
	"eino-cli/backend/sandbox"
	deepagent "eino-cli/deepagent/core"
	deepagentmiddleware "eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/baseprompt"
	host "eino-cli/deepagent/host"
	runtimecontext "eino-cli/deepagent/host/executioncontext"
	"eino-cli/deepagent/memory/autodream"
	sgadktools "eino-cli/deepagent/tools/sgadk"
)

func (runtime *LocalRuntime) runDream(ctx context.Context) (result ActionResult, err error) {
	if runtime == nil || runtime.cfg == nil {
		return result, fmt.Errorf("runtime configuration is required")
	}
	memoryRoot := config.DreamMemoryDir()
	lastConsolidatedAt, err := autodream.ReadLastConsolidatedAt(memoryRoot)
	if err != nil {
		return result, fmt.Errorf("read dream lock: %w", err)
	}
	candidates, err := autodream.ListJSONLSessionCandidates(config.TranscriptDir())
	if err != nil {
		return result, fmt.Errorf("list dream sessions: %w", err)
	}
	sessionIDs := autodream.FilterSessionsTouchedSince(candidates, lastConsolidatedAt, "")
	if len(sessionIDs) == 0 {
		return ActionResult{Success: true, Output: "dream: no transcript sessions to consolidate"}, nil
	}
	lock, err := autodream.TryAcquireConsolidationLock(memoryRoot)
	if err != nil {
		return result, fmt.Errorf("acquire dream lock: %w", err)
	}
	if lock == nil {
		return ActionResult{Success: true, Output: "dream: another consolidation is already running"}, nil
	}
	prompt := autodream.BuildConsolidationPrompt(memoryRoot, config.TranscriptDir(), sessionIDs)
	forkResult, err := runtime.runDreamFork(ctx, prompt)
	if err != nil {
		autodream.RollbackConsolidationLock(lock)
		return result, err
	}
	if len(forkResult.FilesTouched) == 0 {
		return ActionResult{Success: true, Output: "dream: completed; no memory files changed"}, nil
	}
	result = ActionResult{
		Success: true,
		Output:  fmt.Sprintf("dream: improved %d memory files: %s", len(forkResult.FilesTouched), strings.Join(forkResult.FilesTouched, ", ")),
	}
	return result, nil
}

func (runtime *LocalRuntime) runDreamFork(ctx context.Context, prompt string) (result autodream.ForkedAgentResult, err error) {
	ctx = runtimecontext.WithQuerySource(ctx, runtimecontext.QuerySourceAutoDream)
	dreamAgent, err := buildAutoDreamAgent(ctx, runtime.cfg)
	if err != nil {
		return result, err
	}
	stream, err := dreamAgent.Stream(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return result, err
	}
	defer stream.Close()
	var outputs []string
	var filesTouched []string
	for {
		message, receiveErr := stream.Recv()
		if receiveErr == io.EOF {
			break
		}
		if receiveErr != nil {
			return result, receiveErr
		}
		filesTouched = append(filesTouched, dreamTouchedFiles(message)...)
		if message.Role == schema.Assistant && len(message.ToolCalls) == 0 {
			if output := strings.TrimSpace(message.Content); output != "" {
				outputs = append(outputs, output)
			}
		}
	}
	result = autodream.ForkedAgentResult{FilesTouched: uniqueStrings(filesTouched), Output: strings.Join(outputs, "\n")}
	return result, nil
}

func buildAutoDreamAgent(ctx context.Context, cfg *config.Config) (agent *deepagent.DeepAgent, err error) {
	if cfg == nil || cfg.Models[cfg.DefaultModel] == nil {
		return nil, fmt.Errorf("dream model configuration is required")
	}
	chatModel, err := host.BuildToolCallingChatModel(ctx, cfg.Models[cfg.DefaultModel])
	if err != nil {
		return nil, err
	}
	agent, err = deepagent.NewFromSpec(ctx, &deepagent.DeepAgentSpec{
		Model: chatModel,
		Middlewares: []deepagentmiddleware.Middleware{
			baseprompt.New("Consolidate session transcripts into markdown memory. Only write inside the dream memory root."),
		},
		Tools:    sgadktools.BuildAutoDreamTools(sandbox.Default()),
		MaxSteps: consts.DefaultAgentIterations,
	})
	return agent, err
}

func dreamTouchedFiles(message *schema.Message) (paths []string) {
	for _, call := range message.ToolCalls {
		if call.Function.Name != "write_file" && call.Function.Name != "edit_file" {
			continue
		}
		var arguments struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &arguments) == nil && arguments.FilePath != "" {
			paths = append(paths, arguments.FilePath)
		}
	}
	return paths
}

func uniqueStrings(values []string) (unique []string) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
