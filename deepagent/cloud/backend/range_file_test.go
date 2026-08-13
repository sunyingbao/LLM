//go:build !windows

package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"eino-cli/deepagent/core/backends"
)

func TestSupportsRangeReadOnlyForLocalBackend(t *testing.T) {
	if !SupportsRangeRead(Config{}) {
		t.Fatal("SupportsRangeRead() = false for default local backend")
	}
	if SupportsRangeRead(Config{Type: TypeAIInfra}) {
		t.Fatal("SupportsRangeRead() = true for ai_infra backend")
	}
}

func TestOpenRangeFileRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	workspace := &Workspace{
		Backend: backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     root,
			VirtualMode: true,
		}),
		WorkDir: root,
	}
	if _, err := OpenRangeFile(context.Background(), workspace, "link.txt"); !errors.Is(err, backends.ErrInvalidPath) {
		t.Fatalf("OpenRangeFile() error = %v, want ErrInvalidPath", err)
	}
}

func TestRangeFileReadRange(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "video.mp4"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := &Workspace{
		Backend: backends.NewSandboxFilesystemBackend(&backends.FilesystemBackendConfig{
			RootDir:     root,
			VirtualMode: true,
		}),
		WorkDir: root,
	}
	file, err := OpenRangeFile(context.Background(), workspace, "video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	got, err := file.ReadRange(2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2345" {
		t.Fatalf("ReadRange() = %q, want %q", got, "2345")
	}
}
