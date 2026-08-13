package workspace

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const maxProjectNameLen = 128

type Resolver struct {
	Root string
}

func (r Resolver) Resolve(uid int64, projectName string) (string, string, error) {
	if uid <= 0 {
		return "", "", fmt.Errorf("uid is required")
	}
	name, err := CleanProjectName(projectName)
	if err != nil {
		return "", "", err
	}
	root := strings.TrimSpace(r.Root)
	if root == "" {
		return "", "", fmt.Errorf("workspace root is required")
	}
	return name, filepath.Join(root, strconv.FormatInt(uid, 10), name), nil
}

func CleanProjectName(projectName string) (string, error) {
	name := strings.TrimSpace(projectName)
	if name == "" {
		return "", fmt.Errorf("project_name is required")
	}
	if len(name) > maxProjectNameLen {
		return "", fmt.Errorf("project_name is too long")
	}
	if name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("project_name must be a single path segment")
	}
	return name, nil
}
