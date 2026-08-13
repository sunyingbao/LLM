package backends

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fsbackend-test-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	return dir
}

func TestNewFilesystemBackend(t *testing.T) {
	t.Run("with config", func(t *testing.T) {
		tmpDir := setupTestDir(t)
		backend := NewFilesystemBackend(&FilesystemBackendConfig{
			RootDir:       tmpDir,
			VirtualMode:   true,
			MaxFileSizeMB: 5,
		})
		assert.NotNil(t, backend)
		assert.Equal(t, tmpDir, backend.rootDir)
		assert.True(t, backend.virtualMode)
		assert.Equal(t, 5, backend.maxFileSizeMB)
	})

	t.Run("with empty config", func(t *testing.T) {
		backend := NewFilesystemBackend(&FilesystemBackendConfig{})
		assert.NotNil(t, backend)
		assert.NotEmpty(t, backend.rootDir)
		assert.Equal(t, MaxFileSizeMB, backend.maxFileSizeMB)
	})
}

func writeExecutableFile(t *testing.T, path string, content string) {
	t.Helper()
	err := os.WriteFile(path, []byte(content), 0755)
	require.NoError(t, err)
}

func intPtr(v int) *int {
	return &v
}

func TestFilesystemBackend_Write(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	t.Run("write new file", func(t *testing.T) {
		result, err := backend.Write(ctx, "test.txt", "hello world")
		require.NoError(t, err)
		assert.Equal(t, "test.txt", result.Path)
		assert.Empty(t, result.Error)

		// 验证文件确实存在
		content, err := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(content))
	})

	t.Run("write to nested directory", func(t *testing.T) {
		result, err := backend.Write(ctx, "dir1/dir2/nested.txt", "nested content")
		require.NoError(t, err)
		assert.Equal(t, "dir1/dir2/nested.txt", result.Path)

		// 验证文件存在
		content, err := os.ReadFile(filepath.Join(tmpDir, "dir1/dir2/nested.txt"))
		require.NoError(t, err)
		assert.Equal(t, "nested content", string(content))
	})

	t.Run("overwrite existing file", func(t *testing.T) {
		// 先创建文件
		_, err := backend.Write(ctx, "overwrite.txt", "original")
		require.NoError(t, err)

		// 覆盖
		result, err := backend.Write(ctx, "overwrite.txt", "updated")
		require.NoError(t, err)
		assert.Empty(t, result.Error)

		// 验证内容
		content, err := os.ReadFile(filepath.Join(tmpDir, "overwrite.txt"))
		require.NoError(t, err)
		assert.Equal(t, "updated", string(content))
	})
}

func TestFilesystemBackend_Read(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	// 创建测试文件
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, strings.Repeat("x", 20))
	}
	testContent := strings.Join(lines, "\n")
	err := os.WriteFile(filepath.Join(tmpDir, "read-test.txt"), []byte(testContent), 0644)
	require.NoError(t, err)

	t.Run("read entire file", func(t *testing.T) {
		content, err := backend.Read(ctx, "read-test.txt", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)
	})

	t.Run("read with offset and limit", func(t *testing.T) {
		content, err := backend.Read(ctx, "read-test.txt", intPtr(10), intPtr(5))
		require.NoError(t, err)
		assert.Equal(t, strings.Join(lines[10:15], "\n")+"\n", content)
	})

	t.Run("negative offset clamps to start", func(t *testing.T) {
		content, err := backend.Read(ctx, "read-test.txt", intPtr(-100), intPtr(2))
		require.NoError(t, err)
		assert.Equal(t, strings.Join(lines[:2], "\n")+"\n", content)
	})

	t.Run("read non-existent file", func(t *testing.T) {
		_, err := backend.Read(ctx, "nonexistent.txt", nil, nil)
		assert.ErrorIs(t, err, ErrFileNotFound)
	})

	t.Run("read directory", func(t *testing.T) {
		err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
		require.NoError(t, err)

		_, err = backend.Read(ctx, "subdir", nil, nil)
		assert.ErrorIs(t, err, ErrIsDirectory)
	})
}

