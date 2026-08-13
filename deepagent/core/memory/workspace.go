package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"eino-cli/deepagent/core/backends"
)

const (
	summaryFile         = "memory_summary.md"
	memoryFile          = "MEMORY.md"
	rawMemoriesFile     = "raw_memories.md"
	rolloutSummariesDir = "rollout_summaries"
	workspaceDiffFile   = "phase2_workspace_diff.md"
)

type Workspace struct {
	backend backends.Backend
	root    string
}

func NewWorkspace(backend backends.Backend, root string) *Workspace {
	return &Workspace{backend: backend, root: cleanRoot(root)}
}

func (w *Workspace) Root() string {
	if w == nil {
		return ""
	}
	return w.root
}

func (w *Workspace) ArtifactHash(ctx context.Context) (string, error) {
	hashes, err := w.artifactHashes(ctx)
	if err != nil {
		return "", err
	}
	return hashes.artifactHash, nil
}

func (w *Workspace) artifactHashes(ctx context.Context) (workspaceArtifactHashes, error) {
	if w == nil {
		return workspaceArtifactHashes{}, fmt.Errorf("memory workspace is required")
	}
	memoryContent, memoryFound, err := w.readOptional(ctx, memoryFile)
	if err != nil {
		return workspaceArtifactHashes{}, err
	}
	summaryContent, summaryFound, err := w.readOptional(ctx, summaryFile)
	if err != nil {
		return workspaceArtifactHashes{}, err
	}
	return workspaceArtifactHashes{
		memoryHash:   fileArtifactHash(memoryContent, memoryFound),
		summaryHash:  fileArtifactHash(summaryContent, summaryFound),
		artifactHash: memoryArtifactHash(memoryContent, memoryFound, summaryContent, summaryFound),
	}, nil
}

func (w *Workspace) ForUser(userID string) *Workspace {
	if w == nil {
		return nil
	}
	return &Workspace{
		backend: w.backend,
		root:    path.Join(w.root, "users", userPathSegment(userID)),
	}
}

func (w *Workspace) AgentBackend() backends.SandboxBackend {
	if w == nil || w.backend == nil {
		return nil
	}
	if sb, ok := w.backend.(backends.SandboxBackend); ok {
		return workspaceSandboxBackend{backend: sb, root: w.root}
	}
	return nil
}

func (w *Workspace) AgentWorkDir() string {
	if w == nil {
		return ""
	}
	return w.root
}

func (w *Workspace) ReadSummary(ctx context.Context) (Summary, error) {
	content, found, err := w.readOptional(ctx, summaryFile)
	if err != nil || !found {
		return Summary{}, err
	}
	return Summary{Content: content, Found: true}, nil
}

func (w *Workspace) ReadMemory(ctx context.Context) (string, error) {
	content, _, err := w.readOptional(ctx, memoryFile)
	return content, err
}

func (w *Workspace) ReadRawMemories(ctx context.Context) (string, error) {
	content, _, err := w.readOptional(ctx, rawMemoriesFile)
	return content, err
}

func (w *Workspace) ReadWorkspaceDiff(ctx context.Context) (string, error) {
	content, _, err := w.readOptional(ctx, workspaceDiffFile)
	return content, err
}

func (w *Workspace) SyncInputs(ctx context.Context, outputs []Stage1Output) (string, error) {
	raw := renderRawMemories(outputs)
	if _, err := w.backend.Write(ctx, w.path(rawMemoriesFile), raw); err != nil {
		return "", err
	}
	for _, out := range outputs {
		name := slug(out.SourceThreadID)
		if name == "" {
			name = slug(out.ID)
		}
		if name == "" {
			name = "unknown"
		}
		p := w.path(path.Join(rolloutSummariesDir, name+".md"))
		if _, err := w.backend.Write(ctx, p, renderRolloutSummary(out)); err != nil {
			return "", err
		}
	}
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:]), nil
}

func (w *Workspace) WriteConsolidated(ctx context.Context, memory string, summary string) error {
	memory = sanitizeMemoryArtifactText(memory)
	summary = sanitizeMemoryArtifactText(summary)
	raw, found, err := w.readOptional(ctx, rawMemoriesFile)
	if err != nil {
		return err
	}
	if found {
		memory = sanitizeExplicitNoiseTerms(memory, raw)
		summary = sanitizeExplicitNoiseTerms(summary, raw)
	}
	if _, err := w.backend.Write(ctx, w.path(memoryFile), memory); err != nil {
		return err
	}
	_, err = w.backend.Write(ctx, w.path(summaryFile), summary)
	return err
}

