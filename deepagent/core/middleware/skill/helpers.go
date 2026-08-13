package skill

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"eino-cli/deepagent/core/constant"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validateSkillName(name, directoryName string) (bool, string) {
	if name == "" {
		return false, constant.ErrMsgSkillMissingName
	}
	if len(name) > constant.MaxSkillNameLength {
		return false, constant.ErrMsgSkillNameTooLong
	}
	if !skillNamePattern.MatchString(name) {
		return false, constant.ErrMsgSkillNameInvalid
	}
	if name != directoryName {
		return false, fmt.Sprintf("%s: '%s' != '%s'", constant.ErrMsgSkillNameMismatch, name, directoryName)
	}
	return true, ""
}

func cloneSkills(skills []*SkillMetadata) []*SkillMetadata {
	if len(skills) == 0 {
		return nil
	}

	out := make([]*SkillMetadata, 0, len(skills))
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		copied := *skill
		if skill.Metadata != nil {
			copied.Metadata = make(map[string]string, len(skill.Metadata))
			for k, v := range skill.Metadata {
				copied.Metadata[k] = v
			}
		}
		out = append(out, &copied)
	}
	return out
}

func findSkillByName(skills []*SkillMetadata, name string) *SkillMetadata {
	for _, skill := range skills {
		if skill != nil && skill.Name == name {
			return skill
		}
	}
	return nil
}

func sortSkills(skills map[string]*SkillMetadata) []*SkillMetadata {
	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]*SkillMetadata, 0, len(names))
	for _, name := range names {
		out = append(out, skills[name])
	}
	return out
}

func pruneActiveSkills(active map[string]bool, current []*SkillMetadata) {
	for name := range active {
		if findSkillByName(current, name) == nil {
			delete(active, name)
		}
	}
}

func skillDir(path string) string {
	return filepath.Dir(path)
}

func stripLineNumbers(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "|"); idx > 0 && idx < 10 {
			lines[i] = line[idx+1:]
		}
	}
	return strings.Join(lines, "\n")
}
