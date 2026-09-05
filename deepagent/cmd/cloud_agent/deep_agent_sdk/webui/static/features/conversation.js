import { createSubmissionGate } from "./tools.js";

export function createConversationFeature({ api, store, timeline }) {
  const gate = createSubmissionGate();

  function setDraft(draft) {
    store.dispatch({ type: "conversation/draftChanged", draft: String(draft || "") });
  }

  function togglePlanMode() {
    store.dispatch({ type: "plan/toggled" });
  }

  function selectAgent(threadID) {
    store.dispatch({ type: "task/threadSelected", threadID });
  }

  async function submitMessage(rawContent) {
    const content = String(rawContent || "").trim();
    if (!content) return false;
    setDraft(rawContent);
    return gate.run("message", async () => {
      let state = store.getState();
      let taskID = state.catalog.selectedTaskID;
      if (!taskID) {
        const projectName = state.catalog.selectedProject || "default";
        const created = await api.createTask({ title: content.slice(0, 80), projectName });
        store.dispatch({ type: "task/created", projectName, view: created.session_view });
        taskID = store.getState().catalog.selectedTaskID;
      }
      state = store.getState();
      try {
        const payload = await api.submitInput({
          sessionID: taskID,
          threadID: state.task.selectedThreadID,
          content,
          mode: state.task.planMode ? 1 : undefined,
        });
        if (payload.session_view) store.dispatch({ type: "task/loaded", taskID, view: payload.session_view });
        store.dispatch({ type: "conversation/userOptimistic", message: payload.message, content });
        store.dispatch({ type: "conversation/submitted" });
        await timeline.openTask?.(store.getState().task.view);
        return true;
      } catch (error) {
        store.dispatch({ type: "conversation/submitFailed", error: error.message, draft: rawContent });
        throw error;
      }
    });
  }

  async function stopTask() {
    const taskID = store.getState().catalog.selectedTaskID;
    if (!taskID) return false;
    const previousState = store.getState().task.runState;
    store.dispatch({ type: "stop/requested" });
    try {
      await api.stopTask(taskID);
      await timeline.refresh?.();
      return true;
    } catch (error) {
      store.dispatch({ type: "stop/failed", previousState });
      store.dispatch({ type: "ui/error", error: error.message });
      throw error;
    }
  }

  async function compactContext() {
    const state = store.getState();
    if (!state.catalog.selectedTaskID || !state.task.selectedThreadID) {
      throw new Error("Choose a task before compacting context.");
    }
    await api.submitInput({
      sessionID: state.catalog.selectedTaskID,
      threadID: state.task.selectedThreadID,
      mode: 2,
    });
    timeline.resume?.();
  }

  return { setDraft, togglePlanMode, selectAgent, submitMessage, stopTask, compactContext };
}