var (
	disposableMarkerRE      = regexp.MustCompile(`(?i)\b(?:TEMP|QA)(?:-[A-Z0-9]+){2,}\b|\bWEBUI-MEM-[A-Z0-9-]+\b`)
	quotedDisposableTermRE  = regexp.MustCompile("`([^`]+)`|\"([^\"]+)\"|'([^']+)'|“([^”]+)”")
	englishDisposableTermRE = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9_-]*(?:\s+[A-Za-z][A-Za-z0-9_-]*){1,4}\b`)
	initialRejectedQuoteRE  = regexp.MustCompile(`(?i)(?:initially stated|first said|user first said)\s+"([^"]+)"[^.\n]*(?:then|but)[^.\n]*(?:corrected|deprecated|rejected)`)
	correctedFromQuoteRE    = regexp.MustCompile(`(?i)corrected from\s+"([^"]+)"\s+to\s+"[^"]+"`)
	repeatedMarkerTextRE    = regexp.MustCompile(`explicitly temporary test marker(?:\s*(?:,|和|and|or)?\s*explicitly temporary test marker)+`)
)

func sanitizeMemoryArtifactText(text string) string {
	return disposableMarkerRE.ReplaceAllString(text, "explicitly temporary test marker")
}

func sanitizeExplicitNoiseTerms(text string, raw string) string {
	for _, replacement := range explicitArtifactReplacements(raw) {
		text = replaceArtifactTerm(text, replacement.term, replacement.with)
	}
	return repeatedMarkerTextRE.ReplaceAllString(text, "explicitly temporary test marker")
}

func replaceArtifactTerm(text string, term string, with string) string {
	return regexp.MustCompile(`(?i)`+regexp.QuoteMeta(term)).ReplaceAllString(text, with)
}

type explicitArtifactReplacement struct {
	term string
	with string
}

func explicitArtifactReplacements(raw string) []explicitArtifactReplacement {
	seen := map[string]string{}
	for _, segment := range splitNoiseCandidateSegments(raw) {
		for _, match := range quotedDisposableTermRE.FindAllStringSubmatch(segment, -1) {
			for _, group := range match[1:] {
				addExplicitNoiseTerm(seen, group)
			}
		}
		for _, term := range extractChineseNoisePredicateTerms(segment) {
			addExplicitNoiseTerm(seen, term)
		}
		for _, term := range extractChineseRejectedPreferenceTerms(segment) {
			addExplicitDiscardTerm(seen, term)
		}
		for _, term := range extractInitialRejectedQuotedTerms(segment) {
			addExplicitDiscardTerm(seen, term)
		}
		for _, term := range extractCorrectedFromQuotedTerms(segment) {
			addExplicitDiscardTerm(seen, term)
		}
	}
	for _, term := range extractInitialRejectedQuotedTerms(raw) {
		addExplicitDiscardTerm(seen, term)
	}
	for _, term := range extractCorrectedFromQuotedTerms(raw) {
		addExplicitDiscardTerm(seen, term)
	}
	replacements := make([]explicitArtifactReplacement, 0, len(seen))
	for term, with := range seen {
		replacements = append(replacements, explicitArtifactReplacement{term: term, with: with})
	}
	sort.Slice(replacements, func(i, j int) bool {
		return len(replacements[i].term) > len(replacements[j].term)
	})
	return replacements
}

func splitNoiseCandidateSegments(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case '\n', '。', '；', ';':
			return true
		default:
			return false
		}
	})
	out := fields[:0]
	for _, field := range fields {
		if isExplicitNoiseSegment(field) || isExplicitRejectedPreferenceSegment(field) {
			out = append(out, field)
		}
	}
	return out
}

func isExplicitNoiseSegment(segment string) bool {
	lower := strings.ToLower(segment)
	return strings.Contains(segment, "一次性噪声") ||
		strings.Contains(segment, "测试噪声") ||
		strings.Contains(segment, "不要当成长久记忆") ||
		strings.Contains(segment, "不要当作长期记忆") ||
		strings.Contains(lower, "temporary noise") ||
		strings.Contains(lower, "test-only") ||
		strings.Contains(lower, "disposable marker") ||
		strings.Contains(lower, "not long-term")
}

