package webui

import (
	"strings"
	"testing"
)

func TestCodexWorkspaceStaticShell(t *testing.T) {
	for _, name := range []string{
		"static/index.html",
		"static/components/app_shell.js",
		"static/styles/tokens.css",
		"static/styles/layout.css",
		"static/api/client.js",
	} {
		if _, err := Static.ReadFile(name); err != nil {
			t.Fatalf("Static.ReadFile(%q) error = %v", name, err)
		}
	}
}

func TestCodexWorkspaceIndexHasSemanticRegions(t *testing.T) {
	index, err := Static.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("Static.ReadFile(static/index.html) error = %v", err)
	}
	for _, expected := range []string{
		`aria-label="Projects and tasks"`,
		`aria-label="Task conversation"`,
		`aria-label="Task inspector"`,
		`href="/static/styles/index.css"`,
		`src="/static/app.js"`,
	} {
		if !strings.Contains(string(index), expected) {
			t.Fatalf("static/index.html missing %q", expected)
		}
	}
}

func TestWebUIHasFavicon(t *testing.T) {
	index, err := Static.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("Static.ReadFile(static/index.html) error = %v", err)
	}
	icon, err := Static.ReadFile("static/favicon.svg")
	if err != nil {
		t.Fatalf("Static.ReadFile(static/favicon.svg) error = %v", err)
	}
	for _, expected := range []string{
		`rel="icon"`,
		`href="/favicon.svg"`,
		`<svg xmlns="http://www.w3.org/2000/svg"`,
	} {
		if !strings.Contains(string(index)+"\n"+string(icon), expected) {
			t.Fatalf("webui favicon assets missing %q", expected)
		}
	}
}
