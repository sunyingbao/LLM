package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/middleware/skill"
	inprocess "eino-cli/deepagent/worker/inprocess"
)

type appDeps struct {
	config  AppConfig
	worker  *inprocess.Worker
	service *LocalAgentService
}

func buildDeps(ctx context.Context, cfg AppConfig) (*appDeps, error) {
	if cfg.UserID == 0 {
		cfg.UserID = defaultUserID
	}
	workDir, err := filepath.Abs(cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workdir failed: %w", err)
	}
	skillsDir, err := resolveSkillsDir(workDir, cfg.SkillsDir)
	if err != nil {
		return nil, fmt.Errorf("resolve skills dir failed: %w", err)
	}
	cfg.WorkDir = workDir
	cfg.SkillsDir = skillsDir
	cfg.DBPath = filepath.Clean(cfg.DBPath)
	cfg.CheckpointDir = filepath.Clean(cfg.CheckpointDir)
	cfg.EnableRG = envBool("DEEPAGENT_ENABLE_RG")

	if err := validateModelEnv(); err != nil {
		return nil, err
	}
	store, err := NewLocalStore(ctx, cfg.DBPath, cfg.CheckpointDir)
	if err != nil {
		return nil, err
	}
	chatModel, err := createChatModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建模型失败: %w", err)
	}

	fsBackend := backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     cfg.WorkDir,
		VirtualMode: false,
	})
	var skillLoader skill.Loader
	if cfg.SkillsDir != "" {
		loader := skill.NewFileSystemSkillLoader([]string{cfg.SkillsDir}, fsBackend, true, nil)
		if _, err := loader.LoadSkills(ctx); err != nil {
			return nil, fmt.Errorf("load skills failed: %w", err)
		}
		skillLoader = loader
	}

	toolPolicy := NewToolExecPolicy()
	refs := NewThreadRefRegistry()
	collabBuilder := &CollabBuilder{
		Refs:             refs,
		RolesDescription: cmdDeepAgentTaskRolesDescription,
	}
	workerDeps := inprocess.Dependencies{
		ThreadStateStore: store.threadState,
		EventStore:       store.events,
	}
	runtimeFactory := &DeepAgentRuntimeFactory{
		cfg:            cfg,
		store:          store,
		chatModel:      chatModel,
		skillLoader:    skillLoader,
		toolExecPolicy: toolPolicy,
		fsBackend:      fsBackend,
		collabBuilder:  collabBuilder,
	}
	workerDeps.ThreadFactory = runtimeFactory.NewThreadRuntime
	worker, err := inprocess.NewWorker(workerDeps)
	if err != nil {
		return nil, err
	}
	service := NewLocalAgentService(ctx, cfg, worker, refs, toolPolicy)

	return &appDeps{
		config:  cfg,
		worker:  worker,
		service: service,
	}, nil
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}

func validateModelEnv() error {
	hasOpenRouter := os.Getenv("OPENROUTER_API_KEY") != ""
	hasKimi := os.Getenv("KIMI_API_KEY") != ""
	hasOpenAI := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) != ""
	hasArk := os.Getenv("ARK_MODEL") != "" && os.Getenv("ARK_API_KEY") != ""
	if hasOpenRouter || hasKimi || hasOpenAI || hasArk {
		return nil
	}
	return fmt.Errorf("missing model env vars: set DEEPSEEK_API_KEY, OPENROUTER_API_KEY, KIMI_API_KEY, or ARK_MODEL+ARK_API_KEY")
}

func resolveSkillsDir(workDir, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if raw == "~" {
			return filepath.Clean(homeDir), nil
		}
		return filepath.Clean(filepath.Join(homeDir, strings.TrimPrefix(raw, "~/"))), nil
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	return filepath.Clean(filepath.Join(workDir, raw)), nil
}
