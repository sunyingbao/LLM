package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromPathReadsBOEConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf_china-boe.yml")
	if err := os.WriteFile(path, []byte(`
mysql:
  dsn: user:password@tcp(127.0.0.1:3306)/agent_coordinator_test
  read_dsn: ""
  read_timeout_ms: 5000
tables:
  agent_session: t_agent_session
coordinator:
  namespace: cloud_agent
idgen:
  namespace: videocut_aigc_agent_coordinator
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromPath(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.MySQL.DSN != "user:password@tcp(127.0.0.1:3306)/agent_coordinator_test" {
		t.Fatalf("mysql config = %+v", cfg.MySQL)
	}
	if cfg.AgentSessionTable != "t_agent_session" {
		t.Fatalf("agent session table = %q", cfg.AgentSessionTable)
	}
	if cfg.CoordinatorNamespace != "cloud_agent" {
		t.Fatalf("coordinator config = %+v", cfg)
	}
	if cfg.IDGen.Namespace != "videocut_aigc_agent_coordinator" {
		t.Fatalf("idgen config = %+v", cfg.IDGen)
	}
}
