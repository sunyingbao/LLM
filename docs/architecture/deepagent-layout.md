# Unified `deepagent/` Repository Layout

The repository has one Agent implementation root: `deepagent/`. The historical
`arch/` SDK module and `backend/agent/` CLI Agent implementation are migration
sources, not permanent architecture boundaries.

## Target ownership

```text
deepagent/
  definition/       declarative AgentDefinition and capability resolution
  runtime/          RuntimeClient contracts plus local and remote clients
  worker/           in-process and distributed Agent workers
  cloud/            CloudAgent API, protocol and service adapters
  core/             model loop and its execution internals
    agentthread/     durable AgentThread, history, checkpoints and compaction
    middleware/      prompt, memory, skill, plan, approval and observability
    backends/        filesystem/sandbox backends
  tools/            shared tool contracts and implementations
  memory/           durable memory formats, migration and consolidation
  host/              SGADK-specific provider/config bindings only
  internal/          implementation details that are not public contracts
```

SGADK product-shell startup is now implemented by `deepagent/cmd/deepagent`;
TUI rendering, configuration loading and session catalog storage remain shared
backend capabilities. It must consume `deepagent/runtime`; it must not own a second model loop,
middleware stack, tool registry or memory implementation.

## Migration invariants

- The root is one Go module; `deepagent/` is not a nested module.
- Public imports use the root module path plus `deepagent/...`.
- Move by responsibility, not by preserving `arch` or `backend/agent` as nested
  directory names.
- Temporary compatibility packages must have a deletion issue and no new
  behavior.
- Credentials remain host-resolved and never enter definitions, manifests or
  timeline events.
- `core/` is one cohesive execution boundary, not a second project. Its nested
  packages are implementation responsibilities of the shared model loop.
