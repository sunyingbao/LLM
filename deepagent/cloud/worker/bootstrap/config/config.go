package config

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	cloudbackend "eino-cli/deepagent/cloud/backend"
	cloudworker "eino-cli/deepagent/cloud/worker"
	mysqlstore "eino-cli/deepagent/cloud/worker/bootstrap/internal/mysql"
	redisstore "eino-cli/deepagent/cloud/worker/bootstrap/internal/redis"
	"eino-cli/deepagent/cloud/worker/bootstrap/internal/threadrefs"
	"eino-cli/deepagent/core/memory"
	gormstore "eino-cli/deepagent/core/memory/gorm_store"
	"gopkg.in/yaml.v3"
)

const (
	fallbackFilename         = "worker.local.yml"
	defaultConcurrency       = 8
	defaultLogRetentionDays  = 7
	defaultWorkDirRoot       = "./runtime/cloud_agent/workdirs"
	defaultCheckpointStore   = "file://./runtime/cloud_agent/checkpoints"
	defaultBasePrompt        = "conf/prompt/base.md"
	defaultModelID           = "default"
	defaultRoleID            = "main"
	defaultMySQLReadTimeout  = 5 * time.Second
	defaultRedisReadTimeout  = 500 * time.Millisecond
	defaultRedisWriteTimeout = 500 * time.Millisecond
	defaultMCPRequestTimeout = 5 * time.Minute
)

type Config struct {
	BasePrompt string                   `json:"base_prompt" yaml:"base_prompt"`
	Worker     WorkerConfig             `json:"worker" yaml:"worker"`
	MySQL      MySQLConfig              `json:"mysql" yaml:"mysql"`
	Abase      AbaseConfig              `json:"abase" yaml:"abase"`
	IDGen      IDGenConfig              `json:"idgen" yaml:"idgen"`
	Features   FeaturesConfig           `json:"features" yaml:"features"`
	Tables     TablesConfig             `json:"tables" yaml:"tables"`
	Checkpoint CheckpointConfig         `json:"checkpoint" yaml:"checkpoint"`
	Memory     MemoryConfig             `json:"memory" yaml:"memory"`
	Models     map[string]ModelConfig   `json:"models" yaml:"models"`
	Roles      map[string]RoleConfig    `json:"roles" yaml:"roles"`
	Runtime    RuntimeConfig            `json:"runtime" yaml:"runtime"`
	Backend    cloudbackend.Config      `json:"backend" yaml:"backend"`
	Output     cloudworker.OutputConfig `json:"output" yaml:"output"`
	MCP        MCPConfig                `json:"mcp" yaml:"mcp"`
	Fornax     FornaxConfig             `json:"fornax" yaml:"fornax"`
	Log        LogConfig                `json:"log" yaml:"log"`
}

type WorkerConfig struct {
	Namespace                     string        `json:"namespace" yaml:"namespace"`
	Concurrency                   int           `json:"concurrency" yaml:"concurrency"`
	ScanLimit                     int32         `json:"scan_limit" yaml:"scan_limit"`
	MessageLimit                  int32         `json:"message_limit" yaml:"message_limit"`
	LeaseMS                       int64         `json:"lease_ms" yaml:"lease_ms"`
	ScanInterval                  time.Duration `json:"-" yaml:"-"`
	MessagePollInterval           time.Duration `json:"-" yaml:"-"`
	IdleTimeout                   time.Duration `json:"-" yaml:"-"`
	ShutdownDrainTimeout          time.Duration `json:"-" yaml:"-"`
	ShutdownInterruptDrainTimeout time.Duration `json:"-" yaml:"-"`
	InterruptDrainTimeout         time.Duration `json:"-" yaml:"-"`

	ScanIntervalMS                  int `json:"scan_interval_ms" yaml:"scan_interval_ms"`
	MessagePollIntervalMS           int `json:"message_poll_interval_ms" yaml:"message_poll_interval_ms"`
	IdleTimeoutMS                   int `json:"idle_timeout_ms" yaml:"idle_timeout_ms"`
	ShutdownDrainTimeoutMS          int `json:"shutdown_drain_timeout_ms" yaml:"shutdown_drain_timeout_ms"`
	ShutdownInterruptDrainTimeoutMS int `json:"shutdown_interrupt_drain_timeout_ms" yaml:"shutdown_interrupt_drain_timeout_ms"`
	InterruptDrainTimeoutMS         int `json:"interrupt_drain_timeout_ms" yaml:"interrupt_drain_timeout_ms"`
}

