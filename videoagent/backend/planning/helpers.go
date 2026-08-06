package planning

import (
	"encoding/json"
	"strings"

	"eino-cli/videoagent/backend/contract"
)

const defaultPromptTTSCPM = 280

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func firstArtifactValue(artifact contract.Artifact, keys ...string) string {
	var fields map[string]any
	if err := json.Unmarshal(artifact.Data, &fields); err != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizedDurationMS(duration int) int {
	if duration <= 0 {
		return 3000
	}
	if duration < 1000 {
		return duration * 1000
	}
	return duration
}
