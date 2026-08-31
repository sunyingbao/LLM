package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppConfigOneShotPrompt(t *testing.T) {
	t.Run("from prompt flag", func(t *testing.T) {
		cfg := AppConfig{Prompt: "hello"}
		got, err := cfg.oneShotPrompt(nil, strings.NewReader(""))
		if err != nil {
			t.Fatalf("oneShotPrompt() error = %v", err)
		}
		if got != "hello" {
			t.Fatalf("oneShotPrompt() = %q, want %q", got, "hello")
		}
	})

	t.Run("from stdin", func(t *testing.T) {
		cfg := AppConfig{ReadFromStdin: true}
		got, err := cfg.oneShotPrompt(nil, strings.NewReader("hello from stdin\n"))
		if err != nil {
			t.Fatalf("oneShotPrompt() error = %v", err)
		}
		if got != "hello from stdin" {
			t.Fatalf("oneShotPrompt() = %q, want %q", got, "hello from stdin")
		}
	})

	t.Run("from args", func(t *testing.T) {
		cfg := AppConfig{}
		got, err := cfg.oneShotPrompt([]string{"hello", "world"}, strings.NewReader(""))
		if err != nil {
			t.Fatalf("oneShotPrompt() error = %v", err)
		}
		if got != "hello world" {
			t.Fatalf("oneShotPrompt() = %q, want %q", got, "hello world")
		}
	})

	t.Run("conflicting sources", func(t *testing.T) {
		cfg := AppConfig{Prompt: "hello", ReadFromStdin: true}
		if _, err := cfg.oneShotPrompt([]string{"world"}, strings.NewReader("stdin")); err == nil {
			t.Fatalf("expected conflict error")
		}
	})

	t.Run("empty stdin", func(t *testing.T) {
		cfg := AppConfig{ReadFromStdin: true}
		if _, err := cfg.oneShotPrompt(nil, strings.NewReader("\n")); err == nil {
			t.Fatalf("expected empty stdin error")
		}
	})
}

func TestResolveRoot(t *testing.T) {
	base, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("flag wins", func(t *testing.T) {
		got, err := resolveRoot(".")
		if err != nil {
			t.Fatal(err)
		}
		want, _ := filepath.Abs(".")
		if got != want {
			t.Fatalf("resolveRoot() = %q, want %q", got, want)
		}
	})
	t.Run("environment fallback", func(t *testing.T) {
		t.Setenv("SGADK_ROOT", base)
		got, err := resolveRoot("")
		if err != nil {
			t.Fatal(err)
		}
		if got != base {
			t.Fatalf("resolveRoot() = %q, want %q", got, base)
		}
	})
	t.Run("cwd fallback", func(t *testing.T) {
		t.Setenv("SGADK_ROOT", "")
		got, err := resolveRoot("")
		if err != nil {
			t.Fatal(err)
		}
		want := findRepositoryRoot(base)
		if got != want {
			t.Fatalf("resolveRoot() = %q, want %q", got, want)
		}
	})
}

func TestFindRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "yaml", "config.yaml"), []byte("default_model: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := filepath.Join(root, "deepagent", "cmd", "deepagent")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findRepositoryRoot(start); got != root {
		t.Fatalf("findRepositoryRoot() = %q, want %q", got, root)
	}
}
