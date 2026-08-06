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
		{AK: "real-ak", SK: "real-sk"},
	} {
		_, err := NewChatModel(context.Background(), ChatModelConfig{Provider: "fornax", Fornax: config})
		if err == nil {
			t.Fatalf("NewChatModel() accepted identity %#v", config)
		}
	}
}

func TestFornaxModelRejectsIncompleteMaaSConfig(t *testing.T) {
	identity := &FornaxConfig{AppID: 1, AK: "real-ak", SK: "real-sk"}
	for _, config := range []ChatModelConfig{
		{Provider: "fornax", Fornax: identity},
		{Provider: "fornax", Fornax: identity, APIKey: "key", Model: "endpoint"},
		{Provider: "fornax", Fornax: identity, APIKey: "key", BaseURL: "https://ark.example", Model: "replace-with-model"},
	} {
		if _, err := NewChatModel(context.Background(), config); err == nil {
			t.Fatalf("NewChatModel() accepted incomplete MaaS config %#v", config)
		}
	}
}

func TestFornaxIdentityDoesNotRequireAppID(t *testing.T) {
	if err := validateFornaxConfig(&FornaxConfig{AK: "real-ak", SK: "real-sk"}); err != nil {
		t.Fatalf("validateFornaxConfig() error = %v", err)
	}
}