func isExplicitRejectedPreferenceSegment(segment string) bool {
	lower := strings.ToLower(segment)
	return strings.Contains(segment, "是错误的") ||
		strings.Contains(segment, "是错的") ||
		strings.Contains(segment, "旧偏好应该降权") ||
		strings.Contains(segment, "旧偏好应该废弃") ||
		(strings.Contains(lower, "initially stated") && strings.Contains(lower, "corrected")) ||
		(strings.Contains(lower, "first said") && strings.Contains(lower, "corrected"))
}

func extractChineseNoisePredicateTerms(segment string) []string {
	idx := strings.Index(segment, "都是一次性噪声")
	if idx < 0 {
		idx = strings.Index(segment, "都是测试噪声")
	}
	if idx < 0 {
		return nil
	}
	prefix := segment[:idx]
	if boundary := strings.LastIndexAny(prefix, ":：\"“"); boundary >= 0 {
		prefix = prefix[boundary+1:]
	}
	parts := strings.FieldsFunc(prefix, func(r rune) bool {
		switch r {
		case ',', '，', '/', '|', '&':
			return true
		default:
			return false
		}
	})
	var terms []string
	for _, part := range parts {
		for _, token := range strings.Fields(part) {
			switch strings.ToLower(token) {
			case "and", "or":
				part = strings.ReplaceAll(part, token, ",")
			}
		}
		for _, sub := range strings.Split(part, ",") {
			for _, term := range strings.FieldsFunc(sub, func(r rune) bool {
				return r == '和' || r == '与'
			}) {
				terms = append(terms, englishDisposableTermRE.FindAllString(term, -1)...)
			}
		}
	}
	return terms
}

