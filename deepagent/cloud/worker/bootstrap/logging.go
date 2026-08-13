package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"code.byted.org/gopkg/env"
	"code.byted.org/gopkg/logs/v2"
	"code.byted.org/gopkg/logs/v2/writer"
	"eino-cli/deepagent/cloud/worker/bootstrap/config"
)

const defaultLogRetentionDays = 7

func initLogging(cfg config.Config) error {
	dir := strings.TrimSpace(cfg.Log.Dir)
	if dir == "" {
		return errors.New("log dir is required")
	}

	retentionDays := cfg.Log.RetentionDays
	if retentionDays <= 0 {
		retentionDays = defaultLogRetentionDays
	}
	keepFiles := retentionDays * 24
	if keepFiles <= 0 {
		keepFiles = defaultLogRetentionDays * 24
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	logFile := filepath.Join(dir, "cloud_agent_worker.log")
	fileWriter := writer.NewAsyncWriter(
		writer.NewFileWriter(logFile, writer.Hourly, writer.SetKeepFiles(keepFiles)),
		true,
	)
	writers := []writer.LogWriter{fileWriter}
	if logAgentEnabled(cfg) {
		writers = append(writers, writer.NewAgentWriter())
	}
	if cfg.Log.EnableConsole {
		writers = append(writers, writer.NewConsoleWriter(writer.SetColorful(false)))
	}

	logs.SetDefaultLogger(
		logs.SetWriter(logs.InfoLevel, writers...),
		logs.SetCallDepth(2),
	)
	return nil
}

func logAgentEnabled(cfg config.Config) bool {
	return logAgentEnabledForEnv(cfg, env.InTCE())
}

func logWriterSummary(cfg config.Config) string {
	return logWriterSummaryForEnv(cfg, env.InTCE())
}

func logAgentEnabledForEnv(cfg config.Config, inTCE bool) bool {
	return cfg.Log.EnableAgent && inTCE
}

func logWriterSummaryForEnv(cfg config.Config, inTCE bool) string {
	names := []string{"file"}
	if logAgentEnabledForEnv(cfg, inTCE) {
		names = append(names, "agent")
	}
	if cfg.Log.EnableConsole {
		names = append(names, "console")
	}
	return strings.Join(names, ",")
}
