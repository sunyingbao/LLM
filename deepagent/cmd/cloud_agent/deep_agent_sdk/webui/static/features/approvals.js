import { planInputContent, resumeReference } from "./plans.js";
import { createSubmissionGate } from "./tools.js";

export function createPendingFeature({ api, store, timeline }) {
  const gate = createSubmissionGate();

  function submitApproval(eventID, approved, options = {}) {
    return submitPending(eventID, (state, event) => ({
      sessionID: state.catalog.selectedTaskID,
      threadID: event.thread_id,
      resumeRef: resumeReference(event, event.approval),
      approval: {
        approved: Boolean(approved),
        reason: String(options.reason || ""),
        allow_in_session: Boolean(options.allowInSession),
        tool_name: event.approval?.tool_name || "",
        arguments_json: event.approval?.arguments_json || "",
      },
    }));
  }

  function submitPlanInput(eventID, answers, note) {
    const content = planInputContent(answers, note);
    if (!content) return Promise.reject(new Error("Plan input cannot be empty."));
    return submitPending(eventID, (state, event) => ({
      sessionID: state.catalog.selectedTaskID,
      threadID: event.thread_id,
      content,
      resumeRef: resumeReference(event, event.plan_input_required),
    }));
  }

  function submitInterrupt(eventID, answer) {
    const content = String(answer || "").trim();
    if (!content) return Promise.reject(new Error("Follow-up answer cannot be empty."));
    return submitPending(eventID, (state, event) => ({
      sessionID: state.catalog.selectedTaskID,
      threadID: event.thread_id,
      content,
      resumeRef: resumeReference(event, event.interrupt_required),
      interrupt: {
        kind: event.interrupt_required?.kind || "",
        info_type: event.interrupt_required?.info_type || "",
        data: { user_answer: content },
      },
    }));
  }

  function submitPending(eventID, buildInput) {
    return gate.run(`pending:${eventID}`, async () => {
      const state = store.getState();
      const pending = state.task.pending;
      if (pending?.id !== eventID) return false;
      store.dispatch({ type: "pending/submitting", eventID });
      try {
        await api.submitInput(buildInput(state, pending.event));
        store.dispatch({ type: "pending/completed", eventID });
        timeline.resume?.();
        return true;
      } catch (error) {
        store.dispatch({ type: "pending/failed", eventID, error: error.message });
        throw error;
      }
    });
  }

  return { submitApproval, submitPlanInput, submitInterrupt };
}
