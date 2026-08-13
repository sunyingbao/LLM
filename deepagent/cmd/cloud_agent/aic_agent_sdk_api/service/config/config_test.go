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
  psm: ad.creative.aic_agent_coordinator
  cluster: boe-cluster
  direct_hostports: ""
aic_agent_sdk_session:
  psm: ad.creative.aic_agent_sdk_session
  cluster: session-cluster
  direct_hostports: ""
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

	if cfg.ACNamespace != "cloud_agent" || cfg.AgentCoordinatorPSM != "ad.creative.aic_agent_coordinator" {
		t.Fatalf("coordinator config = %+v", cfg)
	}
	if cfg.AICAgentSDKSessionPSM != "ad.creative.aic_agent_sdk_session" || cfg.AICAgentSDKSessionCluster != "session-cluster" {
		t.Fatalf("AIC Agent SDK session config = %+v", cfg)
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