func TestFilesystemBackend_Edit(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	t.Run("edit single occurrence", func(t *testing.T) {
		err := os.WriteFile(filepath.Join(tmpDir, "edit1.txt"), []byte("hello world"), 0644)
		require.NoError(t, err)

		result, err := backend.Edit(ctx, "edit1.txt", "world", "universe", false)
		require.NoError(t, err)
		assert.Equal(t, 1, result.Occurrences)

		content, _ := os.ReadFile(filepath.Join(tmpDir, "edit1.txt"))
		assert.Equal(t, "hello universe", string(content))
	})

	t.Run("edit multiple with replaceAll", func(t *testing.T) {
		err := os.WriteFile(filepath.Join(tmpDir, "edit2.txt"), []byte("foo bar foo baz foo"), 0644)
		require.NoError(t, err)

		result, err := backend.Edit(ctx, "edit2.txt", "foo", "qux", true)
		require.NoError(t, err)
		assert.Equal(t, 3, result.Occurrences)

		content, _ := os.ReadFile(filepath.Join(tmpDir, "edit2.txt"))
		assert.Equal(t, "qux bar qux baz qux", string(content))
	})

	t.Run("edit multiple without replaceAll", func(t *testing.T) {
		err := os.WriteFile(filepath.Join(tmpDir, "edit3.txt"), []byte("foo bar foo"), 0644)
		require.NoError(t, err)

		result, err := backend.Edit(ctx, "edit3.txt", "foo", "qux", false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "找到 2 个匹配")
		assert.Equal(t, 2, result.Occurrences)
	})

	t.Run("edit non-existent string", func(t *testing.T) {
		err := os.WriteFile(filepath.Join(tmpDir, "edit4.txt"), []byte("hello world"), 0644)
		require.NoError(t, err)

		result, err := backend.Edit(ctx, "edit4.txt", "notfound", "replacement", false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "未找到")
		assert.Equal(t, 0, result.Occurrences)
	})

	t.Run("edit non-existent file", func(t *testing.T) {
		result, err := backend.Edit(ctx, "nonexistent.txt", "foo", "bar", false)
		assert.NoError(t, err)
		assert.Equal(t, ErrFileNotFound, result.Error)
	})
}

func TestFilesystemBackend_SupportsApplyPatch(t *testing.T) {
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	t.Run("env unset", func(t *testing.T) {
		t.Setenv(applyPatchBinEnv, "")
		assert.False(t, backend.SupportsApplyPatch())
	})

	t.Run("missing binary", func(t *testing.T) {
		t.Setenv(applyPatchBinEnv, filepath.Join(tmpDir, "missing-apply-patch"))
		assert.False(t, backend.SupportsApplyPatch())
	})

	t.Run("existing executable", func(t *testing.T) {
		bin := filepath.Join(tmpDir, "fake-apply-patch.sh")
		writeExecutableFile(t, bin, "#!/bin/sh\ncat >/dev/null\n")
		t.Setenv(applyPatchBinEnv, bin)
		assert.True(t, backend.SupportsApplyPatch())
	})
}

func TestFilesystemBackend_ApplyPatchUsesConfiguredBinary(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	err := os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("before\n"), 0644)
	require.NoError(t, err)

	bin := filepath.Join(tmpDir, "fake-apply-patch.sh")
	writeExecutableFile(t, bin, `#!/bin/sh
patch="$(cat)"
printf '%s' "$patch" > .seen_patch
pwd > .seen_pwd
printf 'patched\n' > hello.txt
echo "applied"
`)
	t.Setenv(applyPatchBinEnv, bin)

	out, err := backend.ApplyPatch(ctx, "*** Begin Patch\n*** Update File: hello.txt\n@@\n-before\n+patched\n*** End Patch\n")
	require.NoError(t, err)
	assert.Contains(t, out, "stdout>")
	assert.Contains(t, out, "applied")

	content, err := os.ReadFile(filepath.Join(tmpDir, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "patched\n", string(content))

	seenPatch, err := os.ReadFile(filepath.Join(tmpDir, ".seen_patch"))
	require.NoError(t, err)
	assert.Contains(t, string(seenPatch), "*** Update File: hello.txt")

	seenPwd, err := os.ReadFile(filepath.Join(tmpDir, ".seen_pwd"))
	require.NoError(t, err)
	assert.Equal(t, tmpDir+"\n", string(seenPwd))
}