type MySQLConfig struct {
	DSN           string `json:"dsn,omitempty" yaml:"dsn,omitempty"`
	ReadDSN       string `json:"read_dsn,omitempty" yaml:"read_dsn,omitempty"`
	ReadTimeoutMS int    `json:"read_timeout_ms,omitempty" yaml:"read_timeout_ms,omitempty"`
}

type AbaseConfig struct {
	Addr           string `json:"addr,omitempty" yaml:"addr,omitempty"`
	Password       string `json:"password,omitempty" yaml:"password,omitempty"`
	DB             int    `json:"db,omitempty" yaml:"db,omitempty"`
	ReadTimeoutMS  int    `json:"read_timeout_ms,omitempty" yaml:"read_timeout_ms,omitempty"`
	WriteTimeoutMS int    `json:"write_timeout_ms,omitempty" yaml:"write_timeout_ms,omitempty"`
}

type IDGenConfig struct {
	Namespace string `json:"namespace" yaml:"namespace"`
}

type FeaturesConfig struct {
	ThreadRefs ThreadRefsConfig `json:"thread_refs" yaml:"thread_refs"`
}

type ThreadRefsConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

type TablesConfig struct {
	History            string `json:"history" yaml:"history"`
	ThreadRef          string `json:"thread_ref" yaml:"thread_ref"`
	MemorySource       string `json:"memory_source" yaml:"memory_source"`
	MemoryStage1Output string `json:"memory_stage1_output" yaml:"memory_stage1_output"`
	MemoryStage2Job    string `json:"memory_stage2_job" yaml:"memory_stage2_job"`
	MemoryBaseline     string `json:"memory_baseline" yaml:"memory_baseline"`
}

type CheckpointConfig struct {
	Store string `json:"store" yaml:"store"`
}

type MemoryConfig struct {
	Enabled                  bool                            `json:"enabled" yaml:"enabled"`
	WorkspaceRoot            string                          `json:"workspace_root" yaml:"workspace_root"`
	ScanIntervalMS           int                             `json:"scan_interval_ms" yaml:"scan_interval_ms"`
	WakeupDebounceMS         int                             `json:"wakeup_debounce_ms" yaml:"wakeup_debounce_ms"`
	Stage1IdleWindowMS       int                             `json:"stage1_idle_window_ms" yaml:"stage1_idle_window_ms"`
	Stage1LeaseTTLMS         int                             `json:"stage1_lease_ttl_ms" yaml:"stage1_lease_ttl_ms"`
	Stage1MaxClaimedPerScan  int                             `json:"stage1_max_claimed_per_scan" yaml:"stage1_max_claimed_per_scan"`
	Stage1HistoryInput       memory.Stage1HistoryInputConfig `json:"stage1_history_input" yaml:"stage1_history_input"`
	Stage2LeaseTTLMS         int                             `json:"stage2_lease_ttl_ms" yaml:"stage2_lease_ttl_ms"`
	Stage2SuccessCooldownMS  int                             `json:"stage2_success_cooldown_ms" yaml:"stage2_success_cooldown_ms"`
	Stage2ScanIntervalMS     int                             `json:"stage2_scan_interval_ms" yaml:"stage2_scan_interval_ms"`
	Stage2MaxUsersPerScan    int                             `json:"stage2_max_users_per_scan" yaml:"stage2_max_users_per_scan"`
	Stage2OutputLimitPerUser int                             `json:"stage2_output_limit_per_user" yaml:"stage2_output_limit_per_user"`

	ScanInterval          time.Duration `json:"-" yaml:"-"`
	WakeupDebounce        time.Duration `json:"-" yaml:"-"`
	Stage1IdleWindow      time.Duration `json:"-" yaml:"-"`
	Stage1LeaseTTL        time.Duration `json:"-" yaml:"-"`
	Stage2LeaseTTL        time.Duration `json:"-" yaml:"-"`
	Stage2SuccessCooldown time.Duration `json:"-" yaml:"-"`
	Stage2ScanInterval    time.Duration `json:"-" yaml:"-"`
}

