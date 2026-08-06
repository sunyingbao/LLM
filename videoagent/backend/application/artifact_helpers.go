package videoagent

import (
	"encoding/json"
	"strings"
)

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func artifactsByScene(artifacts []Artifact, kind string) map[string]Artifact {
	result := make(map[string]Artifact)
	for _, artifact := range artifacts {
		if artifact.Kind != kind {
			continue
		}
		sceneID := firstArtifactValue(artifact, "scene_id")
		if sceneID == "" {
			_, sceneID, _ = strings.Cut(artifact.ID, ":")
		}
		if sceneID != "" {
			result[sceneID] = artifact
		}
	}
	return result
}

func firstArtifactValue(artifact Artifact, keys ...string) string {
	var data map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &data) != nil {
		return ""
	}
	for _, key := range keys {
		var value string
		if json.Unmarshal(data[key], &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func firstArtifactInt(artifact Artifact, keys ...string) int {
	var data map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &data) != nil {
		return 0
	}
	for _, key := range keys {
		var value int
		if json.Unmarshal(data[key], &value) == nil && value > 0 {
			return value
		}
	}
	return 0
}

func artifactInt32s(artifact Artifact, key string) []int32 {
	var data map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &data) != nil {
		return nil
	}
	var values []int32
	if json.Unmarshal(data[key], &values) != nil {
		return nil
	}
	return values
}
