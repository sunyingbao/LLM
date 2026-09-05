package tools

import (
	"eino-cli/deepagent/backend/consts"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type grepArgs struct {
	Pattern         string
	Path            *string
	CaseInsensitive *bool
}

type grepMatch struct {
	Path    string
	Line    int
	Content string
}

func runGrep(in grepArgs) (result []grepMatch, err error) {
	re, err := compileGrepPattern(in.Pattern, valueOr(in.CaseInsensitive, false))
	if err != nil {
		return nil, err
	}

	base := resolveRoot()
	searchRoot := resolvePath(valueOr(in.Path, ""))

	var matches []grepMatch
	walkErr := filepath.WalkDir(searchRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				matches = append(matches, grepMatch{Path: rel, Line: i + 1, Content: line})
			}
		}
		return nil
	})
	return matches, walkErr
}

func compileGrepPattern(pattern string, caseInsensitive bool) (*regexp.Regexp, error) {
	re, err := compileRegexp(pattern, caseInsensitive)
	if err == nil {
		return re, nil
	}
	re, literalErr := compileRegexp(regexp.QuoteMeta(pattern), caseInsensitive)
	if literalErr != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	return re, nil
}

func compileRegexp(pattern string, caseInsensitive bool) (*regexp.Regexp, error) {
	if caseInsensitive {
		pattern = "(?i:" + pattern + ")"
	}
	return regexp.Compile(pattern)
}

func formatGrepContent(matches []grepMatch, showLine bool) string {
	if len(matches) == 0 {
		return consts.NoMatchesFound
	}
	lines := make([]string, len(matches))
	for i, m := range matches {
		if showLine {
			lines[i] = fmt.Sprintf("%s:%d:%s", m.Path, m.Line, m.Content)
		} else {
			lines[i] = m.Path + ":" + m.Content
		}
	}
	return strings.Join(lines, "\n")
}

func formatGrepCount(matches []grepMatch, offset, headLimit int) string {
	countMap := make(map[string]int)
	for _, m := range matches {
		countMap[m.Path]++
	}
	paths := make([]string, 0, len(countMap))
	for p := range countMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	totalOccurrences := len(matches)
	totalFiles := len(paths)
	summary := fmt.Sprintf("Found %d total %s across %d %s.",
		totalOccurrences, plural(totalOccurrences, "occurrence", "occurrences"),
		totalFiles, plural(totalFiles, "file", "files"))
	if totalOccurrences == 0 {
		return consts.NoMatchesFound + "\n\n" + summary
	}

	paths = applyPagination(paths, offset, headLimit)
	lines := make([]string, len(paths))
	for i, p := range paths {
		lines[i] = fmt.Sprintf("%s:%d", p, countMap[p])
	}
	return strings.Join(lines, "\n") + "\n\n" + summary
}

// applyPagination drops the first offset items and caps to headLimit (<=0 = no cap).
func applyPagination[T any](items []T, offset, headLimit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return nil
	}
	items = items[offset:]
	if headLimit > 0 && headLimit < len(items) {
		items = items[:headLimit]
	}
	return items
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
