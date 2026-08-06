package videoagent

import "testing"

func TestCredentialsBuildChatModelConfig(t *testing.T) {
	credentials := CredentialsConfig{
		Fornax: FornaxConfig{AK: "fornax-ak", SK: "fornax-sk"},
		Models: map[string]ModelCredential{
			"main": {
				APIKey:   "model-key",
				BaseURL:  "https://ark.example/api/v3",
				Region:   "cn-beijing",
				Endpoint: "ep-main",
			},
		},
	}

	config, err := credentials.ChatModelConfig("main")
	if err != nil {
		t.Fatal(err)
	}
	if config.Provider != "fornax" || config.Model != "ep-main" || config.Fornax == nil {
		t.Fatalf("chat model config = %#v", config)
	}
	if config.Fornax.AK != "fornax-ak" || config.Fornax.SK != "fornax-sk" || config.Fornax.Region != "cn-beijing" {
		t.Fatalf("fornax config = %#v", config.Fornax)
	}
}

func TestCredentialsRejectMissingModel(t *testing.T) {
	_, err := (CredentialsConfig{Fornax: FornaxConfig{AK: "ak", SK: "sk"}}).ChatModelConfig("missing")
	if err == nil {
		t.Fatal("expected missing model credentials error")
	}
}
