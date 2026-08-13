//go:build !windows

// Package worker builds and runs the default CloudAgent worker.
//
// This package is the SDK entrypoint for a business that wants a DeepAgent-based
// worker connected to Agent Coordinator. The caller provides only business
// materials: model instances, prompts, role policy, workspace backend config,
// history storage, checkpoint storage, and an Agent Coordinator client. The
// package owns the standard
// thread construction path and adapts that thread to agentworker/cloud.
//
// Start from Config and Deps. Config describes worker/runtime behavior; Deps
// supplies external systems. New builds a Worker, and Run is the one-call
// helper for services that do not need to keep the Worker value.
//
// If a service needs to implement a completely different AgentThread contract,
// it should use agentworker/cloud directly. That is a lower-level host package;
// this package is the opinionated CloudAgent implementation.
package worker
