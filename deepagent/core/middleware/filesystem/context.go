package filesystem

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"eino-cli/deepagent/core/backends"
)

// ignorePatterns 定义应该忽略的目录/文件
var ignorePatterns = map[string]bool{
	".git":          true,
	"node_modules":  true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".ruff_cache":   true,
	".tox":          true,
	".coverage":     true,
	".eggs":         true,
	"dist":          true,
	"build":         true,
	".idea":         true,
	".vscode":       true,
	".next":         true,
	".nuxt":         true,
	"vendor":        true,
	"target":        true,
	".gradle":       true,
	".m2":           true,
	"coverage":      true,
	".nyc_output":   true,
	".cache":        true,
	".parcel-cache": true,
	"__snapshots__": true,
	".terraform":    true,
	".serverless":   true,
}

// getCachedLocalContext 获取缓存的本地上下文提示词（懒初始化）
func (m *FilesystemMiddleware) getCachedLocalContext(ctx context.Context) string {
	m.mu.RLock()
	if m.cachedContext != "" {
		defer m.mu.RUnlock()
		return m.cachedContext
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cachedContext == "" {
		m.cachedContext = m.buildLocalContext(ctx)
	}
	return m.cachedContext
}

// buildLocalContext 构建本地环境上下文信息
func (m *FilesystemMiddleware) buildLocalContext(ctx context.Context) string {
	var sections []string
	sections = append(sections, "## Local Context\n")

	// 1. 当前目录
	sections = append(sections, fmt.Sprintf("**Current Directory**: `%s`", m.workDir))

	// 2. 项目语言
	if lang := m.detectProjectLanguage(); lang != "" {
		sections = append(sections, fmt.Sprintf("**Project Language**: %s", lang))
	}

	// 3. 包管理器
	var pkgManagers []string
	if pm := m.detectPythonPackageManager(); pm != "" {
		pkgManagers = append(pkgManagers, "Python: "+pm)
	}
	if pm := m.detectNodePackageManager(); pm != "" {
		pkgManagers = append(pkgManagers, "Node: "+pm)
	}
	if len(pkgManagers) > 0 {
		sections = append(sections, fmt.Sprintf("**Package Manager**: %s",
			strings.Join(pkgManagers, ", ")))
	}

	// 4. Git 信息
	if git := m.getGitInfo(); git != nil {
		gitStr := fmt.Sprintf("Current branch `%s`", git.CurrentBranch)
		if len(git.MainBranches) > 0 {
			gitStr += fmt.Sprintf(", main branch: `%s`", git.MainBranches[0])
		}
		sections = append(sections, fmt.Sprintf("**Git**: %s", gitStr))
	}

	// 5. 测试命令
	if testCmd := m.detectTestCommand(); testCmd != "" {
		sections = append(sections, fmt.Sprintf("**Run Tests**: `%s`", testCmd))
	}

	// 6. 目录树
	tree := m.getDirectoryTree(ctx, m.maxTreeDepth, m.maxTreeEntries)
	sections = append(sections, "\n**Tree**:\n```\n"+tree+"```")

	// 7. 额外上下文（如已上传文件列表等）
	if m.extraContext != "" {
		sections = append(sections, m.extraContext)
	}

	return strings.Join(sections, "\n")
}

// ==================== Git 信息检测 ====================

type gitInfo struct {
	CurrentBranch string
	MainBranches  []string
}

func (m *FilesystemMiddleware) getGitInfo() *gitInfo {
	info := &gitInfo{}

	// 获取当前分支
	branch, err := m.runCommand("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil
	}
	info.CurrentBranch = strings.TrimSpace(branch)

	// 获取所有分支，查找 main/master
	branches, err := m.runCommand("git", "branch")
	if err != nil {
		return info
	}

	for _, line := range strings.Split(branches, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		line = strings.TrimSpace(line)
		if line == "main" || line == "master" {
			info.MainBranches = append(info.MainBranches, line)
		}
	}

	return info
}

// ==================== 语言检测 ====================

func (m *FilesystemMiddleware) detectProjectLanguage() string {
	checks := []struct {
		files    []string
		language string
	}{
		{[]string{"go.mod"}, "Go"},
		{[]string{"pyproject.toml", "setup.py", "setup.cfg"}, "Python"},
		{[]string{"package.json"}, "JavaScript/TypeScript"},
		{[]string{"Cargo.toml"}, "Rust"},
		{[]string{"pom.xml", "build.gradle", "build.gradle.kts"}, "Java"},
		{[]string{"composer.json"}, "PHP"},
		{[]string{"Gemfile"}, "Ruby"},
		{[]string{"mix.exs"}, "Elixir"},
		{[]string{"pubspec.yaml"}, "Dart/Flutter"},
		{[]string{"Package.swift"}, "Swift"},
		{[]string{"*.csproj", "*.sln"}, "C#/.NET"},
	}

	for _, check := range checks {
		for _, file := range check.files {
			if strings.Contains(file, "*") {
				// 使用 glob 匹配
				matches, _ := filepath.Glob(filepath.Join(m.workDir, file))
				if len(matches) > 0 {
					return check.language
				}
			} else if m.fileExists(file) {
				return check.language
			}
		}
	}
	return ""
}

// ==================== 包管理器检测 ====================

func (m *FilesystemMiddleware) detectPythonPackageManager() string {
	// 优先级: uv > poetry > pipenv > pip
	if m.fileExists("uv.lock") || m.fileContains("pyproject.toml", "[tool.uv]") {
		return "uv"
	}
	if m.fileExists("poetry.lock") || m.fileContains("pyproject.toml", "[tool.poetry]") {
		return "poetry"
	}
	if m.fileExists("Pipfile.lock") || m.fileExists("Pipfile") {
		return "pipenv"
	}
	if m.fileExists("requirements.txt") || m.fileExists("pyproject.toml") {
		return "pip"
	}
	return ""
}

func (m *FilesystemMiddleware) detectNodePackageManager() string {
	// 优先级: bun > pnpm > yarn > npm
	if m.fileExists("bun.lockb") || m.fileExists("bun.lock") {
		return "bun"
	}
	if m.fileExists("pnpm-lock.yaml") {
		return "pnpm"
	}
	if m.fileExists("yarn.lock") {
		return "yarn"
	}
	if m.fileExists("package-lock.json") || m.fileExists("package.json") {
		return "npm"
	}
	return ""
}

// ==================== 测试命令检测 ====================

func (m *FilesystemMiddleware) detectTestCommand() string {
	// 1. 检查 Makefile
	if m.fileContains("Makefile", "test:") || m.fileContains("Makefile", "tests:") {
		return "make test"
	}

	// 2. Go 项目
	if m.fileExists("go.mod") {
		return "go test ./..."
	}

	// 3. Python 项目
	if m.fileExists("pyproject.toml") || m.fileExists("pytest.ini") ||
		m.fileExists("conftest.py") || m.dirExists("tests") || m.dirExists("test") {
		return "pytest"
	}

	// 4. Node 项目
	if m.fileExists("package.json") {
		content, err := os.ReadFile(filepath.Join(m.workDir, "package.json"))
		if err == nil && strings.Contains(string(content), `"test"`) {
			return "npm test"
		}
	}

	// 5. Rust 项目
	if m.fileExists("Cargo.toml") {
		return "cargo test"
	}

	// 6. Java/Maven 项目
	if m.fileExists("pom.xml") {
		return "mvn test"
	}

	// 7. Java/Gradle 项目
	if m.fileExists("build.gradle") || m.fileExists("build.gradle.kts") {
		return "./gradlew test"
	}

	return ""
}

// ==================== 目录树生成 ====================

func (m *FilesystemMiddleware) getDirectoryTree(ctx context.Context, maxDepth, maxEntries int) string {
	var sb strings.Builder
	baseName := filepath.Base(m.workDir)
	sb.WriteString(baseName + "/\n")

	entryCount := 0
	m.buildTree(ctx, &sb, m.workDir, "", 0, maxDepth, maxEntries, &entryCount)

	return sb.String()
}

func (m *FilesystemMiddleware) buildTree(ctx context.Context, sb *strings.Builder, dir, prefix string,
	depth, maxDepth, maxEntries int, entryCount *int) {

	if depth >= maxDepth || *entryCount >= maxEntries {
		return
	}

	entries, err := m.backend.LsInfo(ctx, dir)
	if err != nil {
		return
	}

	// 过滤和排序
	var filtered []backends.FileInfo
	for _, e := range entries {
		name := e.Name()
		// 跳过隐藏文件（但保留 .deepagents）
		if strings.HasPrefix(name, ".") && name != ".deepagents" {
			continue
		}
		// 跳过忽略的目录
		if ignorePatterns[name] {
			continue
		}
		filtered = append(filtered, e)
	}

	// 目录优先排序
	sort.Slice(filtered, func(i, j int) bool {
		iDir := filtered[i].IsDir
		jDir := filtered[j].IsDir
		if iDir != jDir {
			return iDir
		}
		return filtered[i].Name() < filtered[j].Name()
	})

	for i, entry := range filtered {
		if *entryCount >= maxEntries {
			sb.WriteString(prefix + "... (truncated)\n")
			return
		}

		isLast := i == len(filtered)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		name := entry.Name()
		if entry.IsDir {
			name += "/"
		}

		sb.WriteString(prefix + connector + name + "\n")
		*entryCount++

		if entry.IsDir {
			newPrefix := prefix + "│   "
			if isLast {
				newPrefix = prefix + "    "
			}
			m.buildTree(ctx, sb, filepath.Join(dir, entry.Name()), newPrefix,
				depth+1, maxDepth, maxEntries, entryCount)
		}
	}
}

// ==================== 辅助函数 ====================

// runCommand 执行 shell 命令并返回输出
func (m *FilesystemMiddleware) runCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), m.commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// fileExists 检查文件是否存在
func (m *FilesystemMiddleware) fileExists(name string) bool {
	path := filepath.Join(m.workDir, name)
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// dirExists 检查目录是否存在
func (m *FilesystemMiddleware) dirExists(name string) bool {
	path := filepath.Join(m.workDir, name)
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// fileContains 检查文件是否包含指定内容
func (m *FilesystemMiddleware) fileContains(name, substr string) bool {
	path := filepath.Join(m.workDir, name)
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(content), substr)
}
