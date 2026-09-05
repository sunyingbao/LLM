import assert from "node:assert/strict";
import test from "node:test";

import { createFilesFeature } from "../static/features/files.js";
import { initialState, reduce } from "../static/state/reducer.js";
import { createStore } from "../static/state/store.js";

test("does not attach a directory response to a different task", async () => {
  let finishRequest;
  const store = createStore(reduce, selectedTaskState("task-1"));
  const files = createFilesFeature({
    api: {
      listFiles: () => new Promise((done) => { finishRequest = done; }),
    },
    store,
  });

  const load = files.loadDirectory("task-1", ".");
  store.dispatch({ type: "task/selected", taskID: "task-2" });
  finishRequest({ files: [{ path: "main.go", name: "main.go", is_dir: false }] });
  await load;

  assert.equal(store.getState().inspector.fileTree.size, 0);
});

test("loads a directory once and toggles its expanded state", async () => {
  let calls = 0;
  const store = createStore(reduce, selectedTaskState("task-1"));
  const files = createFilesFeature({
    api: {
      listFiles: async () => {
        calls += 1;
        return { files: [{ path: "src", name: "src", is_dir: true }] };
      },
    },
    store,
  });

  await files.loadDirectory("task-1", ".");
  await files.loadDirectory("task-1", ".");
  files.toggleDirectory(".");

  assert.equal(calls, 1);
  assert.equal(store.getState().inspector.fileTree.get(".").expanded, false);
});

function selectedTaskState(taskID) {
  const state = initialState();
  state.catalog.selectedTaskID = taskID;
  return state;
}
