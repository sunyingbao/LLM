package handler

import "testing"

func TestParseHTTPRange(t *testing.T) {
	start, end, err := parseHTTPRange("bytes=0-1023", 2048)
	if err != nil {
		t.Fatalf("parseHTTPRange() error = %v", err)
	}
	if start != 0 || end != 1023 {
		t.Fatalf("range = %d-%d, want 0-1023", start, end)
	}
}

func TestParseHTTPRangeSuffix(t *testing.T) {
	start, end, err := parseHTTPRange("bytes=-512", 2048)
	if err != nil {
		t.Fatalf("parseHTTPRange() error = %v", err)
	}
	if start != 1536 || end != 2047 {
		t.Fatalf("range = %d-%d, want 1536-2047", start, end)
	}
}

func TestParseHTTPRangeRejectsUnsatisfiable(t *testing.T) {
	if _, _, err := parseHTTPRange("bytes=2048-", 1024); err == nil {
		t.Fatal("parseHTTPRange() error = nil, want error")
	}
}
