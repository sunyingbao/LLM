package memory

import "testing"

func TestIsConsolidationThreadMetadataAllowsInvalidOwnedEnvelope(t *testing.T) {
	metadata, err := BuildStage2Metadata(nil, Stage2ThreadSpec{
		UserID:         "user-1",
		OwnershipToken: "token",
		InputWatermark: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}

	metadata[MetadataKey] = `{"version":1,"thread_kind":"memory_consolidation","user_id":"user-1"}`
	if !IsConsolidationThreadMetadata(metadata) {
		t.Fatalf("invalid owned envelope should still route through memory thread handling")
	}
	if _, err := ParseStage2Metadata(metadata); err == nil {
		t.Fatalf("ParseStage2Metadata() error = nil, want strict validation failure")
	}
}

func TestIsConsolidationThreadMetadataRoutesLegacyOwnedThreadAsStale(t *testing.T) {
	metadata := map[string]string{
		"memory_thread_kind": "memory_consolidation",
	}
	if !IsConsolidationThreadMetadata(metadata) {
		t.Fatalf("legacy owned metadata should still route through memory thread handling")
	}
	if _, err := ParseStage2Metadata(metadata); err == nil {
		t.Fatalf("ParseStage2Metadata() error = nil, want strict envelope validation failure")
	}
}
