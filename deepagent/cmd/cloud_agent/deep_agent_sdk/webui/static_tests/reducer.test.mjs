import assert from "node:assert/strict";
import test from "node:test";

import { initialState, reduce } from "../static/state/reducer.js";
import { createStore } from "../static/state/store.js";
import { selectPendingActivity, selectRunState } from "../static/state/selectors.js";

test("live deltas in the same millisecond are not discarded, even with identical text", () => {
  let state = initialState();
  for (const delta of ["1", "1", "\n", "2"]) {
    state = receive(state, timelineEvent("", "ASSISTANT_DELTA", {
      created_at_ms: 100, assistant_delta: { delta },
    }));
  }
  assert.equal(state.task.activities[0].content, "11\n2");
  const identified = timelineEvent("unique", "ASSISTANT_DELTA", { assistant_delta: { delta: "3" } });
  state = receive(receive(state, identified), identified);
  assert.equal(state.task.events.length, 5);
  for (const output_delta of ["x", "x"]) {
    state = receive(state, timelineEvent("0", "TOOL_CALL_OUTPUT_DELTA", {
      created_at_ms: 100, tool_call: { tool_call_id: "command", output_delta },
    }));
  }
  assert.equal(state.task.activities.find((activity) => activity.kind === "tool").output, "xx");
});

test("a child finishing does not finish the main run or erase its approval", () => {
  let state = receive(initialState(), timelineEvent("1", "TURN_STARTED"));
  state = receive(state, timelineEvent("2", "TURN_STARTED", { thread_id: "child" }));
  state = receive(state, timelineEvent("3", "TURN_FINISHED", { thread_id: "child" }));
  assert.equal(state.task.runState, "running");
  state = receive(state, timelineEvent("4", "APPROVAL_REQUIRED"));
  state = receive(state, timelineEvent("5", "TURN_FINISHED", { thread_id: "child" }));
  assert.equal(state.task.pending.id, "4");
});

test("approvals retain their own submission state across events and thread selection", () => {
  let state = receive(initialState(), timelineEvent("1", "APPROVAL_REQUIRED"));
  state = reduce(state, { type: "pending/submitting", eventID: "1" });
  state = receive(state, timelineEvent("2", "ASSISTANT_DELTA", { thread_id: "child" }));
  assert.equal(state.task.pending.submitting, true);
  state = receive(state, timelineEvent("3", "APPROVAL_REQUIRED", { thread_id: "child" }));
  state = reduce(state, { type: "task/threadSelected", threadID: "t1" });
  assert.equal(state.task.pending.id, "1");
  assert.equal(state.task.pending.submitting, true);
  state = reduce(state, { type: "pending/completed", eventID: "1" });
  assert.equal(state.task.pending, null);
  state = reduce(state, { type: "task/threadSelected", threadID: "child" });
  assert.equal(state.task.pending.id, "3");
});

test("selected thread and a newer run are not overwritten by unrelated terminal events", () => {
  let state = receive(initialState(), timelineEvent("1", "TURN_STARTED"));
  state = receive(state, timelineEvent("2", "TURN_STARTED", { turn_id: "r2" }));
  state = receive(state, timelineEvent("3", "TURN_FINISHED"));
  assert.equal(state.task.runState, "running");
  state = receive(state, timelineEvent("4", "TURN_STARTED", { thread_id: "child" }));
  state = reduce(state, { type: "task/threadSelected", threadID: "t1" });
  state = reduce(state, { type: "stop/requested" });
  state = receive(state, timelineEvent("5", "TURN_FINISHED", { thread_id: "child" }));
  assert.equal(state.task.runState, "stopping");
  state = receive(state, timelineEvent("6", "TURN_FINISHED"));
  assert.equal(state.task.runState, "stopping");
  state = receive(state, timelineEvent("7", "TURN_INTERRUPTED", { turn_id: "r2" }));
  assert.equal(state.task.runState, "idle");
});

test("thread context usage survives starting its next run", () => {
  let state = receive(initialState(), timelineEvent("1", "TURN_FINISHED", { context_usage: { total_tokens: 42 } }));
  state = reduce(state, { type: "task/threadSelected", threadID: "t1" });
  state = receive(state, timelineEvent("2", "TURN_STARTED", { turn_id: "r2" }));
  assert.equal(state.task.contextUsage.total_tokens, 42);
});

