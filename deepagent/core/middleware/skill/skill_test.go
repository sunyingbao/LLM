package skill

import (
	"context"
	"strings"
	"sync"
	"testing"

	"eino-cli/deepagent/core/backends"
	"eino-cli/deepagent/core/constant"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type fakeLoader struct {
	skills    [][]*SkillMetadata
	listCalls int
}

func (l *fakeLoader) ListSkills(ctx context.Context) ([]*SkillMetadata, error) {
	idx := l.listCalls
	l.listCalls++
	if idx >= len(l.skills) {
		idx = len(l.skills) - 1
	}
	if idx < 0 {
		return nil, nil
	}
	return cloneSkills(l.skills[idx]), nil
}

type countingBackend struct {
	mu        sync.Mutex
	backend   backends.Backend
	lsCalls   int
	readCalls int
}

func (b *countingBackend) LsInfo(ctx context.Context, path string) ([]backends.FileInfo, error) {
	b.mu.Lock()
	b.lsCalls++
	b.mu.Unlock()
	return b.backend.LsInfo(ctx, path)
}

func (b *countingBackend) Read(ctx context.Context, path string, offset, limit *int) (string, error) {
	b.mu.Lock()
	b.readCalls++
	b.mu.Unlock()
	return b.backend.Read(ctx, path, offset, limit)
}

func (b *countingBackend) Write(ctx context.Context, path string, content string) (*backends.WriteResult, error) {
	return b.backend.Write(ctx, path, content)
}

func (b *countingBackend) Edit(ctx context.Context, path string, oldString, newString string, replaceAll bool) (*backends.EditResult, error) {
	return b.backend.Edit(ctx, path, oldString, newString, replaceAll)
}

func (b *countingBackend) GrepRaw(ctx context.Context, pattern string, path string, glob string) ([]backends.GrepMatch, error) {
	return b.backend.GrepRaw(ctx, pattern, path, glob)
}

func (b *countingBackend) GlobInfo(ctx context.Context, pattern string, path string) ([]backends.FileInfo, error) {
	return b.backend.GlobInfo(ctx, pattern, path)
}

func (b *countingBackend) UploadFiles(ctx context.Context, files []struct {
	Path    string
	Content []byte
}) ([]backends.FileUploadResponse, error) {
	return b.backend.UploadFiles(ctx, files)
}

func (b *countingBackend) ChangeDir(ctx context.Context, path string) error {
	return b.backend.ChangeDir(ctx, path)
}

func (b *countingBackend) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lsCalls, b.readCalls
}

func newSkillTestBackend(t *testing.T) backends.Backend {
	t.Helper()
	return backends.NewFilesystemBackend(&backends.FilesystemBackendConfig{
		RootDir:     t.TempDir(),
		VirtualMode: true,
	})
}

func newCountingSkillBackend(t *testing.T) *countingBackend {
	t.Helper()

	ctx := context.Background()
	backend := newSkillTestBackend(t)
	_, _ = backend.Write(ctx, "/skills/example-skill/SKILL.md", `---
name: example-skill
description: cached
---
# Example
`)
	return &countingBackend{backend: backend}
}

func invokeTool(t *testing.T, base tool.BaseTool, payload string) string {
	t.Helper()
	invokable, ok := base.(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool %T is not invokable", base)
	}
	got, err := invokable.InvokableRun(context.Background(), payload)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	return got
}

func TestParseMetadataReadsAgent(t *testing.T) {
	content := `---
name: example-skill
description: Example skill
context: fork
agent: explorer
join: no
---
# Example
`

	skill, err := parseMetadata(context.Background(), content, "/tmp/example-skill/SKILL.md")
	if err != nil {
		t.Fatalf("parseMetadata() error = %v", err)
	}

	if skill.Agent != "explorer" {
		t.Fatalf("skill.Agent = %q, want explorer", skill.Agent)
	}
	if skill.Context != "fork" {
		t.Fatalf("skill.Context = %q, want fork", skill.Context)
	}
	if skill.Join != "no" {
		t.Fatalf("skill.Join = %q, want no", skill.Join)
	}
}

