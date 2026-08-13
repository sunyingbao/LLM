// Package cloudagent documents the reusable service-side CloudAgent packages.
//
// Subpackages carry the real implementation:
//   - protocol defines the structured worker input and timeline event payloads.
//   - worker builds the default DeepAgent-based CloudAgent worker.
//   - worker/thread adapts DeepAgentThread to agentworker.AgentThread.
//   - worker/policy provides default approval reuse policies.
//
// The stable entrypoint for the default CloudAgent worker is cloudagent/worker.
// Lower-level custom runtimes should use agentworker/cloud directly. The
// cmd/cloud_agent services are reference service wiring, not public SDK
// packages.
package cloudagent
