package subagent

import (
	"context"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/components/tool"
	"gopkg.in/yaml.v3"

	"eino-cli/deepagent/core/backends"
)

// SubAgentFileConfig 子代理配置文件结构
type SubAgentFileConfig struct {
	Name             string   `yaml:"name"`
	Description      string   `yaml:"description"`
	SystemPrompt     string   `yaml:"system_prompt"`
	MaxSteps         int      `yaml:"max_steps"`
	Tools            []string `yaml:"tools"`             // 工具名称列表
	EnableFilesystem bool     `yaml:"enable_filesystem"` // 是否启用文件系统工具
	ReadOnly         bool     `yaml:"read_only"`         // 文件系统只读模式
	EnableWeb        bool     `yaml:"enable_web"`        // 是否启用 Web 工具
	EnableSkill      bool     `yaml:"enable_skill"`      // 是否启用 skill 中间件
}

// LoadSubAgentsFromDir 从目录加载子代理配置
// 目录结构:
//
//	subagents/
//	├── researcher/
//	│   └── SUBAGENT.yaml
//	└── code-reviewer/
//	    └── SUBAGENT.yaml
func LoadSubAgentsFromDir(ctx context.Context, dir string, backend backends.Backend, availableTools map[string]tool.BaseTool) ([]*SubAgent, error) {
	entries, err := backend.LsInfo(ctx, dir)
	if err != nil {
		return nil, err
	}

	var agents []*SubAgent
	for _, entry := range entries {
		if !entry.IsDir {
			continue
		}

		// 尝试读取 SUBAGENT.yaml
		configPath := filepath.Join(dir, entry.Name(), "SUBAGENT.yaml")
		data, err := backend.Read(ctx, configPath, nil, nil)
		if err != nil {
			// 尝试 SUBAGENT.yml
			configPath = filepath.Join(dir, entry.Name(), "SUBAGENT.yml")
			data, err = backend.Read(ctx, configPath, nil, nil)
			if err != nil {
				continue
			}
		}

		var cfg SubAgentFileConfig
		if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
			continue
		}

		// 使用目录名作为默认名称
		if cfg.Name == "" {
			cfg.Name = entry.Name()
		}

		// 解析工具引用
		var tools []tool.BaseTool
		if availableTools != nil {
			for _, toolName := range cfg.Tools {
				if t, ok := availableTools[toolName]; ok {
					tools = append(tools, t)
				}
			}
		}

		// 设置默认 MaxSteps
		if cfg.MaxSteps <= 0 {
			cfg.MaxSteps = 10
		}

		sa := &SubAgent{
			Name:             cfg.Name,
			Description:      cfg.Description,
			SystemPrompt:     cfg.SystemPrompt,
			MaxSteps:         cfg.MaxSteps,
			Tools:            tools,
			EnableFilesystem: cfg.EnableFilesystem,
			ReadOnly:         cfg.ReadOnly,
			EnableWeb:        cfg.EnableWeb,
			EnableSkill:      cfg.EnableSkill,
		}
		agents = append(agents, sa)
	}

	return agents, nil
}

// LoadSubAgentFromFile 从单个文件加载子代理配置
func LoadSubAgentFromFile(path string, availableTools map[string]tool.BaseTool) (*SubAgent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg SubAgentFileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// 解析工具引用
	var tools []tool.BaseTool
	if availableTools != nil {
		for _, toolName := range cfg.Tools {
			if t, ok := availableTools[toolName]; ok {
				tools = append(tools, t)
			}
		}
	}

	// 设置默认 MaxSteps
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 10
	}

	sa := &SubAgent{
		Name:             cfg.Name,
		Description:      cfg.Description,
		SystemPrompt:     cfg.SystemPrompt,
		MaxSteps:         cfg.MaxSteps,
		Tools:            tools,
		EnableFilesystem: cfg.EnableFilesystem,
		ReadOnly:         cfg.ReadOnly,
		EnableWeb:        cfg.EnableWeb,
		EnableSkill:      cfg.EnableSkill,
	}
	return sa, nil
}
