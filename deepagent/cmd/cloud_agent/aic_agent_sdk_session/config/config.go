package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"code.byted.org/gdp/env"
	"gopkg.in/yaml.v3"
)

const (
	defaultMySQLDSN = "ac_test:ac_test_pwd_20260416@tcp(127.0.0.1:3306)/agent_coordinator_test?charset=utf8mb4&parseTime=True&loc=Local"
	defaultCluster  = "default"
)

var (
	currentCluster = env.Cluster
	currentVRegion = env.GetCurrentVRegion
)

type Config struct {
	MySQL                MySQLConfig
	IDGen                IDGenConfig
	MySQLDSN             string
	AgentSessionTable    string
	CoordinatorPSM       string
	CoordinatorCluster   string
	CoordinatorHostports string
	CoordinatorNamespace string
	DisableAC            bool
}

type MySQLConfig struct {
	PSM           string `json:"psm" yaml:"psm"`
	DBName        string `json:"db_name" yaml:"db_name"`
	DSN           string `json:"dsn" yaml:"dsn"`
	ReadDSN       string `json:"read_dsn" yaml:"read_dsn"`
	ReadTimeoutMS int    `json:"read_timeout_ms" yaml:"read_timeout_ms"`
}

type IDGenConfig struct {
	Namespace string `json:"namespace" yaml:"namespace"`
}

func Load() (Config, error) {
	cfg := Default()
	if path := configPath(); path != "" {
		loaded, err := LoadFromPath(path)
		if err != nil {
			return Config{}, err
		}
		cfg = loaded
	}
	applyEnv(&cfg)
	return cfg, nil
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
	cfg := Config{
		MySQL: MySQLConfig{
			DSN: defaultMySQLDSN,
		},
		AgentSessionTable:    "t_agent_session",
		CoordinatorPSM:       "ad.creative.aic_agent_coordinator",
		CoordinatorNamespace: "cloud_agent",
	}
	cfg.MySQLDSN = cfg.MySQL.DSN
	return cfg
}

func LoadFromEnv() Config {
	cfg := Default()
	applyEnv(&cfg)
	return cfg
}

type fileConfig struct {
	MySQL  MySQLConfig `json:"mysql" yaml:"mysql"`
	Tables struct {
		AgentSession string `json:"agent_session" yaml:"agent_session"`
	} `json:"tables" yaml:"tables"`
	Coordinator struct {
		PSM             string `json:"psm" yaml:"psm"`
		Cluster         string `json:"cluster" yaml:"cluster"`
		DirectHostPorts string `json:"direct_hostports" yaml:"direct_hostports"`
		Namespace       string `json:"namespace" yaml:"namespace"`
		Disabled        bool   `json:"disabled" yaml:"disabled"`
	} `json:"coordinator" yaml:"coordinator"`
	IDGen IDGenConfig `json:"idgen" yaml:"idgen"`
}

func (raw fileConfig) applyTo(cfg *Config) {
	if strings.TrimSpace(raw.MySQL.PSM) != "" || strings.TrimSpace(raw.MySQL.DBName) != "" || strings.TrimSpace(raw.MySQL.DSN) != "" {
		cfg.MySQL = raw.MySQL
		cfg.MySQLDSN = raw.MySQL.DSN
	}
	if value := strings.TrimSpace(raw.Tables.AgentSession); value != "" {
		cfg.AgentSessionTable = value
	}
	if value := strings.TrimSpace(raw.Coordinator.PSM); value != "" {
		cfg.CoordinatorPSM = value
	}
	cfg.CoordinatorCluster = strings.TrimSpace(raw.Coordinator.Cluster)
	cfg.CoordinatorHostports = strings.TrimSpace(raw.Coordinator.DirectHostPorts)
	if value := strings.TrimSpace(raw.Coordinator.Namespace); value != "" {
		cfg.CoordinatorNamespace = value
	}
	cfg.DisableAC = raw.Coordinator.Disabled
	cfg.IDGen = raw.IDGen
}

func applyEnv(cfg *Config) {
	cfg.MySQL.DSN = envString("AIC_AGENT_SDK_SESSION_MYSQL_DSN", envString("AGENT_WORKER_MYSQL_DSN", cfg.MySQL.DSN))
	cfg.MySQL.PSM = envString("AIC_AGENT_SDK_SESSION_MYSQL_PSM", envString("AGENT_WORKER_MYSQL_PSM", cfg.MySQL.PSM))
	cfg.MySQL.DBName = envString("AIC_AGENT_SDK_SESSION_MYSQL_DB_NAME", envString("AGENT_WORKER_MYSQL_DB_NAME", cfg.MySQL.DBName))
	cfg.MySQL.ReadDSN = envString("AIC_AGENT_SDK_SESSION_MYSQL_READ_DSN", envString("AGENT_WORKER_MYSQL_READ_DSN", cfg.MySQL.ReadDSN))
	cfg.MySQL.ReadTimeoutMS = envInt("AIC_AGENT_SDK_SESSION_MYSQL_READ_TIMEOUT_MS", envInt("AGENT_WORKER_MYSQL_READ_TIMEOUT_MS", cfg.MySQL.ReadTimeoutMS))
	cfg.MySQLDSN = cfg.MySQL.DSN
	cfg.AgentSessionTable = envString("AIC_AGENT_SDK_SESSION_AGENT_SESSION_TABLE", cfg.AgentSessionTable)
	cfg.CoordinatorPSM = envString("AIC_AGENT_SDK_SESSION_AC_PSM", envString("AGENT_COORDINATOR_PSM", cfg.CoordinatorPSM))
	cfg.CoordinatorCluster = envString("AIC_AGENT_SDK_SESSION_AC_CLUSTER", envString("AGENT_COORDINATOR_CLUSTER", cfg.CoordinatorCluster))
	cfg.CoordinatorHostports = envString("AIC_AGENT_SDK_SESSION_AC_HOSTPORTS", envString("AGENT_COORDINATOR_HOSTPORTS", cfg.CoordinatorHostports))
	cfg.CoordinatorNamespace = envString("AIC_AGENT_SDK_SESSION_AC_NAMESPACE", envString("AGENT_WORKER_NAMESPACE", cfg.CoordinatorNamespace))
	cfg.IDGen.Namespace = envString("AIC_AGENT_SDK_SESSION_IDGEN_NAMESPACE", envString("AGENT_WORKER_IDGEN_NAMESPACE", cfg.IDGen.Namespace))
	cfg.DisableAC = envBool("AIC_AGENT_SDK_SESSION_DISABLE_AC", cfg.DisableAC)
}

func configPath() string {
	if path := strings.TrimSpace(os.Getenv("AIC_AGENT_SDK_SESSION_CONF")); path != "" {
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

func ParseHostPorts(raw string) []string {
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

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
