package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"code.byted.org/gdp/env"
	cloudbackend "eino-cli/deepagent/cloud/backend"
	"gopkg.in/yaml.v3"
)

const (
	AgentCoordinatorPSM   = "ad.creative.aic_agent_coordinator"
	AICAgentSDKSessionPSM = "ad.creative.aic_agent_sdk_session"
)

const defaultWorkspaceRoot = "/home/wudi.hust/deepagent_workspace"
const defaultCluster = "default"

var (
	currentCluster = env.Cluster
	currentVRegion = env.GetCurrentVRegion
)

type Config struct {
	AgentCoordinatorPSM           string
	AICAgentSDKSessionPSM         string
	ACNamespace                   string
	ACCluster                     string
	ACDirectHostPorts             []string
	AICAgentSDKSessionCluster     string
	AICAgentSDKSessionDirectHosts []string
	WorkspaceRoot                 string
	Backend                       cloudbackend.Config
	LocalDefaultUID               int64
	UseLocalDefaultUIDOnAuthErr   bool
	TimelineDefaultLimit          int32
	TimelineMaxLimit              int32
}

func Load() Config {
	cfg := Default()
	if path := configPath(); path != "" {
		loaded, err := LoadFromPath(path)
		if err != nil {
			panic(err)
		}
		cfg = loaded
	}
	applyEnv(&cfg)
	return cfg
}

func LoadFromPath(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var raw fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(buf))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("unmarshal config %s: %w", path, err)
	}
	raw.applyTo(&cfg)
	return cfg, nil
}

func Default() Config {
	authMode := "online"
	workspaceRoot := defaultWorkspaceRoot
	return Config{
		AgentCoordinatorPSM:         AgentCoordinatorPSM,
		AICAgentSDKSessionPSM:       AICAgentSDKSessionPSM,
		ACNamespace:                 "cloud_agent",
		WorkspaceRoot:               workspaceRoot,
		Backend:                     defaultBackendConfig(workspaceRoot),
		UseLocalDefaultUIDOnAuthErr: authMode == "local",
		TimelineDefaultLimit:        50,
		TimelineMaxLimit:            200,
	}
}

type fileConfig struct {
	Auth struct {
		Mode string `yaml:"mode"`
	} `yaml:"auth"`
	Coordinator struct {
		Namespace       string `yaml:"namespace"`
		PSM             string `yaml:"psm"`
		Cluster         string `yaml:"cluster"`
		DirectHostPorts string `yaml:"direct_hostports"`
	} `yaml:"coordinator"`
	AICAgentSDKSession struct {
		PSM             string `yaml:"psm"`
		Cluster         string `yaml:"cluster"`
		DirectHostPorts string `yaml:"direct_hostports"`
	} `yaml:"aic_agent_sdk_session"`
	Workspace struct {
		Root string `yaml:"root"`
	} `yaml:"workspace"`
	Backend  cloudbackend.Config `yaml:"backend"`
	Timeline struct {
		DefaultLimit int32 `yaml:"default_limit"`
		MaxLimit     int32 `yaml:"max_limit"`
	} `yaml:"timeline"`
}

func (raw fileConfig) applyTo(cfg *Config) {
	if value := strings.TrimSpace(raw.Auth.Mode); value != "" {
		cfg.UseLocalDefaultUIDOnAuthErr = value == "local"
	}
	if value := strings.TrimSpace(raw.Coordinator.Namespace); value != "" {
		cfg.ACNamespace = value
	}
	if value := strings.TrimSpace(raw.Coordinator.PSM); value != "" {
		cfg.AgentCoordinatorPSM = value
	}
	cfg.ACCluster = strings.TrimSpace(raw.Coordinator.Cluster)
	cfg.ACDirectHostPorts = splitHostPorts(raw.Coordinator.DirectHostPorts)
	if value := strings.TrimSpace(raw.AICAgentSDKSession.PSM); value != "" {
		cfg.AICAgentSDKSessionPSM = value
	}
	cfg.AICAgentSDKSessionCluster = strings.TrimSpace(raw.AICAgentSDKSession.Cluster)
	cfg.AICAgentSDKSessionDirectHosts = splitHostPorts(raw.AICAgentSDKSession.DirectHostPorts)
	if value := strings.TrimSpace(raw.Workspace.Root); value != "" {
		cfg.WorkspaceRoot = value
	}
	if strings.TrimSpace(string(raw.Backend.Type)) != "" {
		cfg.Backend = cloudbackend.Normalize(raw.Backend)
	} else {
		cfg.Backend = defaultBackendConfig(cfg.WorkspaceRoot)
	}
	if raw.Timeline.DefaultLimit > 0 {
		cfg.TimelineDefaultLimit = raw.Timeline.DefaultLimit
	}
	if raw.Timeline.MaxLimit > 0 {
		cfg.TimelineMaxLimit = raw.Timeline.MaxLimit
	}
}