type RuntimeConfig struct {
	WorkDir                  string `json:"workdir" yaml:"workdir"`
	SkillsDir                string `json:"skills_dir" yaml:"skills_dir"`
	SpawnMetadataDescription string `json:"spawn_metadata_description" yaml:"spawn_metadata_description"`
	AutoCompactLimitTokens   int    `json:"auto_compact_limit_tokens" yaml:"auto_compact_limit_tokens"`
	CompactKeptUserTokens    int    `json:"compact_kept_user_tokens" yaml:"compact_kept_user_tokens"`
	CompactPromptAppend      string `json:"compact_prompt_append" yaml:"compact_prompt_append"`
	MaxSteps                 int    `json:"max_steps" yaml:"max_steps"`
	MaxModelCalls            int    `json:"max_model_calls" yaml:"max_model_calls"`
	DisableApplyPatch        bool   `json:"disable_apply_patch" yaml:"disable_apply_patch"`
	EnableFollowUpTool       bool   `json:"enable_follow_up_tool" yaml:"enable_follow_up_tool"`
}

type MCPConfig struct {
	Enabled          bool              `json:"enabled" yaml:"enabled"`
	RequestTimeoutMS int               `json:"request_timeout_ms" yaml:"request_timeout_ms"`
	Region           string            `json:"region" yaml:"region"`
	Servers          []MCPServerConfig `json:"servers" yaml:"servers"`
}

type MCPServerConfig struct {
	Name             string            `json:"name" yaml:"name"`
	Type             MCPServerType     `json:"type" yaml:"type"`
	PSM              string            `json:"psm" yaml:"psm"`
	PPEEnv           string            `json:"ppe_env" yaml:"ppe_env"`
	Region           string            `json:"region" yaml:"region"`
	RequestTimeoutMS int               `json:"request_timeout_ms" yaml:"request_timeout_ms"`
	Tools            []string          `json:"tools" yaml:"tools"`
	Headers          map[string]string `json:"headers" yaml:"headers"`
	Params           map[string]any    `json:"params" yaml:"params"`
	Trace            bool              `json:"trace" yaml:"trace"`

	Command string            `json:"command" yaml:"command"`
	Args    []string          `json:"args" yaml:"args"`
	Env     map[string]string `json:"env" yaml:"env"`
}

type MCPServerType string

const (
	MCPServerTypeBytedHTTP MCPServerType = "byted_http"
	MCPServerTypeStdio     MCPServerType = "stdio"
	MCPServerTypeSSE       MCPServerType = "sse"
)

type FornaxConfig struct {
	Enabled       bool   `json:"enabled" yaml:"enabled"`
	AK            string `json:"ak" yaml:"ak"`
	SK            string `json:"sk" yaml:"sk"`
	Region        string `json:"region" yaml:"region"`
	HTTPTimeoutMS int    `json:"http_timeout_ms" yaml:"http_timeout_ms"`
}

type LogConfig struct {
	Dir           string `json:"dir" yaml:"dir"`
	RetentionDays int    `json:"retention_days" yaml:"retention_days"`
	EnableAgent   bool   `json:"enable_agent" yaml:"enable_agent"`
	EnableConsole bool   `json:"enable_console" yaml:"enable_console"`
}

