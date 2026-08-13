package memory

import (
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
)

const (
	MetadataKey = "memory"

	metadataVersion               = 1
	threadKindMemoryConsolidation = "memory_consolidation"
	legacyMetadataThreadKind      = "memory_thread_kind"
)

type Stage2ThreadMetadata struct {
	UserID              string
	OwnershipToken      string
	InputWatermark      string
	InputHash           string
	StartedArtifactHash string
	StartedMemoryHash   string
	StartedSummaryHash  string
	WorkspaceRoot       string
}

type stage2MetadataPayload struct {
	Version             int    `json:"version"`
	ThreadKind          string `json:"thread_kind"`
	UserID              string `json:"user_id"`
	OwnershipToken      string `json:"ownership_token"`
	InputWatermark      string `json:"input_watermark"`
	InputHash           string `json:"input_hash"`
	StartedArtifactHash string `json:"started_artifact_hash"`
	StartedMemoryHash   string `json:"started_memory_hash"`
	StartedSummaryHash  string `json:"started_summary_hash"`
	WorkspaceRoot       string `json:"workspace_root"`
}

func BuildStage2Metadata(base map[string]string, spec Stage2ThreadSpec) (map[string]string, error) {
	payload := stage2MetadataPayload{
		Version:             metadataVersion,
		ThreadKind:          threadKindMemoryConsolidation,
		UserID:              strings.TrimSpace(spec.UserID),
		OwnershipToken:      strings.TrimSpace(spec.OwnershipToken),
		InputWatermark:      strings.TrimSpace(spec.InputWatermark),
		InputHash:           strings.TrimSpace(spec.InputHash),
		StartedArtifactHash: strings.TrimSpace(spec.StartedArtifactHash),
		StartedMemoryHash:   strings.TrimSpace(spec.StartedMemoryHash),
		StartedSummaryHash:  strings.TrimSpace(spec.StartedSummaryHash),
		WorkspaceRoot:       strings.TrimSpace(spec.WorkspaceRoot),
	}
	raw, err := sonic.MarshalString(payload)
	if err != nil {
		return nil, fmt.Errorf("memory: marshal stage2 metadata: %w", err)
	}
	out := cloneMetadata(base)
	out[MetadataKey] = raw
	return out, nil
}

func ParseStage2Metadata(metadata map[string]string) (Stage2ThreadMetadata, error) {
	raw := strings.TrimSpace(metadata[MetadataKey])
	if raw == "" {
		return Stage2ThreadMetadata{}, fmt.Errorf("memory: metadata %q is empty", MetadataKey)
	}
	var payload stage2MetadataPayload
	if err := sonic.UnmarshalString(raw, &payload); err != nil {
		return Stage2ThreadMetadata{}, fmt.Errorf("memory: parse stage2 metadata: %w", err)
	}
	if payload.Version != metadataVersion || strings.TrimSpace(payload.ThreadKind) != threadKindMemoryConsolidation {
		return Stage2ThreadMetadata{}, fmt.Errorf("memory: not a consolidation thread")
	}
	parsed := Stage2ThreadMetadata{
		UserID:              strings.TrimSpace(payload.UserID),
		OwnershipToken:      strings.TrimSpace(payload.OwnershipToken),
		InputWatermark:      strings.TrimSpace(payload.InputWatermark),
		InputHash:           strings.TrimSpace(payload.InputHash),
		StartedArtifactHash: strings.TrimSpace(payload.StartedArtifactHash),
		StartedMemoryHash:   strings.TrimSpace(payload.StartedMemoryHash),
		StartedSummaryHash:  strings.TrimSpace(payload.StartedSummaryHash),
		WorkspaceRoot:       strings.TrimSpace(payload.WorkspaceRoot),
	}
	if parsed.UserID == "" || parsed.OwnershipToken == "" || parsed.InputWatermark == "" {
		return Stage2ThreadMetadata{}, fmt.Errorf("memory: consolidation metadata requires user id, token and watermark")
	}
	return parsed, nil
}

func IsConsolidationThreadMetadata(metadata map[string]string) bool {
	raw := strings.TrimSpace(metadata[MetadataKey])
	if raw == "" {
		return strings.TrimSpace(metadata[legacyMetadataThreadKind]) == threadKindMemoryConsolidation
	}
	var payload stage2MetadataPayload
	if err := sonic.UnmarshalString(raw, &payload); err != nil {
		return false
	}
	return payload.Version == metadataVersion &&
		strings.TrimSpace(payload.ThreadKind) == threadKindMemoryConsolidation
}

func cloneMetadata(src map[string]string) map[string]string {
	out := make(map[string]string, len(src)+1)
	for k, v := range src {
		out[k] = v
	}
	return out
}
