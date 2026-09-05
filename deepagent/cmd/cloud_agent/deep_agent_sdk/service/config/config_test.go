package config

import (
	"os"
	"path/filepath"
	"testing"

	cloudbackend "eino-cli/deepagent/cloud/backend"
)

func TestLoadFromPathReadsBOEConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf_china-boe.yml")
	if err := os.WriteFile(path, []byte(`
auth:
  mode: online
coordinator:
  namespace: cloud_agent
workspace:
  root: /opt/tiger/workspace
backend:
  type: ai_infra
  ai_infra:
    psm: pippit.sandbox.gateway
    biz_type: test
    biz_id_template: "user_{uid}"
    workdir_template: "/opt/tiger/workspace/{project_name}"
    action: SandboxToolCall
timeline:
  default_limit: 50
  max_limit: 200
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromPath(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ACNamespace != "cloud_agent" {
		t.Fatalf("coordinator config = %+v", cfg)
	}
	if cfg.WorkspaceRoot != "/opt/tiger/workspace" {
		t.Fatalf("workspace root = %q", cfg.WorkspaceRoot)
	}
	if cfg.Backend.Type != cloudbackend.TypeAIInfra || cfg.Backend.AIInfra.PSM != "pippit.sandbox.gateway" || cfg.Backend.AIInfra.BizType != "test" {
		t.Fatalf("backend config = %+v", cfg.Backend)
	}
	if cfg.TimelineDefaultLimit != 50 || cfg.TimelineMaxLimit != 200 {
		t.Fatalf("timeline limits = %d/%d", cfg.TimelineDefaultLimit, cfg.TimelineMaxLimit)
	}
	if cfg.UseLocalDefaultUIDOnAuthErr {
		t.Fatalf("online auth should not use local fallback uid")
	}
}

func TestLocalAuthEnablesLocalUIDFallback(t *testing.T) {
	t.Setenv("DEEP_AGENT_SDK_API_AUTH_MODE", "local")
	cfg := Load()

	if !cfg.UseLocalDefaultUIDOnAuthErr {
		t.Fatal("local auth should enable local uid fallback")
	}
}
