package handler

import "testing"

func TestSubmitInputInterruptParsesRawBody(t *testing.T) {
	got := submitInputInterrupt([]byte(`{"interrupt":{"kind":"follow_up","data":{"user_answer":"yes"}}}`))
	if got == nil {
		t.Fatal("submitInputInterrupt() = nil")
	}
	if got.Kind != "follow_up" {
		t.Fatalf("Kind = %q", got.Kind)
	}
	if string(got.Data) != `{"user_answer":"yes"}` {
		t.Fatalf("Data = %s", got.Data)
	}
}

func TestSubmitInputInterruptIgnoresInvalidOrMissingBody(t *testing.T) {
	for _, body := range [][]byte{
		nil,
		[]byte(`{bad json`),
		[]byte(`{"content":"hello"}`),
	} {
		if got := submitInputInterrupt(body); got != nil {
			t.Fatalf("submitInputInterrupt(%s)=%+v, want nil", body, got)
		}
	}
}
