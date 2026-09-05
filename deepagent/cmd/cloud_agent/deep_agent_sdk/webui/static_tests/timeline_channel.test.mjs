import assert from "node:assert/strict";
import test from "node:test";

import { createTimelineChannel } from "../static/api/timeline.js";

test("discards events emitted after another task was selected", async () => {
  const subscriptions = [];
  const received = [];
  const channel = createTimelineChannel({
    api: {
      subscribeTimeline: async (request) => {
        subscriptions.push(request);
        request.onQueue(`queue-${request.sessionID}`);
        await aborted(request.signal);
        request.onEvent({ event_id: `late-${request.sessionID}` });
      },
      listTimeline: async () => ({ events: [] }),
    },
    loadQueue: () => "saved-queue",
    saveQueue: () => {},
    onEvents: (taskID, events) => received.push({ taskID, events }),
  });

  channel.follow("task-1");
  await tick();
  channel.follow("task-2");
  await tick();
  channel.close();

  assert.equal(subscriptions[0].recoverQueueID, "saved-queue");
  assert.equal(received.some((item) => item.taskID === "task-1"), false);
});

test("uses timeline polling after a non-retryable stream failure", async () => {
  const received = [];
  const statuses = [];
  const cursors = [];
  const channel = createTimelineChannel({
    api: {
      subscribeTimeline: async () => {
        const error = new Error("stream unavailable");
        error.retryStream = false;
        throw error;
      },
      listTimeline: async ({ cursor }) => ({
        events: [{ event_id: "9" }],
        page_info: { next_cursor: cursor ? "10" : "9" },
      }),
    },
    loadQueue: () => "",
    saveQueue: () => {},
    saveCursor: (taskID, cursor) => cursors.push({ taskID, cursor }),
    onEvents: (taskID, events) => received.push({ taskID, events }),
    onStatus: (taskID, status) => statuses.push({ taskID, status }),
    schedule: () => 1,
    cancelSchedule: () => {},
  });

  channel.follow("task-1");
  await tick();
  channel.close();

  assert.equal(received[0].taskID, "task-1");
  assert.equal(received[0].events[0].event_id, "9");
  assert.equal(statuses.at(-1).status, "polling");
  assert.deepEqual(cursors, [{ taskID: "task-1", cursor: "9" }]);
});

function aborted(signal) {
  return new Promise((done) => {
    if (signal.aborted) {
      done();
      return;
    }
    signal.addEventListener("abort", done, { once: true });
  });
}

function tick() {
  return new Promise((done) => setTimeout(done, 0));
}
