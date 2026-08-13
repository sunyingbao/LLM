// Package memory implements the core user-level long-term memory loop.
//
// The package owns the closed-loop mechanics that can be tested before worker
// integration: Stage 1 extraction from agentthread history, Stage 1 output
// storage, sandbox workspace sync, Stage 2 consolidation, baseline tracking,
// and summary reading. Worker scheduling, product APIs, and cloud-agent
// lifecycle integration stay outside this package.
package memory