func TestFilesystemBackend_ApplyPatchErrorIncludesToolOutput(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	bin := filepath.Join(tmpDir, "fake-apply-patch.sh")
	writeExecutableFile(t, bin, `#!/bin/sh
echo "Invalid patch: The last line of the patch must be '*** End Patch'" >&2
exit 1
`)
	t.Setenv(applyPatchBinEnv, bin)

	out, err := backend.ApplyPatch(ctx, "*** Begin Patch\n")
	require.Error(t, err)
	assert.Empty(t, out)
	assert.Contains(t, err.Error(), "exit status 1")
	assert.Contains(t, err.Error(), "stderr>")
	assert.Contains(t, err.Error(), "Invalid patch: The last line of the patch must be '*** End Patch'")
	assert.Contains(t, err.Error(), "stderr end")
}

func TestFilesystemBackend_LsInfo(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	// 创建测试结构
	os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("content1"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("content2"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "subdir/file3.txt"), []byte("content3"), 0644)

	t.Run("list root directory", func(t *testing.T) {
		files, err := backend.LsInfo(ctx, ".")
		require.NoError(t, err)

		var names []string
		for _, f := range files {
			names = append(names, filepath.Base(f.Path))
		}

		assert.Contains(t, names, "file1.txt")
		assert.Contains(t, names, "file2.txt")
		assert.Contains(t, names, "subdir")
	})

	t.Run("list subdirectory", func(t *testing.T) {
		files, err := backend.LsInfo(ctx, "subdir")
		require.NoError(t, err)
		assert.Len(t, files, 1)
		assert.Equal(t, "file3.txt", filepath.Base(files[0].Path))
	})

	t.Run("list non-existent directory", func(t *testing.T) {
		_, err := backend.LsInfo(ctx, "nonexistent")
		assert.ErrorIs(t, err, ErrFileNotFound)
	})

	t.Run("list file path", func(t *testing.T) {
		_, err := backend.LsInfo(ctx, "file1.txt")
		assert.ErrorIs(t, err, ErrInvalidPath)
	})

	t.Run("directories sorted first", func(t *testing.T) {
		files, err := backend.LsInfo(ctx, ".")
		require.NoError(t, err)

		// 第一个应该是目录
		foundFile := false
		for _, f := range files {
			if !f.IsDir {
				foundFile = true
			}
			if foundFile && f.IsDir {
				t.Error("Directory found after file - sorting is incorrect")
			}
		}
	})
}

func TestFilesystemBackend_GrepRaw(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	// 创建测试文件
	os.WriteFile(filepath.Join(tmpDir, "grep1.txt"), []byte("line with ERROR here\nline without\nERROR again"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "grep2.go"), []byte("func test() {\n\treturn nil\n}"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "subdir/grep3.txt"), []byte("nested ERROR file"), 0644)

	t.Run("simple pattern search", func(t *testing.T) {
		matches, err := backend.GrepRaw(ctx, "ERROR", ".", "")
		require.NoError(t, err)
		assert.Equal(t, 3, len(matches))
	})

	t.Run("regex pattern search", func(t *testing.T) {
		matches, err := backend.GrepRaw(ctx, "func.*\\(\\)", ".", "")
		require.NoError(t, err)
		assert.Len(t, matches, 1)
	})

	t.Run("search with glob filter", func(t *testing.T) {
		matches, err := backend.GrepRaw(ctx, "ERROR", ".", "*.txt")
		require.NoError(t, err)
		for _, m := range matches {
			assert.True(t, strings.HasSuffix(m.Path, ".txt"))
		}
	})

	t.Run("invalid regex", func(t *testing.T) {
		_, err := backend.GrepRaw(ctx, "[invalid", ".", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "正则表达式")
	})
}

