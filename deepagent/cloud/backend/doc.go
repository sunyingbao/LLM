//go:build !windows

// Package backend opens the workspace computer used by CloudAgent threads.
//
// The supported workspace backends are intentionally narrow:
// local uses the host filesystem and shell for local development, and ai_infra
// uses ByteDance AI infra sandbox for online deployment. Project names are
// business grouping keys; sandbox identity is backend-specific.
package backend
