//go:build windows

package byted_sandbox

import "errors"

var ErrUnsupportedPlatform = errors.New("byted_sandbox: unsupported on windows")
