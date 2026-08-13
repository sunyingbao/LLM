package skill

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/constant"
	"gopkg.in/yaml.v2"
)

func parseMetadata(ctx context.Context, content string, path string) (*SkillMetadata, error) {
	if len(content) > constant.MaxSkillFileSize {
		return nil, fmt.Errorf(constant.ErrMsgSkillFileTooLarge)
	}

	frontmatter, err := extractFrontmatter(content)
	if err != nil {
		logs.CtxError(ctx, "[skill.parseMetadata] failed to extract frontmatter: %v path:%s", err, path)
		return nil, err
	}

	var fm SkillFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		logs.CtxError(ctx, "[skill.parseMetadata] failed to unmarshal frontmatter: %v path:%s", err, path)
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}

	if fm.Name == "" {
		logs.CtxError(ctx, "[skill.parseMetadata] missing name in frontmatter path:%s", path)
		return nil, fmt.Errorf(constant.ErrMsgSkillMissingName)
	}

	directoryName := filepath.Base(filepath.Dir(path))
	if valid, _ := validateSkillName(fm.Name, directoryName); !valid {
	}

	if fm.Description == "" {
		return nil, fmt.Errorf(constant.ErrMsgSkillMissingDescription)
	}

	description := fm.Description
	if len(description) > constant.MaxSkillDescriptionLength {
		description = description[:constant.MaxSkillDescriptionLength]
	}

	return &SkillMetadata{
		Name:          fm.Name,
		Description:   description,
		Path:          path,
		Content:       content,
		License:       fm.License,
		Compatibility: fm.Compatibility,
		AllowedTools:  fm.AllowedTools,
		Triggers:      fm.Triggers,
		Metadata:      fm.Metadata,
		Context:       fm.Context,
		Agent:         fm.Agent,
		Join:          fm.Join,
	}, nil
}

func extractFrontmatter(content string) (string, error) {
	content = stripLineNumbers(content)

	lines := strings.Split(content, "\n")
	var fm []string
	started := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !started {
				started = true
				continue
			}
			return strings.Join(fm, "\n"), nil
		}
		if started {
			fm = append(fm, line)
		}
	}

	if !started {
		return "", fmt.Errorf("no YAML frontmatter found")
	}
	return strings.Join(fm, "\n"), nil
}
