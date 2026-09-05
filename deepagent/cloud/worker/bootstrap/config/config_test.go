package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cloudbackend "eino-cli/deepagent/cloud/backend"
	"gopkg.in/yaml.v3"
)

func TestLoadUsesLocalProfileByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeConfigFile(t, "conf/worker.local.yml", validYAML("local", "local-dsn"))

	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Namespace != "local" || cfg.MySQL.DSN != "local-dsn" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadUsesExplicitProfileFromArgs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.remote.yml")
	writeConfigFile(t, path, validYAML("remote", "remote-dsn"))

	cfg, err := Load([]string{"-conf", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Namespace != "remote" || cfg.MySQL.DSN != "remote-dsn" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadUsesExplicitProfileFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.remote.yml")
	writeConfigFile(t, path, validYAML("remote-env", "remote-env-dsn"))
	t.Setenv("AGENT_WORKER_CONF", path)

	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Namespace != "remote-env" {
		t.Fatalf("namespace = %q", cfg.Worker.Namespace)
	}
}

func TestLoadRejectsBehaviorFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yml")
	writeConfigFile(t, path, validYAML("yaml", "dsn"))

	_, err := Load([]string{"-conf", path, "-namespace", "flag"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("err = %v, want unsupported flag error", err)
	}
}

func TestLoadRejectsRemovedTCCConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yml")
	writeConfigFile(t, path, validYAML("yaml", "dsn")+`
tcc_runtime_config:
  enabled: true
`)

	_, err := Load([]string{"-conf", path})
	if err == nil || !strings.Contains(err.Error(), "field tcc_runtime_config not found") {
		t.Fatalf("err = %v, want removed TCC field error", err)
	}
}

