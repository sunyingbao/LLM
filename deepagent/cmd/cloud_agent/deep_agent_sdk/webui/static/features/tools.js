export function createSubmissionGate() {
  const active = new Set();

  return {
    async run(key, submit) {
      if (active.has(key)) return false;
      active.add(key);
      try {
        return await submit();
      } finally {
        active.delete(key);
      }
    },
    busy(key) {
      return active.has(key);
    },
  };
}

export function agentSummaries(view) {
  return (view?.threads || [])
    .filter((thread) => Number(thread.status || 0) !== 6)
    .map((thread) => ({
      id: String(thread.thread_id),
      title: thread.title || (Number(thread.role || 0) === 1 ? "Main agent" : "Agent"),
      role: Number(thread.role || 0) === 1 ? "main" : "child",
      status: threadStatus(thread.status),
    }));
}

export function commandTool(activity) {
  const name = String(activity?.toolName || "").toLocaleLowerCase();
  return name === "exec_command" || name === "execute" || name === "bash" || name === "shell";
}

function threadStatus(status) {
  return {
    1: "idle",
    2: "ready",
    3: "running",
    4: "waiting",
    5: "stopping",
    6: "closed",
  }[Number(status)] || "idle";
}
