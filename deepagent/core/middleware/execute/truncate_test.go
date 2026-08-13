package execute

import (
	"strings"
	"testing"
)

func TestTruncateHeadTailNoop(t *testing.T) {
	if got := truncateHeadTail("short", 0); got != "short" {
		t.Fatalf("truncateHeadTail(max=0) = %q, want short", got)
	}
	if got := truncateHeadTail("short", 10); got != "short" {
		t.Fatalf("truncateHeadTail(short) = %q, want short", got)
	}
}

func TestTruncateHeadTailSmallLimit(t *testing.T) {
	got := truncateHeadTail("abcdefghijklmnopqrstuvwxyz", 5)
	if got != "abcde" {
		t.Fatalf("truncateHeadTail(small limit) = %q, want abcde", got)
	}
}

func TestTruncateHeadTailKeepsHeadAndTail(t *testing.T) {
	input := "0123456789abcdefghijklmnopqrstuvwxyz"
	got := truncateHeadTail(input, 34)
	marker := "\n... output truncated ...\n"
	if len(got) != 34 {
		t.Fatalf("len(got) = %d, want 34; got %q", len(got), got)
	}
	if !strings.Contains(got, marker) {
		t.Fatalf("got %q, want marker", got)
	}
	if !strings.HasPrefix(got, "01234") {
		t.Fatalf("got %q, want head prefix", got)
	}
	if !strings.HasSuffix(got, "xyz") {
		t.Fatalf("got %q, want tail suffix", got)
	}
}
