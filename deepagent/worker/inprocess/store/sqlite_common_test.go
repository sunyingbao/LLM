package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenSQLiteEnablesWAL(t *testing.T) {
	db, err := openSQLite(filepath.Join(t.TempDir(), "wal.sqlite"))
	if err != nil {
		t.Fatalf("openSQLite() error = %v", err)
	}

	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("query journal_mode error = %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		t.Fatalf("query busy_timeout error = %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}
}