func extractChineseRejectedPreferenceTerms(segment string) []string {
	idx := strings.Index(segment, "是错误的")
	if idx < 0 {
		idx = strings.Index(segment, "是错的")
	}
	if idx < 0 {
		return nil
	}
	prefix := segment[:idx]
	if boundary := strings.LastIndexAny(prefix, ":：,，"); boundary >= 0 {
		prefix = strings.TrimLeft(prefix[boundary:], ":：,，")
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	return []string{prefix}
}

func extractInitialRejectedQuotedTerms(segment string) []string {
	var terms []string
	for _, match := range initialRejectedQuoteRE.FindAllStringSubmatch(segment, -1) {
		if len(match) > 1 {
			terms = append(terms, match[1])
		}
	}
	return terms
}

func extractCorrectedFromQuotedTerms(segment string) []string {
	var terms []string
	for _, match := range correctedFromQuoteRE.FindAllStringSubmatch(segment, -1) {
		if len(match) > 1 {
			terms = append(terms, match[1])
		}
	}
	return terms
}

func addExplicitNoiseTerm(seen map[string]string, term string) {
	term = strings.TrimSpace(strings.Trim(term, "`'\"“”.,，:：;；()（）[]【】"))
	if !isSpecificNoiseTerm(term) {
		return
	}
	seen[term] = "explicitly temporary test marker"
}

func addExplicitDiscardTerm(seen map[string]string, term string) {
	term = strings.TrimSpace(strings.Trim(term, "`'\"“”.,，:：;；()（）[]【】"))
	if len(term) < 2 || len(term) > 80 || strings.ContainsAny(term, "\r\n") {
		return
	}
	seen[term] = "an older rejected preference"
	if trimmed, ok := stalePreferencePrefix(term); ok {
		seen[trimmed] = "an older rejected preference"
	}
	for _, variant := range stalePreferenceVariants(term) {
		seen[variant] = "an older rejected preference"
	}
}

func stalePreferencePrefix(term string) (string, bool) {
	lower := strings.ToLower(term)
	for _, suffix := range []string{" wins", " first"} {
		if strings.HasSuffix(lower, suffix) {
			trimmed := strings.TrimSpace(term[:len(term)-len(suffix)])
			return trimmed, trimmed != ""
		}
	}
	return "", false
}

func stalePreferenceVariants(term string) []string {
	lower := strings.ToLower(term)
	switch {
	case strings.Contains(term, "最早偏好优先"):
		return []string{"earliest preference first", "earliest preference", "oldest preference wins", "oldest preference"}
	case strings.Contains(lower, "oldest preference"):
		return []string{"earliest preference first", "earliest preference"}
	default:
		return nil
	}
}

func isSpecificNoiseTerm(term string) bool {
	if len(term) < 3 || len(term) > 80 || strings.ContainsAny(term, "\r\n") {
		return false
	}
	if disposableMarkerRE.MatchString(term) {
		return true
	}
	lower := strings.ToLower(term)
	switch lower {
	case "temporary noise", "test noise", "temporary test marker", "explicitly temporary test marker", "disposable marker", "not long-term":
		return false
	}
	return englishDisposableTermRE.MatchString(term)
}

func (w *Workspace) WriteWorkspaceDiff(ctx context.Context, diff string) error {
	_, err := w.backend.Write(ctx, w.path(workspaceDiffFile), diff)
	return err
}

func (w *Workspace) path(rel string) string {
	return path.Join(w.root, rel)
}

func (w *Workspace) readOptional(ctx context.Context, rel string) (string, bool, error) {
	content, err := w.backend.Read(ctx, w.path(rel), nil, nil)
	if err != nil {
		if errors.Is(err, backends.ErrFileNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return content, true, nil
}

func cleanRoot(root string) string {
	root = strings.TrimSpace(root)
	root = strings.Trim(root, "/")
	if root == "" || root == "." {
		return "memory"
	}
	return path.Clean(root)
}

func renderRawMemories(outputs []Stage1Output) string {
	var b strings.Builder
	b.WriteString("# Raw Memories\n\n")
	for _, out := range outputs {
		if strings.TrimSpace(out.RawMemory) == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("## %s\n\n", displayStage1ID(out)))
		b.WriteString(fmt.Sprintf("- user_id: `%s`\n", out.UserID))
		b.WriteString(fmt.Sprintf("- source_thread_id: `%s`\n", out.SourceThreadID))
		b.WriteString(fmt.Sprintf("- source_turn_id: `%s`\n", out.SourceTurnID))
		if !out.SourceUpdatedAt.IsZero() {
			b.WriteString(fmt.Sprintf("- source_updated_at: `%s`\n", out.SourceUpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")))
		}
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(out.RawMemory))
		b.WriteString("\n\n")
	}
	return b.String()
}

func renderRolloutSummary(out Stage1Output) string {
	return fmt.Sprintf("# Rollout Summary\n\nSource thread: `%s`\nSource turn: `%s`\nStage1 output: `%s`\n\n%s\n",
		out.SourceThreadID,
		out.SourceTurnID,
		displayStage1ID(out),
		strings.TrimSpace(out.RolloutSummary),
	)
}

func displayStage1ID(out Stage1Output) string {
	if strings.TrimSpace(out.ID) != "" {
		return out.ID
	}
	if strings.TrimSpace(out.SourceThreadID) != "" {
		return out.SourceThreadID
	}
	return "unknown"
}

var slugRe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func slug(v string) string {
	v = strings.TrimSpace(v)
	v = slugRe.ReplaceAllString(v, "-")
	return strings.Trim(v, "-")
}

func userPathSegment(userID string) string {
	trimmed := strings.TrimSpace(userID)
	label := slug(trimmed)
	if label == "" {
		label = "unknown"
	}
	h := sha256.Sum256([]byte(trimmed))
	return fmt.Sprintf("%s-%s", label, hex.EncodeToString(h[:])[:12])
}

func memoryArtifactHash(memoryContent string, memoryFound bool, summaryContent string, summaryFound bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "memory_found=%t\n", memoryFound)
	h.Write([]byte(memoryContent))
	fmt.Fprintf(h, "\nsummary_found=%t\n", summaryFound)
	h.Write([]byte(summaryContent))
	return hex.EncodeToString(h.Sum(nil))
}

type workspaceArtifactHashes struct {
	memoryHash   string
	summaryHash  string
	artifactHash string
}

func fileArtifactHash(content string, found bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "found=%t\n", found)
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}

type workspaceSandboxBackend struct {
	backend backends.SandboxBackend
	root    string
}

func (b workspaceSandboxBackend) LsInfo(ctx context.Context, p string) ([]backends.FileInfo, error) {
	prefixed, err := b.prefixPath(p)
	if err != nil {
		return nil, err
	}
	files, err := b.backend.LsInfo(ctx, prefixed)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].Path = b.stripPath(files[i].Path)
	}
	return files, nil
}

func (b workspaceSandboxBackend) Read(ctx context.Context, p string, offset, limit *int) (string, error) {
	prefixed, err := b.prefixPath(p)
	if err != nil {
		return "", err
	}
	return b.backend.Read(ctx, prefixed, offset, limit)
}

func (b workspaceSandboxBackend) Write(ctx context.Context, p string, content string) (*backends.WriteResult, error) {
	prefixed, err := b.prefixPath(p)
	if err != nil {
		return &backends.WriteResult{Path: p, Error: backends.ErrInvalidPath}, nil
	}
	result, err := b.backend.Write(ctx, prefixed, content)
	if result != nil {
		result.Path = b.stripPath(result.Path)
	}
	return result, err
}

