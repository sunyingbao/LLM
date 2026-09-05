import assert from "node:assert/strict";
import test from "node:test";

import { createPendingFeature } from "../static/features/approvals.js";
import { initialState, reduce } from "../static/state/reducer.js";
import { createStore } from "../static/state/store.js";

test("submits an approval once and restores it after failure", async () => {
  let calls = 0;
  const store = pendingStore();
  const pending = createPendingFeature({
    api: {
      submitInput: async () => {
        calls += 1;
        throw new Error("resume failed");
      },
    },
    store,
    timeline: { resume: () => {} },
  });

  await Promise.allSettled([
    pending.submitApproval("approval-1", true),
    pending.submitApproval("approval-1", true),
  ]);

  assert.equal(calls, 1);
  assert.equal(store.getState().task.pending.id, "approval-1");
  assert.equal(store.getState().task.pending.error, "resume failed");
  assert.equal(store.getState().task.runState, "waiting_approval");
});

test("builds the existing approval resume payload", async () => {
  const submitted = [];
  const store = pendingStore();
  const pending = createPendingFeature({
    api: { submitInput: async (payload) => submitted.push(payload) },
    store,
    timeline: { resume: () => {} },
  });

  await pending.submitApproval("approval-1", false, {
    reason: "use a safer command",
    allowInSession: false,
  });

  assert.deepEqual(submitted[0], {
    sessionID: "9",
    threadID: "t1",
    resumeRef: { turn_id: "r1", checkpoint_id: "cp1", interrupt_id: "i1" },
    approval: {
      approved: false,
      reason: "use a safer command",
      allow_in_session: false,
      tool_name: "exec_command",
      arguments_json: "{\"cmd\":\"rm file\"}",
    },
  });
  assert.equal(store.getState().task.pending, null);
  assert.equal(store.getState().task.runState, "running");

  store.dispatch({
    type: "timeline/received",
    events: [{
      event_id: "assistant-1",
      event_type: "ASSISTANT_DELTA",
      session_id: "9",
      thread_id: "t1",
      turn_id: "r1",
      assistant_delta: { delta: "continuing" },
    }],
  });
  assert.equal(store.getState().task.pending, null);
});

function pendingStore() {
  const state = initialState();
  state.catalog.selectedTaskID = "9";
  const store = createStore(reduce, state);
  store.dispatch({
    type: "timeline/received",
    events: [{
      event_id: "approval-1",
      event_type: "APPROVAL_REQUIRED",
      session_id: "9",
      thread_id: "t1",
      turn_id: "r1",
      approval: {
        checkpoint_id: "cp1",
        interrupt_id: "i1",
        tool_name: "exec_command",
        arguments_json: "{\"cmd\":\"rm file\"}",
      },
    }],
  });
  return store;
}
