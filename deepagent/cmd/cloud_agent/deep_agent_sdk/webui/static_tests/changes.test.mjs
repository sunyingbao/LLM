import assert from "node:assert/strict";
import test from "node:test";

import { createChangesFeature, formatReviewMessage, parseUnifiedDiff } from "../static/features/changes.js";
import { initialState, reduce } from "../static/state/reducer.js";
import { createStore } from "../static/state/store.js";

test("does not apply a stale change list to a different selected task", async () => {
  let finishList;
  const store = createStore(reduce, selectedTaskState("task-1"));
  const changes = createChangesFeature({
    api: {
      listChanges: () => new Promise((resolve) => {
        finishList = resolve;
      }),
      getDiff: () => ({ patch: "", truncated: false }),
    },
    store,
  });

  const loading = changes.openTask({ session: { session_id: "task-1" } });
  store.dispatch({ type: "task/selected", taskID: "task-2" });
  finishList({ changes: [{ path: "stale.go", status: "modified", additions: 2, deletions: 1 }] });
  await loading;

  assert.equal(store.getState().catalog.selectedTaskID, "task-2");
  assert.equal(store.getState().inspector.changes.length, 0);
});

test("loads list changes and updates selected diff after selecting a file", async () => {
  const store = createStore(reduce, selectedTaskState("task-1"));
  const changes = createChangesFeature({
    api: {
      listChanges: async () => ({
        changes: [
          { path: "a.txt", status: "modified", additions: 2, deletions: 1 },
          { path: "b.txt", status: "added", additions: 1, deletions: 0 },
        ],
      }),
      getDiff: async (_, path) => ({
        path,
        patch: `diff for ${path}`,
        truncated: false,
      }),
    },
    store,
  });

  await changes.openTask({ session: { session_id: "task-1" } });
  assert.equal(store.getState().inspector.changes.length, 2);

  await changes.selectChange({ path: "a.txt" });
  const inspector = store.getState().inspector;
  assert.equal(inspector.selectedChangePath, "a.txt");
  assert.equal(inspector.diff?.patch, "diff for a.txt");
});

test("refreshChanges can be triggered from current task and reloads list", async () => {
  let calls = 0;
  const store = createStore(reduce, selectedTaskState("task-1"));
  const changes = createChangesFeature({
    api: {
      listChanges: async () => {
        calls += 1;
        return { changes: [{ path: `file-${calls}.go`, status: "modified" }] };
      },
      getDiff: async () => ({ patch: "", truncated: false }),
    },
    store,
  });

  await changes.openTask({ session: { session_id: "task-1" } });
  await changes.refreshChanges();

  assert.equal(calls, 2);
  assert.equal(store.getState().inspector.changes[0].path, "file-2.go");
});

test("parses unified diff line numbers for review comments", () => {
  const diff = parseUnifiedDiff([
    "@@ -10,2 +10,3 @@ function run() {",
    " unchanged",
    "-old line",
    "+new line",
    "+another line",
  ].join("\n"));

  assert.deepEqual(diff.hunks[0].lines.map((line) => [line.kind, line.oldLine, line.newLine]), [
    ["context", 10, 10],
    ["removed", 11, null],
    ["added", null, 11],
    ["added", null, 12],
  ]);
});

test("does not turn a trailing newline into an extra diff row", () => {
  const diff = parseUnifiedDiff("@@ -0,0 +1,1 @@\n+one line\n");

  assert.deepEqual(diff.hunks[0].lines.map((line) => [line.kind, line.oldLine, line.newLine]), [
    ["added", null, 1],
  ]);
});

test("formats line annotations as one stable review message", () => {
  const message = formatReviewMessage([{
    path: "static/app.js",
    startLine: 11,
    endLine: 12,
    comment: "Keep the state transition explicit.",
  }], "Please update this change.");

  assert.equal(message, [
    "Review comment:",
    "- File: static/app.js",
    "- Lines: 11-12",
    "- Comment: Keep the state transition explicit.",
    "",
    "Please update this change.",
  ].join("\n"));
});

test("submits accumulated annotations through the conversation and clears them", async () => {
  const store = createStore(reduce, selectedTaskState("task-1"));
  const submissions = [];
  const changes = createChangesFeature({
    api: {},
    store,
    submitMessage: async (message) => {
      submissions.push(message);
      return true;
    },
  });

  changes.addAnnotation({ path: "main.go", startLine: 7, endLine: 7, comment: "Handle the error." });
  await changes.submitReview("Apply this review.");

  assert.equal(submissions.length, 1);
  assert.match(submissions[0], /File: main\.go/);
  assert.equal(store.getState().inspector.annotations.length, 0);
});

function selectedTaskState(taskID) {
  const state = initialState();
  state.catalog.selectedTaskID = taskID;
  return state;
}
