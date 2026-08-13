package bootstrap

import (
	"fmt"
	"sort"
	"strings"

	"eino-cli/deepagent/cloud/worker/bootstrap/config"
)

func runtimeConfigSummary(cfg config.Config) string {
	return fmt.Sprintf("models=[%s] roles=[%s] runtime={max_steps:%d max_model_calls:%d follow_up_tool:%t} fornax={enabled:%t ak_set:%t sk_set:%t region:%q timeout_ms:%d}",
		modelConfigSummary(cfg.Models),
		roleConfigSummary(cfg.Roles),
		cfg.Runtime.MaxSteps,
		cfg.Runtime.MaxModelCalls,
		cfg.Runtime.EnableFollowUpTool,
		cfg.Fornax.Enabled,
		strings.TrimSpace(cfg.Fornax.AK) != "",
		strings.TrimSpace(cfg.Fornax.SK) != "",
		strings.TrimSpace(cfg.Fornax.Region),
		cfg.Fornax.HTTPTimeoutMS,
	)
}

func modelConfigSummary(models map[string]config.ModelConfig) string {
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		modelCfg := models[id]
		parts = append(parts, fmt.Sprintf("%s:{sdk:%s name:%q endpoint_set:%t api_key_set:%t max_tokens:%d thinking:%s}",
			id,
			modelCfg.SDKType,
			strings.TrimSpace(modelCfg.ModelName),
			strings.TrimSpace(modelCfg.ModelEndpointID) != "",
			strings.TrimSpace(modelCfg.ModelAPIKey) != "",
			modelCfg.MaxTokens,
			thinkingConfigSummary(modelCfg.Thinking),
		))
	}
	return strings.Join(parts, ",")
}

func thinkingConfigSummary(thinking *config.ThinkingType) string {
	if thinking == nil {
		return "unset"
	}
	return string(*thinking)
}

func roleConfigSummary(roles map[string]config.RoleConfig) string {
	ids := make([]string, 0, len(roles))
	for id := range roles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		roleCfg := roles[id]
		parts = append(parts, fmt.Sprintf("%s:{models:%s default:%s approval:%s}",
			id,
			strings.Join(roleCfg.Models, "|"),
			roleCfg.DefaultModel,
			roleCfg.ApprovalPolicy,
		))
	}
	return strings.Join(parts, ",")
}