func TestBackendLoaderListSkillsUsesPriorityAndLoadsContent(t *testing.T) {
	ctx := context.Background()
	backend := newSkillTestBackend(t)
	_, _ = backend.Write(ctx, "/skills-a/example-skill/SKILL.md", `---
name: example-skill
description: low priority
---
# Low
`)
	_, _ = backend.Write(ctx, "/skills-b/example-skill/SKILL.md", `---
name: example-skill
description: high priority
context: fork
agent: explorer
---
# High
`)
	_, _ = backend.Write(ctx, "/skills-b/other-skill/SKILL.md", `---
name: other-skill
description: other
---
# Other
`)

	loader := NewFileSystemSkillLoader([]string{"/skills-a", "/skills-b"}, backend, true, nil)
	skills, err := loader.LoadSkills(ctx)

	if err != nil {
		t.Fatalf("ListSkills() error = %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d, want 2", len(skills))
	}
	if skills[0].Name != "example-skill" || skills[0].Description != "high priority" {
		t.Fatalf("skills[0] = %+v, want high priority example-skill", skills[0])
	}

	if !strings.Contains(skills[0].Content, "# High") {
		t.Fatalf("skills[0].Content = %q, want full markdown content", skills[0].Content)
	}
}

func TestFileSystemSkillLoaderListSkillsUsesSharedCache(t *testing.T) {
	backend := newCountingSkillBackend(t)
	loader := NewFileSystemSkillLoader([]string{"/skills"}, backend, true, nil)

	first, err := loader.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("first ListSkills() error = %v", err)
	}
	first[0].Description = "mutated-by-caller"

	second, err := loader.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("second ListSkills() error = %v", err)
	}

	lsCalls, readCalls := backend.counts()
	if lsCalls != 1 || readCalls != 1 {
		t.Fatalf("backend calls = (%d ls, %d read), want (1, 1)", lsCalls, readCalls)
	}
	if second[0].Description != "cached" {
		t.Fatalf("second ListSkills() description = %q, want cached content", second[0].Description)
	}
}

func TestFileSystemSkillLoaderListSkillsConcurrentLoadsOnce(t *testing.T) {
	backend := newCountingSkillBackend(t)
	loader := NewFileSystemSkillLoader([]string{"/skills"}, backend, true, nil)

	const callers = 8
	var wg sync.WaitGroup
	errCh := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			skills, err := loader.ListSkills(context.Background())
			if err != nil {
				errCh <- err
				return
			}
			if len(skills) != 1 || skills[0].Name != "example-skill" {
				errCh <- backends.ErrSandboxFsFailed
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("cached loader returned error under concurrency: %v", err)
	}

	lsCalls, readCalls := backend.counts()
	if lsCalls != 1 || readCalls != 1 {
		t.Fatalf("backend calls = (%d ls, %d read), want (1, 1)", lsCalls, readCalls)
	}
}

func TestFileSystemSkillLoaderListSkillsReloadsWhenCacheDisabled(t *testing.T) {
	backend := newCountingSkillBackend(t)
	loader := NewFileSystemSkillLoader([]string{"/skills"}, backend, false, nil)

	first, err := loader.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("first ListSkills() error = %v", err)
	}
	first[0].Description = "mutated-by-caller"

	second, err := loader.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("second ListSkills() error = %v", err)
	}

	lsCalls, readCalls := backend.counts()
	if lsCalls != 2 || readCalls != 2 {
		t.Fatalf("backend calls = (%d ls, %d read), want (2, 2)", lsCalls, readCalls)
	}
	if second[0].Description != "cached" {
		t.Fatalf("second ListSkills() description = %q, want fresh content", second[0].Description)
	}
}

