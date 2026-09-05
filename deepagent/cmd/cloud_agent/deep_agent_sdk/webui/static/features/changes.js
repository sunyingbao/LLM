export function createChangesFeature({ api, store, submitMessage }) {
  let listToken = 0;
  let diffSeq = 0;
  let diffController = null;

  async function openTask(view) {
    clear();
    const taskID = String(view?.session?.session_id || "");
    if (!taskID) return;
    await loadChanges(taskID);
  }

  async function loadChanges(taskID) {
    const id = String(taskID);
    const token = ++listToken;
    store.dispatch({ type: "inspector/changesLoading", taskID: id });
    try {
      const payload = await api.listChanges(id);
      if (store.getState().catalog.selectedTaskID !== id || token !== listToken) return [];
      const changes = (payload.changes || []).map((change) => ({
        path: String(change.path || ""),
        status: String(change.status || ""),
        additions: Number.isFinite(Number(change.additions)) ? Number(change.additions) : 0,
        deletions: Number.isFinite(Number(change.deletions)) ? Number(change.deletions) : 0,
      })).filter((change) => change.path);
      changes.sort((left, right) => {
        if (left.path === right.path) return 0;
        return left.path < right.path ? -1 : 1;
      });
      store.dispatch({ type: "inspector/changesLoaded", taskID: id, changes });
      return changes;
    } catch (error) {
      if (store.getState().catalog.selectedTaskID !== id || token !== listToken) return [];
      store.dispatch({ type: "inspector/changesFailed", taskID: id });
      store.dispatch({ type: "ui/error", error: error.message });
      return [];
    }
  }

  async function refreshChanges() {
    const taskID = store.getState().catalog.selectedTaskID;
    if (!taskID) return [];
    return loadChanges(taskID);
  }

  async function selectChange(change) {
    const path = String(change?.path || "");
    if (!path) return;
    const taskID = store.getState().catalog.selectedTaskID;
    if (!taskID) return;
    store.dispatch({ type: "inspector/changeSelected", path });
    await loadDiff(taskID, path);
  }

  async function loadDiff(taskID, path) {
    const id = String(taskID);
    const target = String(path || "");
    const seq = ++diffSeq;
    const controller = new AbortController();
    diffController?.abort();
    diffController = controller;
    try {
      const payload = await api.getDiff(id, target, { signal: controller.signal });
      const state = store.getState();
      if (controller.signal.aborted || state.catalog.selectedTaskID !== id || seq !== diffSeq || state.inspector.selectedChangePath !== target) return;
      store.dispatch({
        type: "inspector/diffLoaded",
        taskID: id,
        path: target,
        patch: payload.patch || "",
        truncated: payload.truncated,
      });
    } catch (error) {
      if (error?.name === "AbortError") return;
      const state = store.getState();
      if (state.catalog.selectedTaskID !== id || state.inspector.selectedChangePath !== target) return;
      store.dispatch({ type: "inspector/diffFailed", taskID: id, path: target });
      store.dispatch({ type: "ui/error", error: error.message });
    }
  }

  function clear() {
    diffController?.abort();
    diffController = null;
    listToken += 1;
    store.dispatch({ type: "inspector/changesCleared" });
  }

  function startComment(path, line) {
    const lineNumber = Number(line);
    if (!path || !Number.isInteger(lineNumber) || lineNumber <= 0) return;
    store.dispatch({ type: "inspector/commentStarted", path: String(path), line: lineNumber });
  }

  function cancelComment() {
    store.dispatch({ type: "inspector/commentCancelled" });
  }

  function addAnnotation(annotation) {
    const comment = String(annotation?.comment || "").trim();
    const path = String(annotation?.path || "");
    const startLine = Number(annotation?.startLine);
    const endLine = Number(annotation?.endLine || startLine);
    if (!path || !comment || !Number.isInteger(startLine) || startLine <= 0) return false;
    store.dispatch({
      type: "inspector/annotationAdded",
      annotation: { path, startLine, endLine: Math.max(startLine, endLine), comment },
    });
    return true;
  }

  async function submitReview(note = "") {
    const annotations = store.getState().inspector.annotations;
    const message = formatReviewMessage(annotations, note);
    if (!message) return false;
    if (!submitMessage) throw new Error("Review submission is unavailable.");
    const submitted = await submitMessage(message);
    if (submitted) store.dispatch({ type: "inspector/annotationsCleared" });
    return Boolean(submitted);
  }

  return {
    openTask,
    refreshChanges,
    selectChange,
    startComment,
    cancelComment,
    addAnnotation,
    submitReview,
    clear,
  };
}

export function parseUnifiedDiff(patch) {
  const diff = { hunks: [] };
  let hunk = null;
  let oldLine = 0;
  let newLine = 0;
  const lines = String(patch || "").split("\n");
  if (lines.at(-1) === "") lines.pop();
  for (const text of lines) {
    const header = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(text);
    if (header) {
      oldLine = Number(header[1]);
      newLine = Number(header[2]);
      hunk = { header: text, lines: [] };
      diff.hunks.push(hunk);
      continue;
    }
    if (!hunk || text.startsWith("\\ No newline")) continue;
    const removed = text.startsWith("-");
    const added = text.startsWith("+");
    hunk.lines.push({
      kind: removed ? "removed" : added ? "added" : "context",
      text,
      oldLine: added ? null : oldLine,
      newLine: removed ? null : newLine,
    });
    if (!added) oldLine += 1;
    if (!removed) newLine += 1;
  }
  return diff;
}

export function formatReviewMessage(annotations, note = "") {
  const comments = (annotations || []).map((annotation) => [
    "Review comment:",
    `- File: ${annotation.path}`,
    `- Lines: ${lineRange(annotation)}`,
    `- Comment: ${String(annotation.comment || "").trim()}`,
  ].join("\n"));
  return [...comments, String(note || "").trim()].filter(Boolean).join("\n\n");
}

function lineRange(annotation) {
  return annotation.startLine === annotation.endLine
    ? String(annotation.startLine)
    : `${annotation.startLine}-${annotation.endLine}`;
}
