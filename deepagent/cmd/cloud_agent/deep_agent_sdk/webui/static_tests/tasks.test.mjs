import assert from "node:assert/strict";
import test from "node:test";

import { createTaskFeature } from "../static/features/tasks.js";
import { initialState, reduce } from "../static/state/reducer.js";
import { filterTasks } from "../static/state/selectors.js";
import { createStore } from "../static/state/store.js";

test("ignores a task load that finishes after another task was selected", async () => {
  const pending = new Map();
  const store = createStore(reduce, initialState());
  const tasks = createTaskFeature({
    api: {
      getTask: (taskID) => new Promise((done) => pending.set(taskID, done)),
    },
    store,
  });

  const first = tasks.selectTask("1");
  const second = tasks.selectTask("2");
  pending.get("2")({ session_view: view("2", "second") });
  pending.get("1")({ session_view: view("1", "first") });
  await Promise.all([first, second]);

  assert.equal(store.getState().catalog.selectedTaskID, "2");
  assert.equal(store.getState().task.view.session.session_id, "2");
});

test("new task stays local until the first message", () => {
  let createCalls = 0;
  const store = createStore(reduce, {
    ...initialState(),
    catalog: { ...initialState().catalog, selectedProject: "project-a", selectedTaskID: "9" },
  });
  const tasks = createTaskFeature({
    api: { createTask: () => { createCalls += 1; } },
    store,
  });

  tasks.newTask();

  assert.equal(createCalls, 0);
  assert.equal(store.getState().catalog.selectedTaskID, "");
  assert.equal(store.getState().task.view, null);
});

test("rename, archive, and restore use the existing session status contract", async () => {
  const calls = [];
  const store = createStore(reduce, initialState());
  store.dispatch({ type: "catalog/tasksLoaded", projectName: "project-a", tasks: [{ session_id: "7", title: "old" }] });
  const tasks = createTaskFeature({
    api: {
      updateTask: async (taskID, patch) => calls.push({ taskID, patch }),
    },
    store,
  });

  await tasks.renameTask("7", "clear title");
  await tasks.archiveTask("7");
  await tasks.restoreTask("7", "project-a");

  assert.deepEqual(calls, [
    { taskID: "7", patch: { title: "clear title" } },
    { taskID: "7", patch: { status: 2 } },
    { taskID: "7", patch: { status: 1 } },
  ]);
  assert.equal(store.getState().catalog.tasksByProject.get("project-a")[0].title, "clear title");
});

test("filters title and preview without changing task order", () => {
  const tasks = [
    { session_id: "1", title: "Fix layout", preview: "CSS" },
    { session_id: "2", title: "Inspect worker", preview: "Runtime events" },
  ];

  assert.deepEqual(filterTasks(tasks, "runtime").map((task) => task.session_id), ["2"]);
  assert.deepEqual(filterTasks(tasks, "FIX").map((task) => task.session_id), ["1"]);
});

function view(id, title) {
  return { session: { session_id: id, title }, threads: [] };
}