func TestFilesystemBackend_GlobInfo(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	// 创建测试文件
	os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("content1"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("content2"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file1.go"), []byte("package main"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "subdir/file3.txt"), []byte("content3"), 0644)

	t.Run("match txt files", func(t *testing.T) {
		files, err := backend.GlobInfo(ctx, "*.txt", ".")
		require.NoError(t, err)
		for _, f := range files {
			assert.True(t, strings.HasSuffix(f.Path, ".txt"))
		}
	})

	t.Run("match go files", func(t *testing.T) {
		files, err := backend.GlobInfo(ctx, "*.go", ".")
		require.NoError(t, err)
		assert.Len(t, files, 1)
	})
}

func TestFilesystemBackend_UploadDownloadFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	t.Run("upload files", func(t *testing.T) {
		files := []struct {
			Path    string
			Content []byte
		}{
			{Path: "upload1.txt", Content: []byte("upload content 1")},
			{Path: "upload2.txt", Content: []byte("upload content 2")},
		}

		responses, err := backend.UploadFiles(ctx, files)
		require.NoError(t, err)
		assert.Len(t, responses, 2)

		for i, resp := range responses {
			assert.Equal(t, files[i].Path, resp.Path)
			assert.Empty(t, resp.Error)
		}

		// 验证文件内容
		content1, _ := os.ReadFile(filepath.Join(tmpDir, "upload1.txt"))
		assert.Equal(t, "upload content 1", string(content1))
	})

	t.Run("download files", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmpDir, "download1.txt"), []byte("download content 1"), 0644)
		os.WriteFile(filepath.Join(tmpDir, "download2.txt"), []byte("download content 2"), 0644)

		responses, err := backend.DownloadFiles(ctx, []string{"download1.txt", "download2.txt"})
		require.NoError(t, err)
		assert.Len(t, responses, 2)

		assert.Equal(t, "download1.txt", responses[0].Path)
		assert.Equal(t, "download content 1", string(responses[0].Content))
	})

	t.Run("download non-existent file", func(t *testing.T) {
		responses, err := backend.DownloadFiles(ctx, []string{"nonexistent.txt"})
		require.NoError(t, err)
		assert.Equal(t, ErrFileNotFound, responses[0].Error)
	})

	t.Run("download directory", func(t *testing.T) {
		os.MkdirAll(filepath.Join(tmpDir, "download-dir"), 0755)

		responses, err := backend.DownloadFiles(ctx, []string{"download-dir"})
		require.NoError(t, err)
		assert.Equal(t, ErrIsDirectory, responses[0].Error)
	})

	t.Run("download symlink", func(t *testing.T) {
		target := filepath.Join(tmpDir, "download-target.txt")
		require.NoError(t, os.WriteFile(target, []byte("secret"), 0644))
		link := filepath.Join(tmpDir, "download-link.txt")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}

		responses, err := backend.DownloadFiles(ctx, []string{"download-link.txt"})
		require.NoError(t, err)
		assert.Equal(t, ErrInvalidPath, responses[0].Error)
	})
}

func TestFilesystemBackend_PathTraversal(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	t.Run("reject path traversal in read", func(t *testing.T) {
		_, err := backend.Read(ctx, "../../../etc/passwd", nil, nil)
		assert.ErrorIs(t, err, ErrInvalidPath)
	})

	t.Run("reject path traversal in write", func(t *testing.T) {
		result, err := backend.Write(ctx, "../../../tmp/evil.txt", "malicious content")
		assert.NoError(t, err) // Write returns error in result, not as return value
		assert.Equal(t, ErrInvalidPath, result.Error)
	})

	t.Run("reject path traversal in ls", func(t *testing.T) {
		_, err := backend.LsInfo(ctx, "../..")
		assert.ErrorIs(t, err, ErrInvalidPath)
	})
}

