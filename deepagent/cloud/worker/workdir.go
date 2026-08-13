//go:build !windows

package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// sessionWorkDir is the default workdir policy for threads without an explicit
// cwd in their thread profile.
func sessionWorkDir(root string, userID int64, sessionID string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		root = DefaultWorkDirRoot
	}
	session := safePathSegment(sessionID, 96)
	if session == "" {
		session = "sessionless"
	}
	out := filepath.Join(root, fmt.Sprintf("u%d", userID), session)
	abs, err := filepath.Abs(out)
	if err != nil {
		return out
	}
	return abs
}

func safePathSegment(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "session"
	}
	if limit <= 0 || len(out) <= limit {
		return out
	}
	sum := sha256.Sum256([]byte(out))
	suffix := hex.EncodeToString(sum[:])[:8]
	headLimit := limit - len(suffix) - 1
	if headLimit < 1 {
		return suffix[:limit]
	}
	return out[:headLimit] + "_" + suffix
}
