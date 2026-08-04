package videoagent

import (
	"context"
	"testing"
)

func TestFornaxModelRejectsMissingOrPlaceholderIdentity(t *testing.T) {
	for _, config := range []*FornaxConfig{
		nil,
		{AK: "replace-with-fornax-ak", SK: "replace-with-fornax-sk"},
		{AK: "real-ak"},
	} {
		_, err := NewChatModel(context.Background(), ChatModelConfig{Provider: "fornax", Fornax: config})
		if err == nil {
			t.Fatalf("NewChatModel() accepted identity %#v", config)
		}
	}
}
