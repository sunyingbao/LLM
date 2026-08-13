package memory

import (
	_ "embed"
	"strings"

	"github.com/cloudwego/eino/schema"
)

//go:embed templates/stage1_system.md
var stage1SystemPrompt string

//go:embed templates/stage1_input.md
var stage1InputTemplate string

//go:embed templates/stage2_system.md
var stage2SystemPrompt string

//go:embed templates/stage2_input.md
var stage2InputTemplate string

func BuildStage1PromptMessages(input Stage1ExtractInput) []*schema.Message {
	return []*schema.Message{
		schema.SystemMessage(stage1SystemPrompt),
		schema.UserMessage(renderStage1Input(input)),
	}
}

func renderStage1Input(input Stage1ExtractInput) string {
	out := stage1InputTemplate
	replacements := map[string]string{
		"{{ rollout_path }}":     emptyAsUnknown(input.RolloutPath),
		"{{ rollout_cwd }}":      emptyAsUnknown(input.RolloutCWD),
		"{{ rollout_contents }}": strings.TrimSpace(input.RolloutContents),
	}
	for old, newValue := range replacements {
		out = strings.ReplaceAll(out, old, newValue)
	}
	return out
}

func BuildStage2ThreadPrompt(input ConsolidateInput) string {
	return strings.TrimSpace(stage2SystemPrompt) + "\n\n" + strings.TrimSpace(renderStage2Input(input))
}

func renderStage2Input(input ConsolidateInput) string {
	out := stage2InputTemplate
	replacements := map[string]string{
		"{{ user_id }}":             emptyAsUnknown(input.Memory.UserID),
		"{{ workspace_root }}":      emptyAsUnknown(input.Memory.WorkspaceRoot),
		"{{ selected_stage1_ids }}": strings.Join(input.SelectedStage1IDs, ", "),
		"{{ current_memory }}":      emptyAsNone(input.CurrentMemory),
		"{{ current_summary }}":     emptyAsNone(input.CurrentSummary),
		"{{ workspace_diff }}":      emptyAsNone(input.WorkspaceDiff),
		"{{ raw_memories }}":        emptyAsNone(input.RawMemories),
	}
	for old, newValue := range replacements {
		out = strings.ReplaceAll(out, old, newValue)
	}
	return out
}

func emptyAsUnknown(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	return v
}

func emptyAsNone(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "(none)"
	}
	return v
}