func (b workspaceSandboxBackend) Edit(ctx context.Context, p string, oldString, newString string, replaceAll bool) (*backends.EditResult, error) {
	prefixed, err := b.prefixPath(p)
	if err != nil {
		return &backends.EditResult{Path: p, Error: backends.ErrInvalidPath}, nil
	}
	result, err := b.backend.Edit(ctx, prefixed, oldString, newString, replaceAll)
	if result != nil {
		result.Path = b.stripPath(result.Path)
	}
	return result, err
}

func (b workspaceSandboxBackend) GrepRaw(ctx context.Context, pattern string, p string, glob string) ([]backends.GrepMatch, error) {
	prefixed, err := b.prefixPath(p)
	if err != nil {
		return nil, err
	}
	matches, err := b.backend.GrepRaw(ctx, pattern, prefixed, glob)
	if err != nil {
		return nil, err
	}
	for i := range matches {
		matches[i].Path = b.stripPath(matches[i].Path)
	}
	return matches, nil
}

func (b workspaceSandboxBackend) GlobInfo(ctx context.Context, pattern string, p string) ([]backends.FileInfo, error) {
	prefixed, err := b.prefixPath(p)
	if err != nil {
		return nil, err
	}
	files, err := b.backend.GlobInfo(ctx, pattern, prefixed)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].Path = b.stripPath(files[i].Path)
	}
	return files, nil
}

func (b workspaceSandboxBackend) UploadFiles(ctx context.Context, files []struct {
	Path    string
	Content []byte
}) ([]backends.FileUploadResponse, error) {
	prefixed := make([]struct {
		Path    string
		Content []byte
	}, 0, len(files))
	for _, file := range files {
		prefixedPath, err := b.prefixPath(file.Path)
		if err != nil {
			return nil, err
		}
		prefixed = append(prefixed, struct {
			Path    string
			Content []byte
		}{Path: prefixedPath, Content: file.Content})
	}
	responses, err := b.backend.UploadFiles(ctx, prefixed)
	for i := range responses {
		responses[i].Path = b.stripPath(responses[i].Path)
	}
	return responses, err
}

func (b workspaceSandboxBackend) ChangeDir(ctx context.Context, p string) error {
	prefixed, err := b.prefixPath(p)
	if err != nil {
		return err
	}
	return b.backend.ChangeDir(ctx, prefixed)
}

func (b workspaceSandboxBackend) Execute(ctx context.Context, command string) (*backends.ExecuteResponse, error) {
	return b.backend.Execute(ctx, command)
}

func (b workspaceSandboxBackend) ExecuteCommand(ctx context.Context, req backends.CommandRequest) (*backends.CommandResult, error) {
	if strings.TrimSpace(req.WorkDir) != "" {
		prefixed, err := b.prefixPath(req.WorkDir)
		if err != nil {
			return nil, err
		}
		req.WorkDir = prefixed
	}
	return b.backend.ExecuteCommand(ctx, req)
}

func (b workspaceSandboxBackend) ID() string {
	return b.backend.ID()
}

func (b workspaceSandboxBackend) prefixPath(p string) (string, error) {
	root := strings.Trim(strings.TrimSpace(b.root), "/")
	if root == "" || root == "." {
		return p, nil
	}
	clean := strings.Trim(strings.TrimSpace(p), "/")
	if clean == "" || clean == "." {
		return root, nil
	}
	clean = path.Clean(clean)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", backends.ErrInvalidPath
	}
	if clean == root || strings.HasPrefix(clean, root+"/") {
		return clean, nil
	}
	prefixed := path.Join(root, clean)
	if prefixed != root && !strings.HasPrefix(prefixed, root+"/") {
		return "", backends.ErrInvalidPath
	}
	return prefixed, nil
}

func (b workspaceSandboxBackend) stripPath(p string) string {
	root := strings.Trim(strings.TrimSpace(b.root), "/")
	clean := strings.Trim(strings.TrimSpace(p), "/")
	if root == "" || root == "." || clean == "" {
		return p
	}
	if clean == root {
		return "."
	}
	if strings.HasPrefix(clean, root+"/") {
		return strings.TrimPrefix(clean, root+"/")
	}
	return p
}

var _ backends.SandboxBackend = workspaceSandboxBackend{}