func TestFileSystemSkillLoaderLoadSkillsWarmsCache(t *testing.T) {
	backend := newCountingSkillBackend(t)
	loader := NewFileSystemSkillLoader([]string{"/skills"}, backend, true, nil)

	if _, err := loader.LoadSkills(context.Background()); err != nil {
		t.Fatalf("LoadSkills() error = %v", err)
	}
	if _, err := loader.ListSkills(context.Background()); err != nil {
		t.Fatalf("ListSkills() error = %v", err)
	}

	lsCalls, readCalls := backend.counts()
	if lsCalls != 1 || readCalls != 1 {
		t.Fatalf("backend calls = (%d ls, %d read), want (1, 1)", lsCalls, readCalls)
	}
}

func TestFileSystemSkillLoaderAppliesMaskPerCallContext(t *testing.T) {
	ctx := context.Background()
	backend := &countingBackend{backend: newSkillTestBackend(t)}
	_, _ = backend.Write(ctx, "/skills/public-skill/SKILL.md", `---
name: public-skill
description: public
---
# Public
`)
	_, _ = backend.Write(ctx, "/skills/admin-skill/SKILL.md", `---
name: admin-skill
description: admin
---
# Admin
`)

	type visibilityKey struct{}
	loader := NewFileSystemSkillLoader([]string{"/skills"}, backend, true, func(ctx context.Context, skill *SkillMetadata) bool {
		visible, _ := ctx.Value(visibilityKey{}).(map[string]bool)
		return visible[skill.Name]
	})

	publicOnly, err := loader.ListSkills(context.WithValue(ctx, visibilityKey{}, map[string]bool{
		"public-skill": true,
	}))
	if err != nil {
		t.Fatalf("public-only ListSkills() error = %v", err)
	}
	if len(publicOnly) != 1 || publicOnly[0].Name != "public-skill" {
		t.Fatalf("public-only skills = %+v, want only public-skill", publicOnly)
	}

	allVisible, err := loader.ListSkills(context.WithValue(ctx, visibilityKey{}, map[string]bool{
		"public-skill": true,
		"admin-skill":  true,
	}))
	if err != nil {
		t.Fatalf("all-visible ListSkills() error = %v", err)
	}
	if len(allVisible) != 2 {
		t.Fatalf("len(allVisible) = %d, want 2", len(allVisible))
	}

	lsCalls, readCalls := backend.counts()
	if lsCalls != 1 || readCalls != 2 {
		t.Fatalf("backend calls = (%d ls, %d read), want (1, 2)", lsCalls, readCalls)
	}
}

func TestMiddlewareFormatSkillsListAddsForkPromptWithConfiguredAgent(t *testing.T) {
	m := New(nil)
	m.skills = []*SkillMetadata{{
		Name:        "example-skill",
		Description: "Example skill",
		Path:        "/tmp/example-skill/SKILL.md",
		Context:     "fork",
		Agent:       "explorer",
		Join:        " no ",
	}}

	got := m.formatSkillsList()

	wantParts := []string{
		"- **example-skill**: Example skill",
		"  -> Path: `/tmp/example-skill/SKILL.md`",
		"  -> Execution: This skill should be executed in a child agent via the `task` tool. The tool's `fork_context` param should be set to true.",
		"  -> Join behavior: Set the tool's `wait_for_done` param to false so the main agent does not wait for the child agent to finish.",
		"  -> Delegation rule: When you decide to use this skill, delegate the work to subagent `explorer`. Only skip delegation if the user explicitly tells you not to delegate.",
		"  -> Child task requirements: Explicitly require the child agent to use skill `example-skill`.",
		"  -> Recursion guard: The delegated task must explicitly instruct the child agent not to delegate this same skill `example-skill` again.",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("formatSkillsList() missing %q in output:\n%s", part, got)
		}
	}
}

func TestMiddlewareFormatSkillsListForkFallsBackToExecutor(t *testing.T) {
	m := New(nil)
	m.skills = []*SkillMetadata{{
		Name:        "example-skill",
		Description: "Example skill",
		Path:        "/tmp/example-skill/SKILL.md",
		Context:     "fork",
	}}

	got := m.formatSkillsList()
	want := "delegate the work to subagent `" + constant.ExecutorName + "`"
	if !strings.Contains(got, want) {
		t.Fatalf("formatSkillsList() = %q, want substring %q", got, want)
	}
	if !strings.Contains(got, "Keep the tool's `wait_for_done` param omitted or set to true") {
		t.Fatalf("formatSkillsList() should describe default wait behavior, got %q", got)
	}
	if strings.Contains(got, "`wait_for_done` param to false") {
		t.Fatalf("formatSkillsList() unexpectedly requested wait_for_done=false, got %q", got)
	}
}

