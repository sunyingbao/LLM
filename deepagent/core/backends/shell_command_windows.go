//go:build windows

package backends

import "os/exec"

func configureShellCommandCancel(cmd *exec.Cmd) {}
