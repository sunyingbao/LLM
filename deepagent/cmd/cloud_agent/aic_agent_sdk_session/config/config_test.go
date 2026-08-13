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
  psm: toutiao.mysql.cc_family_agent_coordinator
  db_name: cc_family_agent_coordinator
  dsn: ""
  read_dsn: ""
  read_timeout_ms: 5000
tables:
  agent_session: t_agent_session
coordinator:
  psm: ad.creative.aic_agent_coordinator
  cluster: boe-cluster
  direct_hostports: ""
  namespace: cloud_agent
  disabled: false
idgen:
  namespace: videocut_aigc_agent_coordinator
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromPath(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.MySQL.PSM != "toutiao.mysql.cc_family_agent_coordinator" || cfg.MySQL.DBName != "cc_family_agent_coordinator" || cfg.MySQL.DSN != "" {
		t.Fatalf("mysql config = %+v", cfg.MySQL)
	}
	if cfg.AgentSessionTable != "t_agent_session" {
		t.Fatalf("agent session table = %q", cfg.AgentSessionTable)
	}
	if cfg.CoordinatorPSM != "ad.creative.aic_agent_coordinator" || cfg.CoordinatorCluster != "boe-cluster" || cfg.CoordinatorNamespace != "cloud_agent" {
		t.Fatalf("coordinator config = %+v", cfg)
	}
	if cfg.IDGen.Namespace != "videocut_aigc_agent_coordinator" {
		t.Fatalf("idgen config = %+v", cfg.IDGen)
	}
}
