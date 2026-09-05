# Unified `deepagent/` Repository Layout

The repository has one Agent implementation root: `deepagent/`. The historical
`arch/`, `agent/` and `backend/agent/` implementations have been removed.

## Target ownership

```text
deepagent/
  backend/          local configuration, sandbox, session data and TUI
  runtime/          RuntimeClient contracts plus local and remote clients
  worker/           in-process and distributed Agent workers
  cloud/            CloudAgent API, protocol and service adapters
  core/             model loop and its execution internals
    agentthread/     durable AgentThread, history, checkpoints and compaction
    middleware/      prompt, memory, skill, plan, approval and observability
    backends/        filesystem/sandbox backends
  tools/            shared tool contracts and implementations
  memory/           durable memory formats and consolidation
  host/              DeepAgent-specific provider/config bindings only
```

DeepAgent product-shell startup is implemented by `deepagent/cmd/deepagent`;
TUI rendering, configuration loading and session storage live in
`deepagent/backend`. The CLI consumes `deepagent/runtime`; it must not own a second model loop,
middleware stack, tool registry or memory implementation.

## Repository invariants

- The root is one Go module; `deepagent/` is not a nested module.
- Public imports use the root module path plus `deepagent/...`.
- New code is placed by responsibility and must not recreate `arch`, `agent`
  or `backend/agent` as parallel implementation roots.
- Credentials remain host-owned and never enter manifests or
  timeline events.
- `core/` is one cohesive execution boundary, not a second project. Its nested
  packages are implementation responsibilities of the shared model loop.
