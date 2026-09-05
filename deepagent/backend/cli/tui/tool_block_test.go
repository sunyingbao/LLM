package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func TestFormatArgsLineWriteFile(t *testing.T) {
	got := formatArgsLine("write_file", `{"path":"a.md","content":"hello"}`, 60)
	if got != "a.md, 5 bytes" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatArgsLineEditNewString(t *testing.T) {
	got := formatArgsLine("Edit", `{"file_path":"a.go","new_string":"abc"}`, 60)
	if got != "a.go, 3 bytes" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatArgsLineUnknownToolFallback(t *testing.T) {
	got := formatArgsLine("Unknown", "1234567890", 4)
	if got != "1234…" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderToolBlockCollapsedFooter(t *testing.T) {
	block := &toolBlock{
		name:      "Bash",
		argsLine:  "git log --oneline -30",
		lines:     []string{"1", "2", "3", "4", "5", "6", "7"},
		collapsed: true,
	}

	got := renderToolBlock(block, 5)
	for _, want := range []string{"•", "Ran Bash(git log --oneline -30)", "  ⎿  1", "… +2 lines (ctrl+o to expand)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered block missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "     6") {
		t.Fatalf("collapsed block should hide line 6:\n%s", got)
	}
}

func TestRenderToolBlockExpanded(t *testing.T) {
	block := &toolBlock{
		name:      "Bash",
		argsLine:  "pwd",
		lines:     []string{"1", "2", "3"},
		collapsed: false,
	}

	got := renderToolBlock(block, 1)
	if !strings.Contains(got, "     3") || strings.Contains(got, "ctrl+o") {
		t.Fatalf("expanded render wrong:\n%s", got)
	}
}

func TestRenderToolBlockShortNoFooter(t *testing.T) {
	block := &toolBlock{
		name:      "Bash",
		argsLine:  "pwd",
		lines:     []string{"1", "2", "3"},
		collapsed: true,
	}

	got := renderToolBlock(block, 5)
	if strings.Contains(got, "ctrl+o") {
		t.Fatalf("short block should not show footer:\n%s", got)
	}
}

func TestLatestCollapsibleToolBlockPicksLastLong(t *testing.T) {
	m := &Model{
		toolPreviewLines: 2,
		toolBlocks: []*toolBlock{
			{lines: []string{"1", "2", "3"}},
			{lines: []string{"1"}},
			{lines: []string{"1", "2", "3", "4"}},
		},
	}

	if got := getLatestCollapsibleToolBlock(m); got != m.toolBlocks[2] {
		t.Fatalf("expected latest long block, got %#v", got)
	}
}

func TestLatestCollapsibleToolBlockNoneCollapsible(t *testing.T) {
	m := &Model{
		toolPreviewLines: 5,
		toolBlocks:       []*toolBlock{{lines: []string{"1"}}, {lines: []string{"1", "2"}}},
	}

	if got := getLatestCollapsibleToolBlock(m); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
}

func TestHandleKeyCtrlOToggles(t *testing.T) {
	m := &Model{
		toolPreviewLines: 1,
		toolBlocks:       []*toolBlock{{id: 1, lines: []string{"1", "2"}, collapsed: true}},
		viewport:         viewport.New(80, 10),
	}

	_, _ = applyKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.toolBlocks[0].collapsed {
		t.Fatal("Ctrl-O should expand latest block")
	}
}

func TestHandleKeyCtrlONoBlocksSetsHint(t *testing.T) {
	m := &Model{toolPreviewLines: 1, viewport: viewport.New(80, 10)}

	_, cmd := applyKey(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.footerHint != "nothing to expand" {
		t.Fatalf("unexpected footer hint %q", m.footerHint)
	}
	if cmd == nil {
		t.Fatal("expected expiry command")
	}
}

func TestFooterHintExpires(t *testing.T) {
	m := &Model{footerHint: "nothing to expand"}
	_, _ = m.Update(footerHintExpiredMsg{})
	if m.footerHint != "" {
		t.Fatalf("footer hint should expire, got %q", m.footerHint)
	}
}

func TestToolBlockDefaults(t *testing.T) {
	if defaultToolPreviewLines != 5 || defaultToolArgsMaxChars != 60 {
		t.Fatalf("unexpected defaults: preview=%d args=%d", defaultToolPreviewLines, defaultToolArgsMaxChars)
	}
}

func TestToolBlockStaysLiveBeforeFinalAssistant(t *testing.T) {
	m := &Model{
		toolPreviewLines: defaultToolPreviewLines,
		viewport:         viewport.New(80, 10),
		width:            80,
		height:           24,
		toolBlocks: []*toolBlock{{
			id:       1,
			name:     "web_search",
			argsLine: `{"query":"x"}`,
			lines:    []string{"result"},
		}},
		messages: []chatMessage{
			{Role: "user", Content: "question"},
			{Role: "tool-block", Content: "[tool:#1]"},
		},
		flushedMsgCount: 1,
	}

	pushMessage(m, "assistant", "answer")
	live := getLiveMessages(m)
	if len(live) != 2 || live[0].Role != "tool-block" || live[1].Role != "assistant" {
		t.Fatalf("tool block must stay live before final assistant, got %#v", live)
	}
	if len(m.pendingScrollback) != 0 {
		t.Fatalf("tool block should not be flushed before assistant renders, got %d pending", len(m.pendingScrollback))
	}
}

func TestClearResetsToolBlocks(t *testing.T) {
	m := &Model{
		rt:               stubRuntime{},
		width:            80,
		height:           24,
		modelName:        "stub-model",
		cwd:              ".",
		viewport:         viewport.New(80, 10),
		toolBlocks:       []*toolBlock{{id: 1, lines: []string{"x"}}},
		lastSeenMsgCount: 2,
		toolBlockSeq:     1,
		footerHint:       "nothing to expand",
	}

	_, handled := applyBuiltin(m, "/clear")
	if !handled {
		t.Fatal("/clear should be handled")
	}
	if len(m.toolBlocks) != 0 || m.lastSeenMsgCount != 0 || m.toolBlockSeq != 0 || m.footerHint != "" {
		t.Fatalf("clear did not reset tool state: %#v", m)
	}
}