func TestEnvironmentCannotOverrideYAMLBehavior(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yml")
	writeConfigFile(t, path, validYAML("yaml", "yaml-dsn"))
	t.Setenv("AGENT_WORKER_NAMESPACE", "env")
	t.Setenv("AGENT_WORKER_MYSQL_DSN", "env-dsn")

	cfg, err := Load([]string{"-conf", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Namespace != "yaml" || cfg.MySQL.DSN != "yaml-dsn" {
		t.Fatalf("environment unexpectedly overrode YAML: namespace=%q dsn=%q", cfg.Worker.Namespace, cfg.MySQL.DSN)
	}
}

func TestLoadExpandsOnlyExplicitYAMLReferences(t *testing.T) {
	t.Setenv("MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/db")
	t.Setenv("MODEL_KEY", "model-key")
	t.Setenv("WORKSPACE_ROOT", "/tmp/workspace")
	t.Setenv("MCP_COMMAND", "mcp-server")
	t.Setenv("MCP_TOKEN", "mcp-token")
	t.Setenv("FORNAX_AK", "fornax-ak")
	t.Setenv("FORNAX_SK", "fornax-sk")

	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yml")
	writeConfigFile(t, path, `
worker:
  namespace: explicit-yaml
mysql:
  dsn: ${MYSQL_DSN}
abase:
  addr: 127.0.0.1:6379
checkpoint:
  store: redis://test
models:
  default:
    sdk_type: openai
    model_base_url: https://super-relay.byted.org/v1
    model_api_key: ${MODEL_KEY}
    model_endpoint_id: model_api/experimental_0630
roles:
  main:
    models: [default]
    default_model: default
    approval_policy: normal
runtime:
  workdir: /tmp/work
backend:
  type: local
  local:
    root: ${WORKSPACE_ROOT}
mcp:
  enabled: true
  servers:
    - name: local
      type: stdio
      command: ${MCP_COMMAND}
      env:
        TOKEN: ${MCP_TOKEN}
fornax:
  enabled: true
  ak: ${FORNAX_AK}
  sk: ${FORNAX_SK}
  region: BOE
`)

	cfg, err := Load([]string{"-conf", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MySQL.DSN != "user:pass@tcp(127.0.0.1:3306)/db" || cfg.Backend.Local.Root != "/tmp/workspace" {
		t.Fatalf("expanded infrastructure config = mysql:%q backend:%q", cfg.MySQL.DSN, cfg.Backend.Local.Root)
	}
	if cfg.Models["default"].ModelAPIKey != "model-key" || cfg.MCP.Servers[0].Env["TOKEN"] != "mcp-token" {
		t.Fatalf("expanded runtime config = model:%+v mcp:%+v", cfg.Models["default"], cfg.MCP.Servers[0].Env)
	}
	if cfg.Fornax.AK != "fornax-ak" || cfg.Fornax.SK != "fornax-sk" {
		t.Fatalf("fornax = %+v", cfg.Fornax)
	}
}

func TestLoadFailsWhenReferencedEnvironmentVariableIsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yml")
	writeConfigFile(t, path, strings.Replace(validYAML("local", "dsn"), "dsn: dsn", "dsn: ${MISSING_DSN}", 1))

	_, err := Load([]string{"-conf", path})
	if err == nil || !strings.Contains(err.Error(), "MISSING_DSN") {
		t.Fatalf("err = %v, want missing placeholder name", err)
	}
}

func TestLoadDoesNotExpandPlaceholdersInYAMLComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yml")
	writeConfigFile(t, path, "# ${COMMENT_ONLY}\n"+validYAML("local", "dsn"))
	if _, err := Load([]string{"-conf", path}); err != nil {
		t.Fatalf("comment placeholder should be ignored: %v", err)
	}
}

func TestProfilesUseConfiguredOpenAIModel(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "cmd", "cloud_agent", "conf", "worker.local.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Models["default"].ModelEndpointID; got != "${OPENAI_MODEL}" {
		t.Fatalf("endpoint = %q", got)
	}
	if got := cfg.Models["default"].ModelAPIKey; got != "${OPENAI_API_KEY}" {
		t.Fatalf("api key placeholder = %q", got)
	}
}

func TestTrackedProfilesLoadAndValidate(t *testing.T) {
	t.Setenv("AGENT_WORKER_MYSQL_DSN", "local-dsn")
	t.Setenv("DEEP_AGENT_SDK_WORKSPACE_ROOT", "/tmp/workspace")
	t.Setenv("DEEPSEEK_API_KEY", "local-model-key")
	t.Setenv("OPENAI_API_KEY", "local-model-key")
	t.Setenv("OPENAI_BASE_URL", "https://super-relay.byted.org/v1")
	t.Setenv("OPENAI_MODEL", "model_api/experimental_0630")
	path := filepath.Join("..", "..", "..", "..", "cmd", "cloud_agent", "conf", "worker.local.yml")
	cfg, err := Load([]string{"-conf", path})
	if err != nil {
		t.Fatalf("load worker.local.yml: %v", err)
	}
	if cfg.Models["default"].ModelEndpointID != "model_api/experimental_0630" {
		t.Fatalf("model = %+v", cfg.Models["default"])
	}
}

func TestDefaultRuntimePolicy(t *testing.T) {
	cfg := Default()
	if !cfg.Runtime.DisableApplyPatch || !cfg.Runtime.EnableFollowUpTool {
		t.Fatalf("runtime defaults = %+v", cfg.Runtime)
	}
	if !cfg.Features.ThreadRefs.Enabled || !cfg.Log.EnableAgent {
		t.Fatalf("feature/log defaults = features:%+v log:%+v", cfg.Features, cfg.Log)
	}
	model := cfg.Models["default"]
	if model.SDKType != SDKTypeOpenAI || model.ModelEndpointID != "model_api/experimental_0630" {
		t.Fatalf("default model = %+v", model)
	}
}

func TestLoadNormalizesDurationsAndBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yml")
	writeConfigFile(t, path, `
worker:
  namespace: local
  scan_interval_ms: 250
mysql:
  dsn: dsn
abase:
  addr: 127.0.0.1:6379
checkpoint:
  store: file:///tmp/checkpoints
memory:
  scan_interval_ms: 2000
runtime:
  workdir: /tmp/custom-workdir
backend:
  type: local
  local:
    root: ""
`)

	cfg, err := Load([]string{"-conf", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.ScanInterval != 250*time.Millisecond || cfg.Memory.ScanInterval != 2*time.Second {
		t.Fatalf("durations = worker:%s memory:%s", cfg.Worker.ScanInterval, cfg.Memory.ScanInterval)
	}
	if cfg.Backend.Local.Root != "/tmp/custom-workdir" {
		t.Fatalf("backend root = %q", cfg.Backend.Local.Root)
	}
}

func TestValidateRequiresRedisForHistorySequence(t *testing.T) {
	cfg := validConfig()
	cfg.Abase = AbaseConfig{}
	if err := validate(cfg); err == nil {
		t.Fatal("validate should reject missing Redis/Abase")
	}
}

func TestValidateAllowsRedis(t *testing.T) {
	cfg := validConfig()
	cfg.Abase = AbaseConfig{Addr: "127.0.0.1:6379"}
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Abase.RedisMode() != "redis" {
		t.Fatalf("mode = %q", cfg.Abase.RedisMode())
	}
}

func TestValidateFornax(t *testing.T) {
	cfg := validConfig()
	cfg.Fornax = FornaxConfig{Enabled: true, AK: "ak"}
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "fornax.sk") {
		t.Fatalf("err = %v", err)
	}
	cfg.Fornax.SK = "sk"
	cfg.Fornax.HTTPTimeoutMS = -1
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateMCP(t *testing.T) {
	cfg := validConfig()
	cfg.MCP = MCPConfig{
		Enabled: true,
		Servers: []MCPServerConfig{
			{Name: "http", Type: MCPServerTypeBytedHTTP, PSM: "mcp.psm"},
			{Name: "stdio", Type: MCPServerTypeStdio, Command: "mcp-server"},
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.MCP.Servers[1].Type = MCPServerTypeSSE
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "deprecated") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRuntimeRejectsUnknownRoleModel(t *testing.T) {
	cfg := validConfig()
	role := cfg.Roles["main"]
	role.DefaultModel = "missing"
	cfg.Roles["main"] = role
	if err := ValidateRuntimeConfig(cfg); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v", err)
	}
}

func validConfig() Config {
	cfg := Default()
	cfg.MySQL.DSN = "dsn"
	cfg.Abase = AbaseConfig{Addr: "127.0.0.1:6379"}
	cfg.Checkpoint.Store = "file:///tmp/checkpoints"
	return cfg
}

func validYAML(namespace, dsn string) string {
	return `
worker:
  namespace: ` + namespace + `
mysql:
  dsn: ` + dsn + `
abase:
  addr: 127.0.0.1:6379
checkpoint:
  store: file:///tmp/checkpoints
runtime:
  workdir: /tmp/work
backend:
  type: local
  local:
    root: /tmp/work
`
}

func writeConfigFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStoreConfigConversions(t *testing.T) {
	mysqlCfg := MySQLConfig{DSN: "dsn", ReadTimeoutMS: 1234}.StoreConfig()
	if mysqlCfg.DSN != "dsn" || mysqlCfg.ReadTimeout != 1234*time.Millisecond {
		t.Fatalf("mysql config = %+v", mysqlCfg)
	}
	redisCfg := (AbaseConfig{Addr: "127.0.0.1:6379", ReadTimeoutMS: 100, WriteTimeoutMS: 200}).StoreConfig()
	if redisCfg.ReadTimeout != 100*time.Millisecond || redisCfg.WriteTimeout != 200*time.Millisecond {
		t.Fatalf("redis config = %+v", redisCfg)
	}
}

func TestNormalizeBackendRejectsUnsupportedType(t *testing.T) {
	cfg := validConfig()
	cfg.Backend.Type = cloudbackend.Type("unknown")
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "unsupported backend.type") {
		t.Fatalf("err = %v", err)
	}
}
