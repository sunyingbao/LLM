# AGENTS.md

This file is for Codex or other coding agents working in this repository. It is
not the user-facing project overview. Keep reader-facing documentation in
`README.md` and `docs/`.

## Read First

- Use [`README.md`](./README.md) only as the repository landing page.
- Use [`docs/README.md`](./docs/README.md) as the documentation source of truth
  and directory map.
- For current SDK boundaries, start from [`docs/agents/index.md`](./docs/agents/index.md).
- For CloudAgent worker/API extraction details, start from
  [`docs/agents/cloudagent/index.md`](./docs/agents/cloudagent/index.md).
- For local validation, start from
  [`docs/runbooks/deepagent-worker-e2e.md`](./docs/runbooks/deepagent-worker-e2e.md).

## Repository Intent

`deepagent` is the repository's only Agent implementation root. The core goal
is to make the same definition and runtime contracts work for the DeepAgent local
product and for real server-side Agent workers.

The main layers are:

- `core`: local agent runtime, model/tool orchestration, middleware,
  filesystem/sandbox backend, skills, HITL, planning, checkpoint, context
  management.
- `core/agentthread`: long-lived thread runtime over agent turns, input
  queueing, events, history, checkpoint/resume, compaction.
- `worker`: worker host boundary for local in-process execution and Agent Coordinator; owns scan, claim,
  pull message, ack, append event, release, block/resume, interrupt/close
  lifecycle.
- `cloud/api`: framework-neutral API facade over Agent Coordinator for
  submit, timeline subscribe/list, and control operations.
- `cloud/worker`: reusable default CloudAgent worker assembly for business
  services.
- `cmd/cloud_agent`: reference implementation and dogfood service; it should
  validate SDK usability without becoming the SDK contract by accident.
- `runtime`: the public local/remote client, routing and timeline contract.
- `definition`: portable Agent declarations and host capability resolution.
- `host`: DeepAgent-specific provider and configuration bindings; it must not own a
  second Agent loop.

## Boundary Rules

- Do not put model loop, prompt assembly, ReAct details, or business UI concepts
  into `worker`.
- Do not put `cmd/cloud_agent` product concepts such as friendly names, panels,
  WebUI state, or service-specific wording into core SDK packages unless the
  boundary is explicitly agreed.
- Prefer thin public contracts named after existing repo concepts. Avoid new
  wrapper layers unless they remove real duplication or define a durable host
  boundary.
- CloudAgent timeline payloads are payload-only. The AC event envelope carries
  header fields such as event id, type, thread id, turn id, and timestamp.
- `deep_agent_sdk` should generally pass timeline payloads through instead of
  understanding worker payload schema.
- `cloud/api` must stay independent of Hertz and session-service internals.

## Documentation Rules

- Root `README.md` is for repository users. Keep it short: positioning, module
  map, docs links, and common validation commands.
- Root `AGENTS.md` is for agents. Do not turn it into a marketing or user guide.
- `docs/README.md` is the directory source of truth.
- Current SDK guides live in `docs/agents/`.
- Test suites and validation strategy live in `docs/testing/`.
- Local operation steps live in `docs/runbooks/`.
- Temporary handoff plans and active investigation notes should not be added
  under the current SDK documentation tree. Graduate stable conclusions into
  `docs/agents/`, `docs/testing/`, or `docs/runbooks/`.
- Historical or superseded material lives in `docs/archive/`.
- `cmd/*/docs/` may document the local service or command, but it is not the SDK
  documentation entrypoint.

## Development Rules

- Read the existing code path before changing an abstraction.
- Keep edits scoped to the requested boundary. Do not refactor unrelated modules
  while touching docs or protocol code.
- Preserve user or other-agent work in the tree. Never revert unrelated changes
  just to make the diff smaller.
- Use `rg`/`rg --files` for search.
- Use `gofmt` for Go changes.
- Prefer tests close to the changed package. Broaden validation when changing
  shared protocol, worker lifecycle, docs paths, or public SDK contracts.

## Common Validation

Use the smallest relevant subset first, then broaden when the changed boundary
requires it.

```bash
go test ./cloud/... ./worker/...
go test ./core/... ./runtime/... ./host/...
go test ./cmd/deepagent/... ./cmd/cloud_agent/...
git diff --check
```

For WebUI JavaScript changes:

```bash
node --check cmd/cloud_agent/deep_agent_sdk/webui/static/api.js
node --check cmd/cloud_agent/deep_agent_sdk/webui/static/app.js
node --check cmd/cloud_agent/deep_agent_sdk/webui/static/render.js
node --check cmd/cloud_agent/deep_agent_sdk/webui/static/state.js
node --check cmd/cloud_agent/deep_agent_sdk/webui/static/timeline_model.js
```