test("timeline events drive the single run state", () => {
  let state = initialState();
  state = receive(state, timelineEvent("1", "TURN_STARTED"));
  assert.equal(selectRunState(state), "running");

  state = receive(state, timelineEvent("2", "APPROVAL_REQUIRED", {
    approval: { tool_name: "execute" },
  }));
  assert.equal(selectRunState(state), "waiting_approval");
  assert.equal(selectPendingActivity(state).id, "2");

  state = receive(state, timelineEvent("3", "TURN_STARTED"));
  assert.equal(selectRunState(state), "running");
  assert.equal(selectPendingActivity(state), null);

  state = reduce(state, { type: "stop/requested" });
  assert.equal(selectRunState(state), "stopping");

  state = receive(state, timelineEvent("4", "ERROR", {
    error: { message: "failed" },
  }));
  assert.equal(selectRunState(state), "error");

  state = receive(state, timelineEvent("5", "TURN_FINISHED"));
  assert.equal(selectRunState(state), "idle");
});

test("plan and interrupt requests share the waiting input state", () => {
  const plan = receive(initialState(), timelineEvent("1", "PLAN_INPUT_REQUIRED"));
  const interrupt = receive(initialState(), timelineEvent("2", "INTERRUPT_REQUIRED"));

  assert.equal(selectRunState(plan), "waiting_input");
  assert.equal(selectRunState(interrupt), "waiting_input");
});

test("deduplicates timeline events without mutating the previous state", () => {
  const start = initialState();
  const event = timelineEvent("1", "TURN_STARTED", {
    message: { parts: [{ type: "text", text: "hello" }] },
  });
  const first = reduce(start, { type: "timeline/received", events: [event] });
  const second = reduce(first, { type: "timeline/received", events: [event] });

  assert.notEqual(first, start);
  assert.equal(start.task.events.length, 0);
  assert.equal(first.task.events.length, 1);
  assert.equal(second, first);
  assert.equal(first.task.activities.length, 1);
});

test("store notifies only when state changes and can unsubscribe", () => {
  const store = createStore(reduce, initialState());
  let notifications = 0;
  const unsubscribe = store.subscribe(() => {
    notifications += 1;
  });

  store.dispatch({ type: "unknown" });
  store.dispatch({ type: "stop/requested" });
  unsubscribe();
  store.dispatch({ type: "timeline/received", events: [timelineEvent("1", "TURN_FINISHED")] });

  assert.equal(notifications, 1);
});

test("changes state transitions keep list/diff lifecycle stable", () => {
  const loadTask = (state) => reduce(state, { type: "task/selected", taskID: "task-1" });
  const openChanges = (state) => reduce(state, { type: "inspector/changesLoading", taskID: "task-1" });
  const loadedChanges = (state, changes = []) => reduce(state, {
    type: "inspector/changesLoaded",
    taskID: "task-1",
    changes,
  });
  const selectChange = (state, path) => reduce(state, { type: "inspector/changeSelected", path });
  const loadDiff = (state, path, patch, truncated = false) => reduce(state, {
    type: "inspector/diffLoaded",
    taskID: "task-1",
    path,
    patch,
    truncated,
  });

  let state = openChanges(loadTask(initialState()));
  assert.equal(state.inspector.changesLoading, true);

  state = loadedChanges(state, [{ path: "a.go", status: "modified", additions: 1, deletions: 2 }]);
  assert.equal(state.inspector.changesLoading, false);
  assert.equal(state.inspector.changes.length, 1);
  assert.equal(state.inspector.changes[0].path, "a.go");
  assert.equal(state.inspector.changes[0].additions, 1);

  state = selectChange(state, "a.go");
  assert.equal(state.inspector.selectedChangePath, "a.go");
  assert.equal(state.inspector.diffLoading, true);
  assert.equal(state.inspector.selectedPath, "");

  state = loadDiff(state, "a.go", "patch-content", true);
  assert.equal(state.inspector.diffLoading, false);
  assert.equal(state.inspector.diff.path, "a.go");
  assert.equal(state.inspector.diff.truncated, true);
  assert.equal(state.inspector.diff.patch, "patch-content");
});

function receive(state, event) {
  return reduce(state, { type: "timeline/received", events: [event] });
}

function timelineEvent(id, type, fields = {}) {
  return {
    event_id: id,
    event_type: type,
    session_id: "1",
    thread_id: "t1",
    turn_id: "r1",
    created_at_ms: Number(id),
    ...fields,
  };
}
