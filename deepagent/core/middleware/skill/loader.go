package skill

import (
	"context"
	"path/filepath"
	"sync"

	"code.byted.org/gopkg/logs/v2"
	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
)

type FileSystemSkillLoader struct {
	backend     backends.Backend
	sources     []string
	enableCache bool
	mask        Mask

	once      sync.Once
	allSkills []*SkillMetadata
	err       error
}

func NewFileSystemSkillLoader(sources []string, backend backends.Backend, enableCache bool, mask Mask) *FileSystemSkillLoader {
	return &FileSystemSkillLoader{
		backend:     backend,
		sources:     append([]string(nil), sources...),
		enableCache: enableCache,
		mask:        mask,
	}
}

func (l *FileSystemSkillLoader) LoadSkills(ctx context.Context) ([]*SkillMetadata, error) {
	var allSkills []*SkillMetadata
	if l.enableCache {
		l.once.Do(func() {
			l.allSkills, l.err = l.loadAll(ctx)
		})
		if l.err != nil {
			return nil, l.err
		}
		allSkills = l.allSkills
	} else {
		var err error
		allSkills, err = l.loadAll(ctx)
		if err != nil {
			return nil, err
		}
	}

	return applySkillMask(ctx, allSkills, l.mask), nil
}

func (l *FileSystemSkillLoader) ListSkills(ctx context.Context) ([]*SkillMetadata, error) {
	return l.LoadSkills(ctx)
}

func (l *FileSystemSkillLoader) loadAll(ctx context.Context) ([]*SkillMetadata, error) {
	skills := make(map[string]*SkillMetadata)
	for _, source := range l.sources {
		sourceSkills, err := l.listSkillsFromSource(ctx, source)
		if err != nil {
			logs.CtxError(ctx, "[skill.backendLoader.ListSkills] failed to load source %s: %v", source, err)
			continue
		}
		for _, skill := range sourceSkills {
			skills[skill.Name] = skill
		}
	}
	return sortSkills(skills), nil
}

func (l *FileSystemSkillLoader) listSkillsFromSource(ctx context.Context, source string) ([]*SkillMetadata, error) {
	files, err := l.backend.LsInfo(ctx, source)
	if err != nil {
		logs.CtxError(ctx, "[skill.backendLoader.listSkillsFromSource] failed to list directory: %v path:%s", err, source)
		return nil, err
	}

	var skills []*SkillMetadata
	for _, f := range files {
		if !f.IsDir {
			logs.CtxInfo(ctx, "[skill.backendLoader.listSkillsFromSource] skip non-directory file: %s path:%s", f.Path, source)
			continue
		}

		skillPath := filepath.Join(f.Path, constant.SkillMetadataFile)
		content, err := l.backend.Read(ctx, skillPath, nil, nil)
		if err != nil {
			logs.CtxError(ctx, "[skill.backendLoader.listSkillsFromSource] failed to read skill file: %v path:%s", err, skillPath)
			continue
		}

		skill, err := parseMetadata(ctx, content, skillPath)
		if err != nil {
			logs.CtxError(ctx, "[skill.backendLoader.listSkillsFromSource] failed to parse skill metadata: %v path:%s", err, skillPath)
			continue
		}

		logs.CtxInfo(ctx, "[skill.backendLoader.listSkillsFromSource] successfully loaded skill: %s path:%s", skill.Name, skillPath)
		skills = append(skills, skill)
	}

	return skills, nil
}

func applySkillMask(ctx context.Context, skills []*SkillMetadata, mask Mask) []*SkillMetadata {
	if len(skills) == 0 {
		return nil
	}
	if mask == nil {
		return cloneSkills(skills)
	}

	filtered := make([]*SkillMetadata, 0, len(skills))
	for _, skill := range skills {
		if skill == nil || !mask(ctx, skill) {
			continue
		}
		copied := *skill
		filtered = append(filtered, &copied)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}