func Load(args []string) (Config, error) {
	confPath, err := configPath(args)
	if err != nil {
		return Config{}, err
	}

	cfg := Default()
	if err := loadYAML(confPath, &cfg); err != nil {
		return Config{}, err
	}
	normalizeProfiles(&cfg)
	normalizeBackend(&cfg)
	normalizeMCP(&cfg)
	normalizeFornax(&cfg)
	normalizeDurations(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Default() Config {
	cfg := Config{
		BasePrompt: defaultBasePrompt,
		Worker: WorkerConfig{
			Namespace:                       "cloud_agent",
			Concurrency:                     defaultConcurrency,
			ScanLimit:                       20,
			MessageLimit:                    50,
			LeaseMS:                         60000,
			ScanIntervalMS:                  1000,
			MessagePollIntervalMS:           500,
			IdleTimeoutMS:                   10000,
			ShutdownDrainTimeoutMS:          120000,
			ShutdownInterruptDrainTimeoutMS: 120000,
			InterruptDrainTimeoutMS:         30000,
		},
		Tables: TablesConfig{
			History:            "t_agentthread_history",
			ThreadRef:          threadrefs.DefaultTableName,
			MemorySource:       gormstore.DefaultSourceTable,
			MemoryStage1Output: gormstore.DefaultStage1OutputTable,
			MemoryStage2Job:    gormstore.DefaultStage2JobTable,
			MemoryBaseline:     gormstore.DefaultBaselineTable,
		},
		Features: FeaturesConfig{
			ThreadRefs: ThreadRefsConfig{Enabled: true},
		},
		Checkpoint: CheckpointConfig{
			Store: defaultCheckpointStore,
		},
		Memory: MemoryConfig{
			WorkspaceRoot:            "./runtime/cloud_agent/memory",
			ScanIntervalMS:           60000,
			WakeupDebounceMS:         10000,
			Stage1IdleWindowMS:       10 * 60 * 1000,
			Stage1LeaseTTLMS:         10 * 60 * 1000,
			Stage1MaxClaimedPerScan:  2,
			Stage2LeaseTTLMS:         60 * 60 * 1000,
			Stage2SuccessCooldownMS:  6 * 60 * 60 * 1000,
			Stage2ScanIntervalMS:     5 * 60 * 1000,
			Stage2MaxUsersPerScan:    2,
			Stage2OutputLimitPerUser: 100,
		},
		Models: map[string]ModelConfig{
			defaultModelID: {
				SDKType:         SDKTypeOpenAI,
				ModelName:       "experimental_0630",
				ModelBaseURL:    "https://super-relay.byted.org/v1",
				ModelEndpointID: "model_api/experimental_0630",
				MaxTokens:       32768,
				DisableByAzure:  true,
			},
		},
		Runtime: RuntimeConfig{
			WorkDir:               defaultWorkDirRoot,
			CompactKeptUserTokens: 4000,
			DisableApplyPatch:     true,
			EnableFollowUpTool:    true,
		},
		Backend: cloudbackend.Config{
			Type:  cloudbackend.TypeLocal,
			Local: cloudbackend.LocalConfig{Root: defaultWorkDirRoot},
		},
		MCP: MCPConfig{
			RequestTimeoutMS: int(defaultMCPRequestTimeout / time.Millisecond),
		},
		Log: LogConfig{
			Dir:           "./runtime/cloud_agent/logs",
			RetentionDays: defaultLogRetentionDays,
			EnableAgent:   true,
		},
	}
	normalizeProfiles(&cfg)
	normalizeBackend(&cfg)
	normalizeMCP(&cfg)
	normalizeFornax(&cfg)
	normalizeDurations(&cfg)
	return cfg
}

func loadYAML(path string, cfg *Config) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(buf, &document); err != nil {
		return fmt.Errorf("unmarshal config %s: %w", path, err)
	}
	if err := expandYAMLNodeEnv(&document); err != nil {
		return fmt.Errorf("expand config %s: %w", path, err)
	}
	expanded, err := yaml.Marshal(&document)
	if err != nil {
		return fmt.Errorf("encode expanded config %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(expanded))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return fmt.Errorf("decode config %s: %w", path, err)
	}
	return nil
}

func defaultLocalFilename() string {
	paths := []string{
		filepath.Join("conf", fallbackFilename),
		filepath.Join("deepagent", "cmd", "cloud_agent", "conf", fallbackFilename),
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return paths[0]
}

var envReferencePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandYAMLNodeEnv(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		expanded, err := expandEnvReferences(node.Value)
		if err != nil {
			return err
		}
		node.Value = expanded
	}
	for _, child := range node.Content {
		if err := expandYAMLNodeEnv(child); err != nil {
			return err
		}
	}
	return nil
}

func expandEnvReferences(value string) (string, error) {
	var missing string
	expanded := envReferencePattern.ReplaceAllStringFunc(value, func(reference string) string {
		name := reference[2 : len(reference)-1]
		value, ok := os.LookupEnv(name)
		if !ok && missing == "" {
			missing = name
		}
		return value
	})
	if missing != "" {
		return "", fmt.Errorf("environment variable %s referenced by YAML is not set", missing)
	}
	return expanded, nil
}

func normalizeProfiles(cfg *Config) {
	if strings.TrimSpace(cfg.BasePrompt) == "" {
		cfg.BasePrompt = defaultBasePrompt
	}
	for id, modelCfg := range cfg.Models {
		modelCfg.ModelName = strings.TrimSpace(modelCfg.ModelName)
		if modelCfg.ModelName == "" {
			modelCfg.ModelName = strings.TrimSpace(id)
		}
		modelCfg.ModelBaseURL = strings.TrimSpace(modelCfg.ModelBaseURL)
		modelCfg.ModelAPIKey = strings.TrimSpace(modelCfg.ModelAPIKey)
		modelCfg.ModelEndpointID = strings.TrimSpace(modelCfg.ModelEndpointID)
		modelCfg.APIVersion = strings.TrimSpace(modelCfg.APIVersion)
		modelCfg.LogID = strings.TrimSpace(modelCfg.LogID)
		cfg.Models[id] = modelCfg
	}
	if len(cfg.Roles) == 0 {
		cfg.Roles = defaultRoles()
	}
	for id, roleCfg := range cfg.Roles {
		roleCfg.Prompt = strings.TrimSpace(roleCfg.Prompt)
		roleCfg.DefaultModel = strings.TrimSpace(roleCfg.DefaultModel)
		if roleCfg.DefaultModel == "" {
			roleCfg.DefaultModel = defaultModelID
		}
		if len(roleCfg.Models) == 0 {
			roleCfg.Models = []string{roleCfg.DefaultModel}
		}
		for i, modelID := range roleCfg.Models {
			roleCfg.Models[i] = strings.TrimSpace(modelID)
		}
		if roleCfg.ApprovalPolicy == "" {
			roleCfg.ApprovalPolicy = ApprovalPolicyNormal
		}
		cfg.Roles[id] = roleCfg
	}
}

func normalizeBackend(cfg *Config) {
	if strings.TrimSpace(string(cfg.Backend.Type)) == "" {
		cfg.Backend.Type = cloudbackend.TypeLocal
	}
	if cfg.Backend.Type == cloudbackend.TypeLocal &&
		(strings.TrimSpace(cfg.Backend.Local.Root) == "" || strings.TrimSpace(cfg.Backend.Local.Root) == defaultWorkDirRoot) {
		cfg.Backend.Local.Root = cfg.Runtime.WorkDir
	}
	cfg.Backend = cloudbackend.Normalize(cfg.Backend)
}

func normalizeMCP(cfg *Config) {
	cfg.MCP.Region = strings.TrimSpace(cfg.MCP.Region)
	if cfg.MCP.RequestTimeoutMS == 0 {
		cfg.MCP.RequestTimeoutMS = int(defaultMCPRequestTimeout / time.Millisecond)
	}
	for i, server := range cfg.MCP.Servers {
		server.Name = strings.TrimSpace(server.Name)
		server.Type = MCPServerType(strings.ToLower(strings.TrimSpace(string(server.Type))))
		server.PSM = strings.TrimSpace(server.PSM)
		server.PPEEnv = strings.TrimSpace(server.PPEEnv)
		server.Region = strings.TrimSpace(server.Region)
		server.Command = strings.TrimSpace(server.Command)
		for j, name := range server.Tools {
			server.Tools[j] = strings.TrimSpace(name)
		}
		cfg.MCP.Servers[i] = server
	}
}

func normalizeFornax(cfg *Config) {
	cfg.Fornax.AK = strings.TrimSpace(cfg.Fornax.AK)
	cfg.Fornax.SK = strings.TrimSpace(cfg.Fornax.SK)
	cfg.Fornax.Region = strings.TrimSpace(cfg.Fornax.Region)
}

func defaultRoles() map[string]RoleConfig {
	return map[string]RoleConfig{
		defaultRoleID: {
			Models:         []string{defaultModelID},
			DefaultModel:   defaultModelID,
			ApprovalPolicy: ApprovalPolicyNormal,
		},
		"explorer": {
			Models:         []string{defaultModelID},
			DefaultModel:   defaultModelID,
			ApprovalPolicy: ApprovalPolicyReadOnly,
		},
		"worker": {
			Models:         []string{defaultModelID},
			DefaultModel:   defaultModelID,
			ApprovalPolicy: ApprovalPolicyPermissive,
		},
	}
}

func normalizeDurations(cfg *Config) {
	if cfg.Worker.ScanIntervalMS > 0 {
		cfg.Worker.ScanInterval = time.Duration(cfg.Worker.ScanIntervalMS) * time.Millisecond
	}
	if cfg.Worker.MessagePollIntervalMS > 0 {
		cfg.Worker.MessagePollInterval = time.Duration(cfg.Worker.MessagePollIntervalMS) * time.Millisecond
	}
	if cfg.Worker.IdleTimeoutMS > 0 {
		cfg.Worker.IdleTimeout = time.Duration(cfg.Worker.IdleTimeoutMS) * time.Millisecond
	}
	if cfg.Worker.ShutdownDrainTimeoutMS > 0 {
		cfg.Worker.ShutdownDrainTimeout = time.Duration(cfg.Worker.ShutdownDrainTimeoutMS) * time.Millisecond
	}
	if cfg.Worker.ShutdownInterruptDrainTimeoutMS > 0 {
		cfg.Worker.ShutdownInterruptDrainTimeout = time.Duration(cfg.Worker.ShutdownInterruptDrainTimeoutMS) * time.Millisecond
	}
	if cfg.Worker.InterruptDrainTimeoutMS > 0 {
		cfg.Worker.InterruptDrainTimeout = time.Duration(cfg.Worker.InterruptDrainTimeoutMS) * time.Millisecond
	}
	if cfg.Memory.ScanIntervalMS > 0 {
		cfg.Memory.ScanInterval = time.Duration(cfg.Memory.ScanIntervalMS) * time.Millisecond
	}
	if cfg.Memory.WakeupDebounceMS > 0 {
		cfg.Memory.WakeupDebounce = time.Duration(cfg.Memory.WakeupDebounceMS) * time.Millisecond
	}
	if cfg.Memory.Stage1IdleWindowMS > 0 {
		cfg.Memory.Stage1IdleWindow = time.Duration(cfg.Memory.Stage1IdleWindowMS) * time.Millisecond
	}
	if cfg.Memory.Stage1LeaseTTLMS > 0 {
		cfg.Memory.Stage1LeaseTTL = time.Duration(cfg.Memory.Stage1LeaseTTLMS) * time.Millisecond
	}
	if cfg.Memory.Stage2LeaseTTLMS > 0 {
		cfg.Memory.Stage2LeaseTTL = time.Duration(cfg.Memory.Stage2LeaseTTLMS) * time.Millisecond
	}
	if cfg.Memory.Stage2SuccessCooldownMS > 0 {
		cfg.Memory.Stage2SuccessCooldown = time.Duration(cfg.Memory.Stage2SuccessCooldownMS) * time.Millisecond
	}
	if cfg.Memory.Stage2ScanIntervalMS > 0 {
		cfg.Memory.Stage2ScanInterval = time.Duration(cfg.Memory.Stage2ScanIntervalMS) * time.Millisecond
	}
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.Worker.Namespace) == "" {
		return fmt.Errorf("worker.namespace is required")
	}
	if strings.TrimSpace(cfg.MySQL.DSN) == "" {
		return fmt.Errorf("mysql.dsn is required")
	}
	if strings.TrimSpace(cfg.Runtime.WorkDir) == "" {
		return fmt.Errorf("runtime.workdir is required")
	}
	switch cfg.Backend.Type {
	case cloudbackend.TypeLocal:
		if strings.TrimSpace(cfg.Backend.Local.Root) == "" {
			return fmt.Errorf("backend.local.root is required")
		}
	case cloudbackend.TypeAIInfra:
		if strings.TrimSpace(cfg.Backend.AIInfra.BizType) == "" {
			return fmt.Errorf("backend.ai_infra.biz_type is required")
		}
	default:
		return fmt.Errorf("unsupported backend.type %q", cfg.Backend.Type)
	}
	if strings.TrimSpace(cfg.Checkpoint.Store) == "" {
		return fmt.Errorf("checkpoint.store is required")
	}
	if !cfg.Abase.Configured() {
		return fmt.Errorf("abase.addr is required")
	}
	if cfg.Memory.Enabled && strings.TrimSpace(cfg.Memory.WorkspaceRoot) == "" {
		return fmt.Errorf("memory.workspace_root is required when memory.enabled is true")
	}
	if err := ValidateRuntimeConfig(cfg); err != nil {
		return err
	}
	if err := validateMCP(cfg.MCP); err != nil {
		return err
	}
	return nil
}

func ValidateRuntimeConfig(cfg Config) error {
	if len(cfg.Models) == 0 {
		return fmt.Errorf("models is required")
	}
	for modelID, modelCfg := range cfg.Models {
		if strings.TrimSpace(modelID) == "" {
			return fmt.Errorf("model id is required")
		}
		if strings.TrimSpace(string(modelCfg.SDKType)) == "" {
			return fmt.Errorf("models.%s.sdk_type is required", modelID)
		}
	}
	if len(cfg.Roles) == 0 {
		return fmt.Errorf("roles is required")
	}
	for roleID, roleCfg := range cfg.Roles {
		if strings.TrimSpace(roleID) == "" {
			return fmt.Errorf("role id is required")
		}
		if strings.TrimSpace(roleCfg.DefaultModel) == "" {
			return fmt.Errorf("roles.%s.default_model is required", roleID)
		}
		if _, ok := cfg.Models[roleCfg.DefaultModel]; !ok {
			return fmt.Errorf("roles.%s.default_model %q is not configured", roleID, roleCfg.DefaultModel)
		}
		for _, modelID := range roleCfg.Models {
			if strings.TrimSpace(modelID) == "" {
				return fmt.Errorf("roles.%s.models contains empty model id", roleID)
			}
			if _, ok := cfg.Models[modelID]; !ok {
				return fmt.Errorf("roles.%s.models references unknown model %q", roleID, modelID)
			}
		}
		switch roleCfg.ApprovalPolicy {
		case ApprovalPolicyNormal, ApprovalPolicyReadOnly, ApprovalPolicyPermissive:
		default:
			return fmt.Errorf("roles.%s.approval_policy %q is unsupported", roleID, roleCfg.ApprovalPolicy)
		}
	}
	if cfg.Runtime.MaxSteps < 0 {
		return fmt.Errorf("runtime.max_steps must be >= 0")
	}
	if cfg.Runtime.MaxModelCalls < 0 {
		return fmt.Errorf("runtime.max_model_calls must be >= 0")
	}
	if err := validateFornax(cfg.Fornax); err != nil {
		return err
	}
	return nil
}

func validateFornax(cfg FornaxConfig) error {
	if cfg.HTTPTimeoutMS < 0 {
		return fmt.Errorf("fornax.http_timeout_ms must be non-negative")
	}
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.AK) == "" {
		return fmt.Errorf("fornax.ak is required when fornax.enabled is true")
	}
	if strings.TrimSpace(cfg.SK) == "" {
		return fmt.Errorf("fornax.sk is required when fornax.enabled is true")
	}
	return nil
}

