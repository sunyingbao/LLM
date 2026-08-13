# CloudAgent Worker Bootstrap

`bootstrap` is the recommended entry point for business Worker services. It
owns the standard initialization pipeline that used to live in the reference
`cmd` service: config loading, model and Fornax construction, MySQL/Redis,
history and checkpoint stores, ID generation, MCP, memory, thread references,
Agent Coordinator client creation, and `cloudagent/worker` startup.

The normal business entry is intentionally small:

```go
func run(ctx context.Context, args []string, businessTools []tool.BaseTool) error {
	return bootstrap.Run(ctx, bootstrap.Options{
		Args:  args,
		Tools: businessTools,
	})
}
```

Use `-conf conf/worker.local.yml` for local development and
`-conf conf/worker.remote.yml` for a deployed environment. `AGENT_WORKER_CONF`
may select the same file without changing code. The selected YAML is the only
behavioral configuration source. Environment variables are read only when the
YAML explicitly contains a `${NAME}` placeholder, normally for credentials;
they cannot override unrelated fields. Other command-line configuration flags
are intentionally unsupported.

`Options` contains the business extension points:

- `Tools`: server-side business tools.
- `Callbacks`: business Eino callbacks.

Use the lower-level `cloudagent/worker.Config` and `cloudagent/worker.Deps` only
when the hosting service deliberately owns infrastructure assembly.
