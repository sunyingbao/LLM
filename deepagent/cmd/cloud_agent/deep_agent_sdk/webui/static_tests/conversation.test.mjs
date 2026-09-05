import assert from "node:assert/strict";
import test from "node:test";

import { createConversationFeature } from "../static/features/conversation.js";
import { initialState, reduce } from "../static/state/reducer.js";
import { createStore } from "../static/state/store.js";

test("preserves a created task and draft when the first submit fails", async () => {
  let creates = 0;
  const store = selectedProjectStore();
  const conversation = createConversationFeature({
    api: {
      createTask: async () => {
        creates += 1;
        return { session_view: taskView("9") };
      },
      submitInput: async () => {
        throw new Error("submit failed");
      },
    },
    store,
    timeline: { openTask: async () => {} },
  });

  await assert.rejects(conversation.submitMessage("inspect the repo"), /submit failed/);

  assert.equal(creates, 1);
  assert.equal(store.getState().catalog.selectedTaskID, "9");
  assert.equal(store.getState().task.view.session.session_id, "9");
  assert.equal(store.getState().task.draft, "inspect the repo");
  assert.equal(store.getState().task.submitError, "submit failed");
});

test("successful submit appends one optimistic user activity", async () => {
  const store = selectedProjectStore();
  const conversation = createConversationFeature({
    api: {
      createTask: async () => ({ session_view: taskView("9") }),
      submitInput: async () => ({
        session_view: taskView("9"),
        message: { message_id: "m1", thread_id: "t1" },
      }),
    },
    store,
    timeline: { openTask: async () => {} },
  });

  await conversation.submitMessage("inspect the repo");

  assert.equal(store.getState().task.runState, "running");
  assert.equal(store.getState().task.activities.length, 1);
  assert.equal(store.getState().task.activities[0].content, "inspect the repo");
  assert.equal(store.getState().task.draft, "");
});

test("stop enters stopping before the HTTP call finishes", async () => {
  const store = selectedProjectStore();
  store.dispatch({ type: "task/created", projectName: "project-a", view: taskView("9") });
  let finishStop;
  const conversation = createConversationFeature({
    api: { stopTask: () => new Promise((done) => { finishStop = done; }) },
    store,
    timeline: { refresh: async () => {} },
  });

  const stopping = conversation.stopTask();
  assert.equal(store.getState().task.runState, "stopping");
  finishStop({});
  await stopping;
});

function selectedProjectStore() {
  const store = createStore(reduce, initialState());
  store.dispatch({ type: "catalog/projectSelected", projectName: "project-a" });
  return store;
}

function taskView(id) {
  return {
    session: { session_id: id, title: "inspect the repo" },
    threads: [{ thread_id: "t1", role: 1, status: 1, title: "Main" }],
  };
}