func TestFilesystemBackend_VirtualMode(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)

	t.Run("virtual mode restricts to root", func(t *testing.T) {
		backend := NewFilesystemBackend(&FilesystemBackendConfig{
			RootDir:     tmpDir,
			VirtualMode: true,
		})

		// 尝试访问根目录外的路径
		_, err := backend.Read(ctx, "/etc/passwd", nil, nil)
		// 在虚拟模式下，绝对路径被视为相对于 rootDir
		assert.ErrorIs(t, err, ErrFileNotFound) // 文件不存在于 tmpDir/etc/passwd
	})

	t.Run("virtual mode accepts absolute paths already inside root", func(t *testing.T) {
		backend := NewFilesystemBackend(&FilesystemBackendConfig{
			RootDir:     tmpDir,
			VirtualMode: true,
		})
		target := filepath.Join(tmpDir, "absolute.txt")

		result, err := backend.Write(ctx, target, "inside")
		require.NoError(t, err)
		require.Empty(t, result.Error)
		content, err := os.ReadFile(target)
		require.NoError(t, err)
		assert.Equal(t, "inside", string(content))
		_, err = os.Stat(filepath.Join(tmpDir, strings.TrimLeft(target, string(filepath.Separator))))
		assert.True(t, os.IsNotExist(err), "absolute path must not be nested under root")
	})

	t.Run("non-virtual mode allows absolute paths", func(t *testing.T) {
		backend := NewFilesystemBackend(&FilesystemBackendConfig{
			RootDir:     tmpDir,
			VirtualMode: false,
		})

		// 创建一个临时文件用于测试
		tempFile, err := os.CreateTemp("", "test-*.txt")
		require.NoError(t, err)
		defer os.Remove(tempFile.Name())

		_, err = tempFile.WriteString("test content")
		require.NoError(t, err)
		tempFile.Close()

		// 应该能够读取绝对路径
		content, err := backend.Read(ctx, tempFile.Name(), nil, nil)
		require.NoError(t, err)
		assert.Contains(t, content, "test content")
	})
}

