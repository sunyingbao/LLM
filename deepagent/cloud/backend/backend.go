//go:build !windows

package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"code.byted.org/gopkg/thrift"
	"code.byted.org/overpass/common/option/clientoption"
	"code.byted.org/overpass/pippit_sandbox_gateway/kitex_gen/capcut/business/common/sandbox_model"
	"code.byted.org/overpass/pippit_sandbox_gateway/rpc/pippit_sandbox_gateway"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/backends/byted_sandbox"
)

const (
	TypeLocal   Type = "local"
	TypeAIInfra Type = "ai_infra"

	defaultLocalRoot              = "./runtime/cloud_agent/workdirs"
	defaultAIInfraBizIDTemplate   = "user_{uid}"
	defaultAIInfraWorkDirTemplate = "/opt/tiger/workspace/{project_name}"
)

var aiInfraPSMState struct {
	sync.Mutex
	initialized bool
	psm         string
}

type Type string

type Config struct {
	Type    Type          `json:"type" yaml:"type"`
	Local   LocalConfig   `json:"local" yaml:"local"`
	AIInfra AIInfraConfig `json:"ai_infra" yaml:"ai_infra"`
}

type LocalConfig struct {
	Root string `json:"root" yaml:"root"`
}

type AIInfraConfig struct {
	PSM             string `json:"psm" yaml:"psm"`
	BizType         string `json:"biz_type" yaml:"biz_type"`
	BizIDTemplate   string `json:"biz_id_template" yaml:"biz_id_template"`
	WorkDirTemplate string `json:"workdir_template" yaml:"workdir_template"`
	Action          string `json:"action" yaml:"action"`
}

type Target struct {
	UID         int64
	SessionID   string
	ProjectName string
	ProjectPath string
}

type Project struct {
	Name string
	Path string
}

type Workspace struct {
	Backend     backends.SandboxBackend
	WorkDir     string
	ProjectPath string
}

func Normalize(cfg Config) Config {
	switch cfg.Type {
	case "", TypeLocal:
		cfg.Type = TypeLocal
		if strings.TrimSpace(cfg.Local.Root) == "" {
			cfg.Local.Root = defaultLocalRoot
		}
	case TypeAIInfra:
		if strings.TrimSpace(cfg.AIInfra.BizIDTemplate) == "" {
			cfg.AIInfra.BizIDTemplate = defaultAIInfraBizIDTemplate
		}
		if strings.TrimSpace(cfg.AIInfra.WorkDirTemplate) == "" {
			cfg.AIInfra.WorkDirTemplate = defaultAIInfraWorkDirTemplate
		}
		if strings.TrimSpace(cfg.AIInfra.Action) == "" {
			cfg.AIInfra.Action = sandbox_model.BIZ_META_ACTION_SANDBOX_TOOL_CALL
		}
	}
	return cfg
}

func ResolveProject(cfg Config, uid int64, projectName string) (Project, error) {
	cfg = Normalize(cfg)
	name, err := CleanProjectName(projectName)
	if err != nil {
		return Project{}, err
	}
	target := Target{UID: uid, ProjectName: name}
	switch cfg.Type {
	case TypeLocal:
		if uid <= 0 {
			return Project{}, fmt.Errorf("uid is required")
		}
		root := strings.TrimSpace(cfg.Local.Root)
		if root == "" {
			return Project{}, fmt.Errorf("local backend root is required")
		}
		return Project{Name: name, Path: filepath.Join(root, strconv.FormatInt(uid, 10), name)}, nil
	case TypeAIInfra:
		workDir := renderTemplate(cfg.AIInfra.WorkDirTemplate, target)
		if strings.TrimSpace(workDir) == "" {
			return Project{}, fmt.Errorf("ai_infra workdir_template rendered empty")
		}
		return Project{Name: name, Path: filepath.Clean(workDir)}, nil
	default:
		return Project{}, fmt.Errorf("unsupported backend type %q", cfg.Type)
	}
}

func Open(ctx context.Context, cfg Config, target Target) (*Workspace, error) {
	cfg = Normalize(cfg)
	switch cfg.Type {
	case TypeLocal:
		return openLocal(cfg.Local, target)
	case TypeAIInfra:
		return openAIInfra(ctx, cfg.AIInfra, target)
	default:
		return nil, fmt.Errorf("unsupported backend type %q", cfg.Type)
	}
}

