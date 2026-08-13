package skill

import (
	"context"
)

type SkillTrigger struct {
	Pattern     string `yaml:"pattern" json:"pattern"`
	Description string `yaml:"description" json:"description"`
}

type SkillFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	AllowedTools  []string          `yaml:"allowed-tools,omitempty"`
	Triggers      []SkillTrigger    `yaml:"triggers,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	Context       string            `yaml:"context,omitempty"`
	Agent         string            `yaml:"agent,omitempty"`
	Join          string            `yaml:"join,omitempty"`
}

type SkillMetadata struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Path          string            `json:"path"`
	Content       string            `json:"content,omitempty"`
	License       string            `json:"license,omitempty"`
	Compatibility string            `json:"compatibility,omitempty"`
	AllowedTools  []string          `json:"allowed_tools,omitempty"`
	Triggers      []SkillTrigger    `json:"triggers,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Context       string            `json:"context,omitempty"`
	Agent         string            `json:"agent,omitempty"`
	Join          string            `json:"join,omitempty"`
}

// Mask 决定 skill 是否对外可见。
// 返回 true 表示保留该 skill，返回 false 表示将其从 LoadSkills / ListSkills 的结果中过滤掉。
// 注意：mask 只影响对外暴露，不影响 loader 内部的全量加载与缓存。
type Mask func(ctx context.Context, skill *SkillMetadata) bool

type Loader interface {
	ListSkills(ctx context.Context) ([]*SkillMetadata, error)
}
