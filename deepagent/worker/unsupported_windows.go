//go:build windows

package agentworker

import "errors"

var ErrUnsupportedPlatform = errors.New("agentworker: unsupported on windows")