func applyEnv(cfg *Config) {
	authMode := envString("AIC_AGENT_SDK_API_AUTH_MODE", "")
	if authMode != "" {
		cfg.UseLocalDefaultUIDOnAuthErr = authMode == "local"
	}
	cfg.AgentCoordinatorPSM = envString("AIC_AGENT_SDK_API_AC_PSM", cfg.AgentCoordinatorPSM)
	cfg.AICAgentSDKSessionPSM = envString("AIC_AGENT_SDK_API_SESSION_PSM", cfg.AICAgentSDKSessionPSM)
	cfg.ACNamespace = envString("AIC_AGENT_SDK_API_AC_NAMESPACE", envString("AGENT_WORKER_NAMESPACE", cfg.ACNamespace))
	cfg.ACCluster = envString("AIC_AGENT_SDK_API_AC_CLUSTER", cfg.ACCluster)
	cfg.ACDirectHostPorts = splitHostPorts(envString("AIC_AGENT_SDK_API_AC_DIRECT_HOSTPORTS", envString("AGENT_COORDINATOR_HOSTPORTS", strings.Join(cfg.ACDirectHostPorts, ","))))
	cfg.AICAgentSDKSessionCluster = envString("AIC_AGENT_SDK_API_SESSION_CLUSTER", cfg.AICAgentSDKSessionCluster)
	cfg.AICAgentSDKSessionDirectHosts = splitHostPorts(envString("AIC_AGENT_SDK_API_SESSION_DIRECT_HOSTPORTS", strings.Join(cfg.AICAgentSDKSessionDirectHosts, ",")))
	cfg.WorkspaceRoot = envString("AIC_AGENT_SDK_API_WORKSPACE_ROOT", cfg.WorkspaceRoot)
	cfg.Backend = loadBackendConfig(cfg.WorkspaceRoot, cfg.Backend)
	cfg.LocalDefaultUID = envInt64("AIC_AGENT_SDK_API_LOCAL_DEFAULT_UID", cfg.LocalDefaultUID)
	cfg.TimelineDefaultLimit = int32(envInt("AIC_AGENT_SDK_API_TIMELINE_DEFAULT_LIMIT", int(cfg.TimelineDefaultLimit)))
	cfg.TimelineMaxLimit = int32(envInt("AIC_AGENT_SDK_API_TIMELINE_MAX_LIMIT", int(cfg.TimelineMaxLimit)))
}

func (c Config) NormalizeLimit(limit *int32) int32 {
	if limit == nil || *limit <= 0 {
		return c.TimelineDefaultLimit
	}
	if c.TimelineMaxLimit > 0 && *limit > c.TimelineMaxLimit {
		return c.TimelineMaxLimit
	}
	return *limit
}

func loadBackendConfig(localRoot string, base cloudbackend.Config) cloudbackend.Config {
	cfg := base
	cfg.Type = cloudbackend.Type(envString("AIC_AGENT_SDK_API_BACKEND_TYPE", envString("AIC_AGENT_SDK_BACKEND_TYPE", string(cfg.Type))))
	cfg.Local.Root = envString("AIC_AGENT_SDK_API_BACKEND_LOCAL_ROOT", envString("AIC_AGENT_SDK_BACKEND_LOCAL_ROOT", cfg.Local.Root))
	if strings.TrimSpace(cfg.Local.Root) == "" {
		cfg.Local.Root = localRoot
	}
	cfg.AIInfra.PSM = envString("AIC_AGENT_SDK_API_AI_INFRA_PSM", envString("AIC_AGENT_SDK_BACKEND_AI_INFRA_PSM", cfg.AIInfra.PSM))
	cfg.AIInfra.BizType = envString("AIC_AGENT_SDK_API_AI_INFRA_BIZ_TYPE", envString("AIC_AGENT_SDK_BACKEND_AI_INFRA_BIZ_TYPE", cfg.AIInfra.BizType))
	cfg.AIInfra.BizIDTemplate = envString("AIC_AGENT_SDK_API_AI_INFRA_BIZ_ID_TEMPLATE", envString("AIC_AGENT_SDK_BACKEND_AI_INFRA_BIZ_ID_TEMPLATE", cfg.AIInfra.BizIDTemplate))
	cfg.AIInfra.WorkDirTemplate = envString("AIC_AGENT_SDK_API_AI_INFRA_WORKDIR_TEMPLATE", envString("AIC_AGENT_SDK_BACKEND_AI_INFRA_WORKDIR_TEMPLATE", cfg.AIInfra.WorkDirTemplate))
	cfg.AIInfra.Action = envString("AIC_AGENT_SDK_API_AI_INFRA_ACTION", envString("AIC_AGENT_SDK_BACKEND_AI_INFRA_ACTION", cfg.AIInfra.Action))
	return cloudbackend.Normalize(cfg)
}

func defaultBackendConfig(localRoot string) cloudbackend.Config {
	return cloudbackend.Normalize(cloudbackend.Config{
		Type:  cloudbackend.TypeLocal,
		Local: cloudbackend.LocalConfig{Root: localRoot},
		AIInfra: cloudbackend.AIInfraConfig{
			BizIDTemplate:   "user_{uid}",
			WorkDirTemplate: "/opt/tiger/workspace/{project_name}",
		},
	})
}

func configPath() string {
	if path := strings.TrimSpace(os.Getenv("AIC_AGENT_SDK_API_CONF")); path != "" {
		return path
	}
	return defaultLocalFilename()
}

func defaultLocalFilename() string {
	region := normalizeConfigSuffix(currentVRegion())
	cluster := normalizeConfigSuffix(currentCluster())
	if region != "" && cluster != "" && cluster != defaultCluster {
		path := filepath.Join("conf", fmt.Sprintf("conf_%s_%s.yml", region, cluster))
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if region != "" {
		path := filepath.Join("conf", fmt.Sprintf("conf_%s.yml", region))
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	path := filepath.Join("conf", "conf.yml")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func normalizeConfigSuffix(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func envString(key string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func splitHostPorts(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
