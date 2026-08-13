package main

import (
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
