package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"eino-cli/backend/config"
	"eino-cli/backend/consts"
	"eino-cli/backend/sandbox"
	deepagent "eino-cli/deepagent/core"
	deepagentmiddleware "eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/baseprompt"
	host "eino-cli/deepagent/host"
	sgadktools "eino-cli/deepagent/tools/sgadk"
)

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