func TestSandboxFilesystemBackend(t *testing.T) {
	ctx := context.Background()
	tmpDir := setupTestDir(t)
	backend := NewSandboxFilesystemBackend(&FilesystemBackendConfig{
		RootDir:     tmpDir,
		VirtualMode: true,
	})

	t.Run("create sandbox backend", func(t *testing.T) {
		assert.NotNil(t, backend)
		assert.NotEmpty(t, backend.ID())
		assert.True(t, strings.HasPrefix(backend.ID(), "sandbox-"))
	})

	t.Run("execute simple command", func(t *testing.T) {
		result, err := backend.Execute(ctx, "echo 'hello world'")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode)
		assert.Contains(t, result.Output, "hello world")
	})

	t.Run("execute command with exit code", func(t *testing.T) {
		result, err := backend.Execute(ctx, "exit 42")
		require.NoError(t, err)
		assert.Equal(t, 42, result.ExitCode)
	})

	t.Run("execute in root directory", func(t *testing.T) {
		// 创建一个测试文件
		os.WriteFile(filepath.Join(tmpDir, "marker.txt"), []byte("marker"), 0644)

		result, err := backend.Execute(ctx, "ls marker.txt")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode)
		assert.Contains(t, result.Output, "marker.txt")
	})

	t.Run("execute command with stderr", func(t *testing.T) {
		result, err := backend.Execute(ctx, "echo 'error' >&2")
		require.NoError(t, err)
		assert.Contains(t, result.Output, "error")
	})

	t.Run("execute command request with absolute root workdir", func(t *testing.T) {
		result, err := backend.ExecuteCommand(ctx, CommandRequest{
			Command: "printf '%s' \"$PWD\"",
			WorkDir: tmpDir,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode)
		assert.Equal(t, tmpDir, result.Output)
	})

	t.Run("execute command request with workdir env and output limit", func(t *testing.T) {
		err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(tmpDir, "subdir", "marker.txt"), []byte("marker"), 0644)
		require.NoError(t, err)

		result, err := backend.ExecuteCommand(ctx, CommandRequest{
			Command:        "printf '%s:%s' \"$TEST_VALUE\" \"$(ls marker.txt)\"",
			WorkDir:        "/subdir",
			Env:            map[string]string{"TEST_VALUE": "from-env"},
			MaxOutputBytes: 12,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode)
		assert.Equal(t, "from-env:mar", result.Output)
		assert.True(t, result.Truncated)
	})

	t.Run("execute command request timeout", func(t *testing.T) {
		result, err := backend.ExecuteCommand(ctx, CommandRequest{
			Command: "sleep 1",
			Timeout: 10 * time.Millisecond,
		})
		require.NoError(t, err)
		assert.True(t, result.TimedOut)
	})

	t.Run("execute command context cancel kills child process", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("process group cancellation is Unix-only")
		}
		script := filepath.Join(tmpDir, "cancel-child-"+time.Now().Format("150405.000000000")+".sh")
		started := filepath.Join(tmpDir, "child-started")
		done := filepath.Join(tmpDir, "child-done")
		content := "#!/bin/sh\n" +
			"echo started > " + shellQuote(started) + "\n" +
			"sleep 30\n" +
			"echo done > " + shellQuote(done) + "\n"
		require.NoError(t, os.WriteFile(script, []byte(content), 0755))

		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		resultCh := make(chan error, 1)
		go func() {
			_, err := backend.ExecuteCommand(cancelCtx, CommandRequest{Command: shellQuote(script)})
			resultCh <- err
		}()

		startDeadline := time.NewTimer(15 * time.Second)
		defer startDeadline.Stop()
		startPoll := time.NewTicker(10 * time.Millisecond)
		defer startPoll.Stop()
	startedLoop:
		for {
			select {
			case err := <-resultCh:
				require.NoError(t, err, "ExecuteCommand returned before child start marker")
				t.Fatal("ExecuteCommand returned before child start marker")
			case <-startDeadline.C:
				t.Fatal("child process did not start within 15 seconds")
			case <-startPoll.C:
				if _, err := os.Stat(started); err == nil {
					break startedLoop
				}
			}
		}
		cancel()

		select {
		case err := <-resultCh:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("ExecuteCommand did not return after context cancel")
		}

		assert.NoFileExists(t, done)
		require.Eventually(t, func() bool {
			return !processListContains(t, filepath.Base(script))
		}, 2*time.Second, 20*time.Millisecond)
	})

	t.Run("sandbox backend inherits filesystem operations", func(t *testing.T) {
		// 测试 SandboxFilesystemBackend 继承自 FilesystemBackend 的方法
		_, err := backend.Write(ctx, "sandbox-test.txt", "sandbox content")
		require.NoError(t, err)

		content, err := backend.Read(ctx, "sandbox-test.txt", nil, nil)
		require.NoError(t, err)
		assert.Contains(t, content, "sandbox content")
	})

	t.Run("sandbox backend inherits apply patch capability", func(t *testing.T) {
		bin := filepath.Join(tmpDir, "fake-apply-patch.sh")
		writeExecutableFile(t, bin, "#!/bin/sh\ncat >/dev/null\n")
		t.Setenv(applyPatchBinEnv, bin)
		assert.True(t, backend.SupportsApplyPatch())
	})
}

func processListContains(t *testing.T, needle string) bool {
	t.Helper()
	out, err := exec.Command("ps", "-eo", "pid=,args=").Output()
	if err != nil {
		t.Fatalf("ps failed: %v", err)
	}
	return strings.Contains(string(out), needle)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// TestFilesystemBackend_Interface 确保 FilesystemBackend 实现了 Backend 接口
func TestFilesystemBackend_Interface(t *testing.T) {
	var _ Backend = (*FilesystemBackend)(nil)
	var _ ApplyPatchBackend = (*FilesystemBackend)(nil)
	var _ SandboxBackend = (*SandboxFilesystemBackend)(nil)
}