func ReadFile(ctx context.Context, b backends.Backend, path string) ([]byte, error) {
	type downloader interface {
		DownloadFiles(context.Context, []string) ([]backends.FileDownloadResponse, error)
	}
	if d, ok := b.(downloader); ok {
		responses, err := d.DownloadFiles(ctx, []string{path})
		if err != nil {
			return nil, err
		}
		if len(responses) == 0 {
			return nil, backends.ErrFileNotFound
		}
		if responses[0].Error != "" {
			return nil, responses[0].Error
		}
		return responses[0].Content, nil
	}
	content, err := b.Read(ctx, path, nil, nil)
	if err != nil {
		return nil, err
	}
	return []byte(content), nil
}

func CleanProjectName(projectName string) (string, error) {
	name := strings.TrimSpace(projectName)
	if name == "" {
		return "", fmt.Errorf("project_name is required")
	}
	if len(name) > 128 {
		return "", fmt.Errorf("project_name is too long")
	}
	if name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("project_name must be a single path segment")
	}
	return name, nil
}

func openLocal(cfg LocalConfig, target Target) (*Workspace, error) {
	workDir := strings.TrimSpace(target.ProjectPath)
	if workDir == "" {
		project, err := ResolveProject(Config{Type: TypeLocal, Local: cfg}, target.UID, target.ProjectName)
		if err != nil {
			return nil, err
		}
		workDir = project.Path
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve local workdir %q: %w", workDir, err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("create local workdir %q: %w", abs, err)
	}
	return &Workspace{
		Backend: backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     abs,
			VirtualMode: true,
		}),
		WorkDir:     abs,
		ProjectPath: abs,
	}, nil
}

func openAIInfra(ctx context.Context, cfg AIInfraConfig, target Target) (*Workspace, error) {
	if strings.TrimSpace(cfg.BizType) == "" {
		return nil, fmt.Errorf("ai_infra biz_type is required")
	}
	if err := ensureAIInfraPSM(cfg.PSM); err != nil {
		return nil, err
	}
	safeTarget := target
	if strings.TrimSpace(safeTarget.ProjectName) != "" {
		name, err := CleanProjectName(safeTarget.ProjectName)
		if err != nil {
			return nil, err
		}
		safeTarget.ProjectName = name
	}
	workDir := strings.TrimSpace(target.ProjectPath)
	if workDir == "" {
		name, err := CleanProjectName(safeTarget.ProjectName)
		if err != nil {
			return nil, err
		}
		safeTarget.ProjectName = name
		workDir = renderTemplate(cfg.WorkDirTemplate, safeTarget)
	}
	workDir = filepath.Clean(workDir)
	meta := &sandbox_model.BizMeta{
		BizType: strings.TrimSpace(cfg.BizType),
		BizID:   renderTemplate(cfg.BizIDTemplate, safeTarget),
	}
	if action := strings.TrimSpace(cfg.Action); action != "" {
		meta.Action = thrift.StringPtr(action)
	}
	if safeTarget.UID > 0 {
		meta.UID = thrift.Int64Ptr(safeTarget.UID)
	}
	if strings.TrimSpace(meta.BizID) == "" {
		return nil, fmt.Errorf("ai_infra biz_id_template rendered empty")
	}
	backend := byted_sandbox.NewAIInfraSandbox(meta, workDir)
	if err := backend.EnsureWorkDir(ctx); err != nil {
		return nil, fmt.Errorf("ensure ai_infra workdir %q: %w", workDir, err)
	}
	return &Workspace{
		Backend:     backend,
		WorkDir:     workDir,
		ProjectPath: workDir,
	}, nil
}

func ensureAIInfraPSM(psm string) error {
	psm = strings.TrimSpace(psm)
	if psm == "" {
		return nil
	}
	aiInfraPSMState.Lock()
	defer aiInfraPSMState.Unlock()
	if aiInfraPSMState.initialized {
		if aiInfraPSMState.psm != psm {
			return fmt.Errorf("ai_infra psm already initialized as %q, got %q", aiInfraPSMState.psm, psm)
		}
		return nil
	}
	pippit_sandbox_gateway.InitDefaultClientOptions(clientoption.WithPSM(psm))
	aiInfraPSMState.initialized = true
	aiInfraPSMState.psm = psm
	return nil
}

func renderTemplate(tpl string, target Target) string {
	out := strings.TrimSpace(tpl)
	replacer := strings.NewReplacer(
		"{uid}", strconv.FormatInt(target.UID, 10),
		"{session_id}", strings.TrimSpace(target.SessionID),
		"{project_name}", strings.TrimSpace(target.ProjectName),
		"{project_path}", strings.TrimSpace(target.ProjectPath),
	)
	return replacer.Replace(out)
}
