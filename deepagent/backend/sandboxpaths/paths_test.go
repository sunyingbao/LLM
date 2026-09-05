package sandboxpaths

import (
	"os"
	"path/filepath"
	"testing"

	"eino-cli/deepagent/backend/config"
	"github.com/stretchr/testify/require"
)

func TestGetHostPath(t *testing.T) {
	rootDir := t.TempDir()
	mappings := []MountMapping{
		{VirtualPath: "/mnt", HostPath: rootDir},
		{VirtualPath: "/mnt/repo", HostPath: filepath.Join(rootDir, "repo")},
	}

	hostPath, err := GetHostPath(mappings, "/other/file")
	require.NoError(t, err)
	require.Equal(t, "/other/file", hostPath)

	hostPath, err = GetHostPath(mappings, "/mnt/repo")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(rootDir, "repo"), hostPath)

	hostPath, err = GetHostPath(mappings, "/mnt/repo/dir/file")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(rootDir, "repo", "dir", "file"), hostPath)

	_, err = GetHostPath(mappings, "/mnt/repo/../../outside")
	require.ErrorContains(t, err, "path escapes mount root")
}

func TestVirtualPathMatching(t *testing.T) {
	relative, ok := getPathRelativeToVirtualRoot("/", "/path")
	require.True(t, ok)
	require.Equal(t, "path", relative)

	relative, ok = getPathRelativeToVirtualRoot("/mnt/repo/", "/mnt/repo")
	require.True(t, ok)
	require.Empty(t, relative)

	relative, ok = getPathRelativeToVirtualRoot("/mnt/repo", "/mnt/repo/file")
	require.True(t, ok)
	require.Equal(t, "file", relative)

	_, ok = getPathRelativeToVirtualRoot("/mnt/repo", "/mnt/repository")
	require.False(t, ok)

	require.True(t, isUnder("/root", "/root"))
	require.True(t, isUnder("/root/child", "/root"))
	require.False(t, isUnder("/root-other", "/root"))
}

func TestBuildMountMappings(t *testing.T) {
	rootDir := t.TempDir()
	restore := config.SetRootDirForTest(rootDir)
	t.Cleanup(restore)

	mappings, err := BuildMountMappings("session")
	require.NoError(t, err)
	require.Len(t, mappings, 4)
	require.Empty(t, GetSkillsHostPath())

	skillsDir := filepath.Join(rootDir, "backend", "skills")
	require.NoError(t, os.MkdirAll(skillsDir, 0o755))
	mappings, err = BuildMountMappings("session")
	require.NoError(t, err)
	require.Len(t, mappings, 5)
	require.Equal(t, VirtualPathPrefixSkills, mappings[0].VirtualPath)
	require.True(t, mappings[0].ReadOnly)
	require.Equal(t, skillsDir, GetSkillsHostPath())

	rootFile := filepath.Join(t.TempDir(), "root-file")
	require.NoError(t, os.WriteFile(rootFile, []byte("file"), 0o600))
	restore()
	restore = config.SetRootDirForTest(rootFile)
	t.Cleanup(restore)
	_, err = BuildMountMappings("session")
	require.Error(t, err)
}
