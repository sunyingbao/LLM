export function projectExecution(task) {
  const runs = new Map();
  const currentRuns = new Map();
  const contextByThread = new Map();
  for (const thread of task.view?.threads || []) {
    const id = String(thread.thread_id);
    const run = { state: ({ 3: "running", 4: "waiting_input", 5: "stopping" })[Number(thread.status)] || "idle", pending: null };
    runs.set(`${id}:`, run);
    currentRuns.set(id, `${id}:`);
  }
  for (const event of task.events) {
    if (event.__local) continue;
    const threadID = String(event.thread_id || "");
    const key = `${threadID}:${String(event.turn_id || "")}`;
    let run = runs.get(key);
    if (!run) {
      run = { state: "idle", pending: null };
      runs.set(key, run);
    }
    if (!currentRuns.has(threadID) || currentRuns.get(threadID) === `${threadID}:` || event.event_type === "TURN_STARTED") {
      currentRuns.set(threadID, key);
    }
    if (event.context_usage) contextByThread.set(threadID, event.context_usage);
    switch (event.event_type) {
      case "TURN_STARTED":
        run.state = "running";
        run.pending = null;
        break;
      case "TURN_FINISHED":
      case "TURN_INTERRUPTED":
        run.state = "idle";
        run.pending = null;
        break;
      case "ERROR":
        run.state = "error";
        run.pending = null;
        break;
      case "APPROVAL_REQUIRED":
      case "PLAN_INPUT_REQUIRED":
      case "INTERRUPT_REQUIRED": {
        const reply = task.pendingReplies.get(event.__stable_id);
        const kind = event.event_type === "APPROVAL_REQUIRED" ? "approval" : event.event_type === "PLAN_INPUT_REQUIRED" ? "plan" : "interrupt";
        run.pending = reply?.completed ? null : {
          id: event.__stable_id, kind, event, submitting: false, error: "", ...reply,
        };
        run.state = run.pending ? (kind === "approval" ? "waiting_approval" : "waiting_input") : "running";
        break;
      }
    }
  }
  const selected = task.selectedThreadID;
  const visible = [...currentRuns].filter(([id]) => !selected || id === selected).map(([, key]) => runs.get(key));
  const pending = visible.map((run) => run.pending).filter(Boolean).at(-1) || null;
  const requested = (task.requestedRunStates.get(selected) || task.requestedRunStates.get(""))?.state;
  const runState = requested || (pending ? (pending.kind === "approval" ? "waiting_approval" : "waiting_input") :
    ["stopping", "running", "waiting_input", "error"].find((state) => visible.some((run) => run.state === state)) || "idle");
  const contextUsage = selected ? contextByThread.get(selected) : [...contextByThread.values()].at(-1);
  return { pending, runState, contextUsage: contextUsage || null };
}