func TestMiddlewareFormatSkillsListWithoutForkKeepsOriginalShape(t *testing.T) {
	m := New(nil)
	m.skills = []*SkillMetadata{{
		Name:         "example-skill",
		Description:  "Example skill",
		Path:         "/tmp/example-skill/SKILL.md",
		AllowedTools: []string{"read_file"},
		Context:      "inline",
		Agent:        "explorer",
	}}

	got := m.formatSkillsList()
	if strings.Contains(got, "Execution: This skill should be executed in a child agent via the `task` tool.") {
		t.Fatalf("formatSkillsList() unexpectedly added fork prompt:\n%s", got)
	}
	if !strings.Contains(got, "  -> Allowed tools: read_file") {
		t.Fatalf("formatSkillsList() missing allowed tools line:\n%s", got)
	}
}

func TestMiddlewareActivateSkillUsesEmbeddedContent(t *testing.T) {
	m := New(nil)
	m.skills = []*SkillMetadata{{
		Name:        "example-skill",
		Description: "Example skill",
		Path:        "/tmp/example-skill/SKILL.md",
		Content:     "---\nname: example-skill\n---\n# Full Guide",
	}}

	got := invokeTool(t, m.newActivateSkillTool(), `{"name":"example-skill"}`)
	if !m.activeSkills["example-skill"] {
		t.Fatalf("skill should be marked active")
	}
	if !strings.Contains(got, "# Full Guide") {
		t.Fatalf("activate_skill output = %q, want full markdown content", got)
	}
}

func TestMiddlewareBeforeAgentRefreshesSkillsAndPreservesActiveState(t *testing.T) {
	loader := &fakeLoader{
		skills: [][]*SkillMetadata{
			{{
				Name:        "example-skill",
				Description: "v1",
				Path:        "/tmp/example-skill/SKILL.md",
				Content:     "# Full Guide",
			}},
			{{
				Name:        "example-skill",
				Description: "v2",
				Path:        "/tmp/example-skill/SKILL.md",
				Content:     "# Full Guide",
			}},
		},
	}
	m := New(loader)

	if err := m.BeforeAgent(context.Background()); err != nil {
		t.Fatalf("BeforeAgent() error = %v", err)
	}
	firstPrompt, err := m.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	if len(firstPrompt) != 1 || !strings.Contains(firstPrompt[0].Content, "v1") {
		t.Fatalf("first prompt = %+v, want skill description v1", firstPrompt)
	}

	activateOut := invokeTool(t, m.newActivateSkillTool(), `{"name":"example-skill"}`)
	if !strings.Contains(activateOut, "# Full Guide") {
		t.Fatalf("activate output = %q, want full guide", activateOut)
	}

	if err := m.BeforeAgent(context.Background()); err != nil {
		t.Fatalf("BeforeAgent() second call error = %v", err)
	}
	secondPrompt, err := m.BuildInitialContext(context.Background())
	if err != nil {
		t.Fatalf("BuildInitialContext() second call error = %v", err)
	}
	if len(secondPrompt) != 1 || !strings.Contains(secondPrompt[0].Content, "v2") {
		t.Fatalf("second prompt = %+v, want skill description v2", secondPrompt)
	}
	if !strings.Contains(secondPrompt[0].Content, "[ACTIVE]") {
		t.Fatalf("second prompt = %+v, want active marker preserved", secondPrompt)
	}
	if loader.listCalls != 2 {
		t.Fatalf("loader.ListSkills() calls = %d, want 2", loader.listCalls)
	}
}