func validateMCP(cfg MCPConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if !validMCPRegion(cfg.Region) {
		return fmt.Errorf("mcp.region %q is unsupported", cfg.Region)
	}
	if len(cfg.Servers) == 0 {
		return fmt.Errorf("mcp.servers is required when mcp.enabled is true")
	}
	seen := map[string]struct{}{}
	for i, server := range cfg.Servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			return fmt.Errorf("mcp.servers[%d].name is required", i)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("mcp.servers[%d].name %q is duplicated", i, name)
		}
		seen[name] = struct{}{}
		if !validMCPRegion(server.Region) {
			return fmt.Errorf("mcp.servers[%d].region %q is unsupported", i, server.Region)
		}
		switch server.Type {
		case MCPServerTypeBytedHTTP:
			if strings.TrimSpace(server.PSM) == "" {
				return fmt.Errorf("mcp.servers[%d].psm is required for byted_http server %q", i, name)
			}
		case MCPServerTypeStdio:
			if strings.TrimSpace(server.Command) == "" {
				return fmt.Errorf("mcp.servers[%d].command is required for stdio server %q", i, name)
			}
		case MCPServerTypeSSE:
			return fmt.Errorf("mcp.servers[%d].type sse is deprecated; use byted_http or stdio", i)
		default:
			return fmt.Errorf("mcp.servers[%d].type %q is unsupported", i, server.Type)
		}
		for j, toolName := range server.Tools {
			if strings.TrimSpace(toolName) == "" {
				return fmt.Errorf("mcp.servers[%d].tools[%d] is empty", i, j)
			}
		}
	}
	return nil
}

