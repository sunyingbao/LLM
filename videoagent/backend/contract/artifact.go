package contract

import (
	"encoding/json"
	"strings"
)

func (artifact Artifact) Text(keys ...string) string {
	for _, key := range keys {
		var value string
		if artifact.decode(key, &value) && value != "" {
			return value
		}
	}
	return ""
}

func (artifact Artifact) PositiveInt(keys ...string) int {
	for _, key := range keys {
		var value int
		if artifact.decode(key, &value) && value > 0 {
			return value
		}
	}
	return 0
}

func (artifact Artifact) Int64s(key string) []int64 {
	var values []int64
	artifact.decode(key, &values)
	return values
}

func (artifact Artifact) Int32s(key string) []int32 {
	var values []int32
	artifact.decode(key, &values)
	return values
}

func (artifact Artifact) CutPlacements() []CutPlacement {
	var placements []CutPlacement
	artifact.decode("cut_placements", &placements)
	return placements
}

func (artifact Artifact) decode(key string, target any) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(artifact.Data, &fields) != nil {
		return false
	}
	return json.Unmarshal(fields[key], target) == nil
}

func ArtifactsByScene(artifacts []Artifact, kind string) map[string]Artifact {
	result := make(map[string]Artifact)
	for _, artifact := range artifacts {
		if artifact.Kind != kind {
			continue
		}
		sceneID := artifact.Text("scene_id")
		if sceneID == "" {
			_, sceneID, _ = strings.Cut(artifact.ID, ":")
		}
		if sceneID != "" {
			result[sceneID] = artifact
		}
	}
	return result
}
