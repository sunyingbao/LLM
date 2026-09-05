//go:build !windows

package worker

import (
	cloudbackend "eino-cli/deepagent/cloud/backend"
	"eino-cli/deepagent/coordinator"
	"eino-cli/deepagent/worker/tasktool"
)

const defaultTaskRolesDescription = `
- explorer: use for read-only investigation, codebase questions, logs/config inspection, and concise findings. Explorer task threads have read-only file tools and read-only shell policy; do not assign implementation work to them.
- worker: use for bounded implementation tasks such as creating or editing files and running focused validation commands. Worker task threads can write project files and run project-local commands; keep commands narrow and avoid unrelated system changes.
`

// collabMiddlewares wires task/sub-agent tools into a turn. It is enabled only
// when the host provides MessageWaitObserver, because waiting for task results
// requires reading session events.
func resolvedThreadProjectName(threadInfo *coordinator.Thread, threadProfile ResolvedThreadProfile) string {
	if name, err := cloudbackend.CleanProjectName(threadProfile.Project); err == nil {
		return name
	}
	return threadProjectName(threadInfo, tasktool.ThreadProfile{Cwd: threadProfile.WorkDir})
}

func isMainThread(threadInfo *coordinator.Thread) bool {
	if threadInfo == nil {
		return false
	}
	metadata := threadInfo.Metadata
	return metadata[MetadataThreadRole] == ThreadRoleMain ||
		(metadata[MetadataThreadRole] == "" && metadata[MetadataParentThreadID] == "")
}