func validMCPRegion(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "infer", "cn", "china", "boe", "china_boe", "i18n", "i18n_boe", "i18n-boe", "usttp", "euttp", "i18n_bd", "i18n-bd", "sandbox":
		return true
	default:
		return false
	}
}

func (c MySQLConfig) StoreConfig() mysqlstore.Config {
	timeout := defaultMySQLReadTimeout
	if c.ReadTimeoutMS > 0 {
		timeout = time.Duration(c.ReadTimeoutMS) * time.Millisecond
	}
	return mysqlstore.Config{
		DSN:         c.DSN,
		ReadDSN:     c.ReadDSN,
		ReadTimeout: timeout,
	}
}

func (c AbaseConfig) StoreConfig() redisstore.Config {
	readTimeout := defaultRedisReadTimeout
	if c.ReadTimeoutMS > 0 {
		readTimeout = time.Duration(c.ReadTimeoutMS) * time.Millisecond
	}
	writeTimeout := defaultRedisWriteTimeout
	if c.WriteTimeoutMS > 0 {
		writeTimeout = time.Duration(c.WriteTimeoutMS) * time.Millisecond
	}
	return redisstore.Config{
		Addr:         c.Addr,
		Password:     c.Password,
		DB:           c.DB,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}

func (c AbaseConfig) Configured() bool {
	return strings.TrimSpace(c.Addr) != ""
}

func (c AbaseConfig) RedisMode() string {
	return "redis"
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func configPath(args []string) (string, error) {
	path := envString("AGENT_WORKER_CONF", defaultLocalFilename())
	fs := flag.NewFlagSet("deep_agent_sdk_worker", flag.ContinueOnError)
	fs.StringVar(&path, "conf", path, "DeepAgent worker YAML config file")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("worker config file is required")
	}
	return path, nil
}
