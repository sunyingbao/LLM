import assert from "node:assert/strict";
import test from "node:test";

import { createStreamController } from "../static/stream_controller.js";

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

test("does not reconnect after a non-retryable stream error", async () => {
  let openCalls = 0;
  const stream = createStreamController({
    loadQueue: () => "",
    saveQueue: () => {},
    clearQueue: () => {},
    openStream: async () => {
      openCalls += 1;
      const err = new Error("downstream subscription limit exceeded");
      err.retryStream = false;
      throw err;
    },
    onStatus: () => {},
    onEvent: () => {},
  });

  stream.open("session-1");
  await sleep(1350);
  stream.close();

  assert.equal(openCalls, 1);
});

test("preserves recover queue across retryable stream reconnects", async () => {
  const recoverQueueIDs = [];
  let storedQueueID = "queue-1";
  let clearCalls = 0;
  const stream = createStreamController({
    loadQueue: () => storedQueueID,
    saveQueue: (_, queueID) => {
      storedQueueID = queueID;
    },
    clearQueue: () => {
      clearCalls += 1;
      storedQueueID = "";
    },
    openStream: async ({ recoverQueueID }) => {
      recoverQueueIDs.push(recoverQueueID);
      throw new Error("retryable stream failure");
    },
    onStatus: () => {},
    onEvent: () => {},
  });

  stream.open("session-1");
  await sleep(1350);
  stream.close();

  assert.deepEqual(recoverQueueIDs, ["queue-1", "queue-1"]);
  assert.equal(clearCalls, 0);
});
