# CloudAgent Worker

`cloudagent/worker` is the default server-side CloudAgent worker implementation.
It builds a DeepAgent thread, connects it to Agent Coordinator, and hides the
thread/runtime wiring that most services should not reimplement.

Use this package when a service wants the standard CloudAgent behavior:

- DeepAgent turn execution
- durable history and checkpoint stores
- role-based prompts, models, skills, and approval policy
- task/sub-agent collaboration when the host provides the optional collaboration deps
- Agent Coordinator scan, claim, message, event, release, and close handling

If a service already owns a different `agentworker.AgentThread` implementation,
use `agentworker/cloud` directly. That is the lower-level worker host.

Most business services should start one level higher with
`cloudagent/worker/bootstrap`. It loads the Local/Remote profile and constructs
the standard model, Fornax, MCP, storage, checkpoint, IDGen, memory, thread-ref
and coordinator dependencies from one YAML file. Use this package directly
only when the host deliberately owns that assembly.

## What To Read First

Read these files in order:

1. `docs/agents/cloudagent/index.md`: SDK boundary and service-vs-library guidance.
2. `doc.go`: package contract.
3. `config.go`: public configuration and dependency surface.
4. `worker.go`: coordinator client and host-loop settings.
5. `example_test.go`: minimal wiring shape.
6. `bootstrap/README.md`: recommended business entry point.
7. `cmd/cloud_agent/aic_agent_sdk_worker`: thin reference process entry.

Do not start from `thread/` or `policy/`. Those packages are implementation
details for the default CloudAgent thread.

## Minimal Wiring

The host service provides two groups of inputs:

- `Config`: declarative host/thread/turn behavior.
- `Deps`: external systems owned by the service.

The required dependencies are:

- `HistoryStore`
- `CheckpointStore`

`CoordinatorClient` is optional. If it is nil, `New` creates one from
`Config.Host.Coordinator`; provide it only when the service wants to own
client construction.

The required config sections are:

- `Host.Namespace`
- `Host.Coordinator.PSM`, unless `Deps.CoordinatorClient` is provided
- at least one `Turn.Models` entry
- at least one `Turn.Roles` entry, normally `main`

The normal startup shape is:

```go
cfg := worker.Config{
    Host: worker.HostConfig{
        Namespace: "aic_agent_sdk",
        Concurrency: 4,
        Coordinator: worker.CoordinatorConfig{
            PSM:     "ad.creative.aic_agent_coordinator",
            Cluster: "default",
        },
    },
    Thread: worker.ThreadConfig{
        WorkDir:   "/data/agent_workdir",
        Backend: backend.Config{
            Type:  backend.TypeLocal,
            Local: backend.LocalConfig{Root: "/data/agent_workdir"},
        },
    },
    Turn: worker.TurnConfig{
        Prompt: worker.PromptConfig{Text: basePrompt},
        Models: map[string]worker.ModelProfile{
            "main_model": {
                ChatModel:     chatModel,
                ModelName:     "doubao",
                ContextWindow: 128000,
            },
        },
        Roles: map[string]worker.RolePreset{
            worker.DefaultRoleID: {
                Model:          worker.ModelPolicy{Default: "main_model"},
                ApprovalPolicy: worker.ApprovalPolicyNormal,
            },
            "explorer": {
                Model:          worker.ModelPolicy{Default: "main_model"},
                ApprovalPolicy: worker.ApprovalPolicyReadOnly,
            },
        },
        Defaults: worker.TurnDefaults{
            Capabilities: worker.TurnCapabilities{
                Skills: worker.SkillConfig{Sources: []string{"/data/agent_skills"}},
            },
            Budget: worker.TurnBudget{
                MaxSteps: 100, // optional; 0 keeps the DeepAgent default
            },
        },
    },
}

deps := worker.Deps{
    HistoryStore:      historyProvider,
    CheckpointStore:   checkpointProvider,
    ThreadRefs:        threadRefStore,
    Approvals:         worker.NewApprovalStore(),
    MessageWaitObserver: worker.TaskMessageWaitObserver,
}

return worker.Run(ctx, cfg, deps)
```

## Responsibility Boundary

This package owns the default CloudAgent thread:

- resolve thread role, model, and workdir
- open the configured workspace backend, currently local or AI infra sandbox
- attach history and checkpoint stores
- build turn-level middleware, tools, skills, HITL, plan mode, and collab tools
- convert thread events into worker output items
- handle structured CloudAgent input messages such as user input, resume, and compact

Collaboration is enabled only when `Deps.MessageWaitObserver` is set. Named
thread references are enabled only when `Deps.ThreadRefs` is set; without it,
task tools can still use numeric thread ids.

When using this low-level package directly, the hosting service owns:

- model construction and credentials
- storage implementation
- coordinator client creation or injection
- API, WebUI, auth, workspace/project policy, and session directory service
- thread creation policy. Role and cwd come from Agent Coordinator
  `ThreadProfile.Role` and `ThreadProfile.Cwd`; empty role uses `main`, and
  empty cwd uses the default workdir layout.
- deployment config, logs, metrics, and process lifecycle

The bootstrap package owns the first three items for normal business callers.
`cmd/cloud_agent/aic_agent_sdk_api` and `cmd/cloud_agent/aic_agent_sdk_session` are
reference services for one product experience. They are intentionally outside
this package's public library surface.

## File Map

- `config.go`: public config, dependency interfaces, and validation.
- `cloudagent/backend`: workspace backend config and local / AI infra sandbox opening.
- `worker.go`: Agent Coordinator client creation and host-loop config.
- Internal `agent_builder.go`, `thread_options.go`, `thread_tools.go`,
  `thread_resources.go`, `collab.go`, and `workdir.go`: the default thread
  assembly path. Read these only when changing SDK internals.
- Internal `thread/`: the runtime adapter between `DeepAgentThread` and
  `agentworker.AgentThread`.
- Internal `policy/`: execute approval policy helpers.
- `sql/`: optional reference DDL for worker-owned stores.

## Extension Rules

Prefer adding configuration or dependency injection at this package boundary
only when a real service needs it. Do not expose builder internals just because
they are convenient to test. If the caller needs to control the whole
`agentworker.AgentThread` lifecycle, the correct package is `agentworker/cloud`,
not `cloudagent/worker`.
