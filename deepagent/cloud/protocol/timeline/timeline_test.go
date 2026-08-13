package timeline

import "testing"

func TestNormalizePayloadPreservesJSONObjects(t *testing.T) {
	got := NormalizePayload([]byte(` {"parts":[{"type":"text","text":"hello"}]} `))
	if string(got) != `{"parts":[{"type":"text","text":"hello"}]}` {
		t.Fatalf("payload = %s", got)
	}
}

func TestNormalizePayloadTurnsInvalidJSONIntoString(t *testing.T) {
	got := NormalizePayload([]byte(`not-json`))
	if string(got) != `"not-json"` {
		t.Fatalf("payload = %s", got)
	}
}

func TestNormalizePayloadUsesNullForEmptyPayload(t *testing.T) {
	got := NormalizePayload(nil)
	if string(got) != `null` {
		t.Fatalf("payload = %s", got)
	}
}
