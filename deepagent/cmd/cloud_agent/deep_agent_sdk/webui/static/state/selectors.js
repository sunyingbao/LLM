export function selectRunState(state) {
  return state.task.runState;
}

export function selectPendingActivity(state) {
  return state.task.pending;
}

export function selectTaskID(state) {
  return state.catalog.selectedTaskID;
}

export function selectVisibleActivities(state) {
  const threadID = state.task.selectedThreadID;
  if (!threadID) return state.task.activities;
  return state.task.activities.filter((activity) => activity.threadID === String(threadID));
}

export function filterTasks(tasks, query) {
  const needle = String(query || "").trim().toLocaleLowerCase();
  if (!needle) return tasks || [];
  return (tasks || []).filter((task) => {
    const text = `${task.title || ""} ${task.preview || task.summary || ""}`.toLocaleLowerCase();
    return text.includes(needle);
  });
}