func TestMiddlewareToolMaskFiltersToolsAndPrompt(t *testing.T) {
	ctx := context.Background()
	m := NewWithConfig(nil, &MiddlewareConfig{
		ToolMask: func(_ context.Context, info *schema.ToolInfo) bool {
			return info.Name != constant.ToolActivateSkill
		},
	})
	m.skills = []*SkillMetadata{{
		Name:        "example-skill",
		Description: "Example skill",
		Path:        "/tmp/example-skill/SKILL.md",
	}}

	tools, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	for _, tl := range tools {
		info, _ := tl.Info(ctx)
		if info != nil && info.Name == constant.ToolActivateSkill {
			t.Fatalf("activate_skill should be masked, got tools %+v", tools)
		}
	}

	msgs, err := m.BuildInitialContext(ctx)
	if err != nil {
		t.Fatalf("BuildInitialContext() error = %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("BuildInitialContext() messages = %+v, want one system message", msgs)
	}
	if strings.Contains(msgs[0].Content, "`activate_skill`") {
		t.Fatalf("prompt should not mention masked activate_skill:\n%s", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "list_skills") {
		t.Fatalf("prompt should mention visible list_skills:\n%s", msgs[0].Content)
	}
}

func TestMiddlewareConcurrentToolInvocationsAreSafe(t *testing.T) {
	m := New(nil)
	m.skills = []*SkillMetadata{
		{
			Name:        "alpha",
			Description: "alpha skill",
			Path:        "/tmp/alpha/SKILL.md",
			Content:     "# Alpha",
			Metadata:    map[string]string{"alpha_key": "alpha_value"},
		},
		{
			Name:        "beta",
			Description: "beta skill",
			Path:        "/tmp/beta/SKILL.md",
			Content:     "# Beta",
			Metadata:    map[string]string{"beta_key": "beta_value"},
		},
	}

	activate := m.newActivateSkillTool()
	deactivate := m.newDeactivateSkillTool()
	list := m.newListSkillsTool()

	const workers = 16
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				name := "alpha"
				if (id+j)%2 == 0 {
					name = "beta"
				}
				payload := `{"name":"` + name + `"}`
				_ = invokeTool(t, activate, payload)
				_ = invokeTool(t, list, `{"verbose":true}`)
				_ = invokeTool(t, deactivate, payload)
			}
		}(i)
	}
	wg.Wait()
}

func TestMiddlewareToolsBuiltBeforeReloadSeeUpdatedSkills(t *testing.T) {
	loader := &fakeLoader{
		skills: [][]*SkillMetadata{
			{{
				Name:        "example-skill",
				Description: "v1",
				Path:        "/tmp/example-skill/SKILL.md",
				Content:     "# Full Guide v1",
			}},
			{{
				Name:        "example-skill",
				Description: "v2",
				Path:        "/tmp/example-skill/SKILL.md",
				Content:     "# Full Guide v2",
			}},
		},
	}
	m := New(loader)

	tools, err := m.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("len(tools) = %d, want 3", len(tools))
	}

	if err := m.BeforeAgent(context.Background()); err != nil {
		t.Fatalf("BeforeAgent() error = %v", err)
	}

	listOut := invokeTool(t, tools[0], `{"verbose":true}`)
	if !strings.Contains(listOut, `"description": "v1"`) {
		t.Fatalf("list_skills output = %q, want v1 description", listOut)
	}

	activateOut := invokeTool(t, tools[1], `{"name":"example-skill"}`)
	if !strings.Contains(activateOut, "# Full Guide v1") {
		t.Fatalf("activate output = %q, want v1 guide", activateOut)
	}

	if err := m.BeforeAgent(context.Background()); err != nil {
		t.Fatalf("BeforeAgent() second call error = %v", err)
	}

	listOut = invokeTool(t, tools[0], `{"verbose":true}`)
	if !strings.Contains(listOut, `"description": "v2"`) {
		t.Fatalf("list_skills output after reload = %q, want v2 description", listOut)
	}

	activateOut = invokeTool(t, tools[1], `{"name":"example-skill"}`)
	if !strings.Contains(activateOut, "# Full Guide v2") {
		t.Fatalf("activate output after reload = %q, want v2 guide", activateOut)
	}
}
