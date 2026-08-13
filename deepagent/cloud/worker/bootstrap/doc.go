// Package bootstrap provides the high-level CloudAgent Worker entry point.
//
// It loads one explicit profile file, constructs model, Fornax, MCP, storage,
// checkpoint, ID generator, memory and Agent Coordinator dependencies, then
// delegates execution to cloudagent/worker. Business services normally only
// provide their tools and callback handlers through Options.
//
// Local and remote deployments share one runtime path. Their differences live
// in worker.local.yml and worker.remote.yml; Profile is only a filename
// convention and never switches implementation behavior. The selected YAML is
// the only behavioral configuration source; environment variables are only
// expanded for explicit ${NAME} references in YAML string values.
package bootstrap
