import { createTimelineChannel } from "../api/timeline.js";
import { readStorage, writeStorage } from "../state/storage.js";

export function createTimelineFeature({ api, store, storage = globalThis.localStorage }) {
  const channel = createTimelineChannel({
    api,
    loadQueue: (taskID) => readStorage(storage, queueKey(taskID)),
    saveQueue: (taskID, queueID) => writeStorage(storage, queueKey(taskID), queueID),
    loadCursor: (taskID) => readStorage(storage, cursorKey(taskID)),
    saveCursor: (taskID, cursor) => writeStorage(storage, cursorKey(taskID), cursor),
    onEvents: (taskID, events) => {
      if (store.getState().catalog.selectedTaskID !== taskID) return;
      store.dispatch({ type: "timeline/received", events });
    },
    onStatus: (taskID, status) => {
      if (store.getState().catalog.selectedTaskID === taskID) {
        store.dispatch({ type: "transport/statusChanged", status });
      }
    },
    onError: (taskID, error) => {
      if (store.getState().catalog.selectedTaskID === taskID) {
        store.dispatch({ type: "ui/error", error: error.message });
      }
    },
  });

  async function openTask(view) {
    channel.close();
    const taskID = String(view?.session?.session_id || "");
    if (!taskID) return;
    const threadID = mainThreadID(view);
    const payload = await api.listTimeline({ sessionID: taskID, limit: 200, backward: true });
    if (store.getState().catalog.selectedTaskID !== taskID) return;
    store.dispatch({ type: "timeline/received", events: payload.events || [] });
    if (payload.next_cursor) store.dispatch({ type: "transport/cursorChanged", cursor: String(payload.next_cursor) });
    channel.follow(taskID, threadID);
  }

  async function refresh() {
    const state = store.getState();
    const taskID = state.catalog.selectedTaskID;
    if (!taskID) return;
    const payload = await api.listTimeline({
      sessionID: taskID,
      cursor: state.transport.cursor,
      limit: 200,
    });
    if (store.getState().catalog.selectedTaskID !== taskID) return;
    store.dispatch({ type: "timeline/received", events: payload.events || [] });
    if (payload.next_cursor) store.dispatch({ type: "transport/cursorChanged", cursor: String(payload.next_cursor) });
  }

  function resume() {
    const state = store.getState();
    if (state.catalog.selectedTaskID) channel.follow(state.catalog.selectedTaskID, state.task.selectedThreadID);
  }

  function close() {
    channel.close();
  }

  return { openTask, refresh, resume, close };
}

function mainThreadID(view) {
  const threads = view?.threads || [];
  const main = threads.find((thread) => Number(thread.role || 0) === 1) || threads[0];
  return main?.thread_id === undefined || main?.thread_id === null ? "" : String(main.thread_id);
}

function queueKey(taskID) {
  return `deepagent.timeline.queue.${taskID}`;
}

function cursorKey(taskID) {
  return `deepagent.timeline.cursor.${taskID}`;
}
