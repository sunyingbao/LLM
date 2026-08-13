package session

import "testing"

func TestSessionMetadataJSONIncludesEmail(t *testing.T) {
	got := sessionMetadataJSON("alice@example.com")
	if got != `{"email":"alice@example.com"}` {
		t.Fatalf("sessionMetadataJSON()=%q, want email metadata", got)
	}
}

func TestSessionMetadataJSONEmptyEmailNoop(t *testing.T) {
	got := sessionMetadataJSON("")
	if got != "{}" {
		t.Fatalf("sessionMetadataJSON()=%q, want empty object", got)
	}
}
