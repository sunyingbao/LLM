package bootstrap

import (
	"strings"

	"code.byted.org/gdp/env"
	cloudworker "eino-cli/deepagent/cloud/worker"
	"eino-cli/deepagent/cloud/worker/bootstrap/config"
	"eino-cli/deepagent/core/middleware"
	"eino-cli/deepagent/core/middleware/repairjson"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

func newCloudAgentConfig(cfg config.Config, chatModels map[string]model.ToolCallingChatModel, tools []tool.BaseTool, callbackHandlers []callbacks.Handler) cloudworker.Config {
	return cloudworker.Config{
		Host:   newCloudAgentHostConfig(cfg),
		Thread: newCloudAgentThreadConfig(cfg),
		Turn:   newCloudAgentTurnConfig(cfg, chatModels, tools, callbackHandlers),
		Memory: newCloudAgentMemoryConfig(cfg),
		Output: cfg.Output,
	}
}

func newCloudAgentHostConfig(cfg config.Config) cloudworker.HostConfig {
	return cloudworker.HostConfig{
		Namespace:                     cfg.Worker.Namespace,
		Env:                           env.Env(),
		Concurrency:                   cfg.Worker.Concurrency,
		ScanLimit:                     cfg.Worker.ScanLimit,
		MessageLimit:                  cfg.Worker.MessageLimit,
		LeaseMS:                       cfg.Worker.LeaseMS,
		ScanInterval:                  cfg.Worker.ScanInterval,
		MessagePollInterval:           cfg.Worker.MessagePollInterval,
		IdleTimeout:                   cfg.Worker.IdleTimeout,
		ShutdownDrainTimeout:          cfg.Worker.ShutdownDrainTimeout,
		ShutdownInterruptDrainTimeout: cfg.Worker.ShutdownInterruptDrainTimeout,
		InterruptDrainTimeout:         cfg.Worker.InterruptDrainTimeout,
	}
}

func newCloudAgentThreadConfig(cfg config.Config) cloudworker.ThreadConfig {
	return cloudworker.ThreadConfig{
		WorkDir: cfg.Runtime.WorkDir,
		Backend: cfg.Backend,
		Compaction: cloudworker.CompactionConfig{
			AutoCompactLimitTokens: cfg.Runtime.AutoCompactLimitTokens,
			CompactKeptUserTokens:  cfg.Runtime.CompactKeptUserTokens,
			PromptAppend:           cfg.Runtime.CompactPromptAppend,
		},
		Collaboration: cloudworker.CollaborationConfig{
			SpawnMetadataDescription: cfg.Runtime.SpawnMetadataDescription,
		},
	}
}

func newCloudAgentTurnConfig(cfg config.Config, chatModels map[string]model.ToolCallingChatModel, tools []tool.BaseTool, callbackHandlers []callbacks.Handler) cloudworker.TurnConfig {
	turn := cloudworker.TurnConfig{
		Prompt: cloudworker.PromptConfig{File: cfg.BasePrompt},
		Roles:  newCloudAgentRoles(cfg.Roles),
		Models: newCloudAgentModels(cfg.Models, chatModels),
		Defaults: cloudworker.TurnDefaults{
			Capabilities: cloudworker.TurnCapabilities{
				Tools:     append([]tool.BaseTool(nil), tools...),
				Callbacks: append([]callbacks.Handler(nil), callbackHandlers...),
			},
			Budget: cloudworker.TurnBudget{
				MaxSteps:      cfg.Runtime.MaxSteps,
				MaxModelCalls: cfg.Runtime.MaxModelCalls,
			},
			Policy: cloudworker.TurnPolicy{
				DisableApplyPatch:  cfg.Runtime.DisableApplyPatch,
				EnableFollowUpTool: cfg.Runtime.EnableFollowUpTool,
			},
		},
	}
	if source := strings.TrimSpace(cfg.Runtime.SkillsDir); source != "" {
		turn.Defaults.Capabilities.Skills.Sources = []string{source}
	}
	return turn
}

func newCloudAgentMemoryConfig(cfg config.Config) cloudworker.MemoryConfig {
	return cloudworker.MemoryConfig{
		Enabled:                  cfg.Memory.Enabled,
		ScanInterval:             cfg.Memory.ScanInterval,
		WakeupDebounce:           cfg.Memory.WakeupDebounce,
		Stage1IdleWindow:         cfg.Memory.Stage1IdleWindow,
		Stage1LeaseTTL:           cfg.Memory.Stage1LeaseTTL,
		Stage1MaxClaimedPerScan:  cfg.Memory.Stage1MaxClaimedPerScan,
		Stage1HistoryInput:       cfg.Memory.Stage1HistoryInput,
		Stage2LeaseTTL:           cfg.Memory.Stage2LeaseTTL,
		Stage2SuccessCooldown:    cfg.Memory.Stage2SuccessCooldown,
		Stage2ScanInterval:       cfg.Memory.Stage2ScanInterval,
		Stage2MaxUsersPerScan:    cfg.Memory.Stage2MaxUsersPerScan,
		Stage2OutputLimitPerUser: cfg.Memory.Stage2OutputLimitPerUser,
		WorkspaceRoot:            cfg.Memory.WorkspaceRoot,
	}
}

func newCloudAgentRoles(roles map[string]config.RoleConfig) map[string]cloudworker.RolePreset {
	out := make(map[string]cloudworker.RolePreset, len(roles))
	for id, role := range roles {
		out[id] = cloudworker.RolePreset{
			Prompt: cloudworker.PromptConfig{File: role.Prompt},
			Model: cloudworker.ModelPolicy{
				Default: role.DefaultModel,
				Allowed: append([]string(nil), role.Models...),
			},
			ApprovalPolicy: cloudworker.ApprovalPolicy(role.ApprovalPolicy),
			Middlewares:    []middleware.Middleware{repairjson.New()},
		}
	}
	return out
}

func newCloudAgentModels(models map[string]config.ModelConfig, chatModels map[string]model.ToolCallingChatModel) map[string]cloudworker.ModelProfile {
	out := make(map[string]cloudworker.ModelProfile, len(models))
	for id, modelCfg := range models {
		out[id] = cloudworker.ModelProfile{
			ChatModel:     chatModels[id],
			ModelName:     contextWindowModelName(modelCfg),
			ContextWindow: modelCfg.ContextWindow,
		}
	}
	return out
}

func contextWindowModelName(modelCfg config.ModelConfig) string {
	if name := strings.TrimSpace(modelCfg.ModelEndpointID); name != "" {
		return name
	}
	return strings.TrimSpace(modelCfg.ModelName)
}
