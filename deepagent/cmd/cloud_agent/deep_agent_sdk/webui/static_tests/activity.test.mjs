import assert from "node:assert/strict";
import test from "node:test";

import { toActivities } from "../static/state/activity.js";

test("terminal events stop the matching assistant indicator without erasing partial text", () => {
  for (const type of ["TURN_INTERRUPTED", "TURN_FINISHED", "ERROR"]) {
    const activities = toActivities([
      event("1", "ASSISTANT_DELTA", { assistant_delta: { delta: "partial" } }),
      event("2", "ASSISTANT_DELTA", { thread_id: "child", assistant_delta: { delta: "still running" } }),
      event("3", type),
    ]);
    assert.equal(activities[0].streaming, false);
    assert.equal(activities[0].content, "partial");
    assert.equal(activities[1].streaming, true);
  }
});

test("interleaved threads keep separate assistant text and final messages", () => {
  const activities = toActivities([
    event("1", "ASSISTANT_DELTA", { assistant_delta: { delta: "main " } }),
    event("2", "ASSISTANT_DELTA", { thread_id: "child", assistant_delta: { delta: "child " } }),
    event("3", "ASSISTANT_MESSAGE", { thread_id: "child", message: { parts: [{ type: "text", text: "child done" }] } }),
    event("4", "ASSISTANT_DELTA", { assistant_delta: { delta: "continues" } }),
  ]);
  assert.deepEqual(activities.map(({ threadID, content }) => ({ threadID, content })), [
    { threadID: "t1", content: "main continues" },
    { threadID: "child", content: "child done" },
  ]);
});

test("tool call ids are scoped to their thread and run", () => {
  const activities = toActivities([
    event("1", "TOOL_CALL_STARTED", { tool_call: { tool_call_id: "same", tool_name: "execute" } }),
    event("2", "TOOL_CALL_STARTED", { thread_id: "child", tool_call: { tool_call_id: "same", tool_name: "execute" } }),
    event("3", "TOOL_CALL_OUTPUT_DELTA", { tool_call: { tool_call_id: "same", output_delta: "main" } }),
    event("4", "TOOL_CALL_OUTPUT_DELTA", { thread_id: "child", tool_call: { tool_call_id: "same", output_delta: "child" } }),
  ]);
  assert.deepEqual(activities.map(({ threadID, output }) => ({ threadID, output })), [
    { threadID: "t1", output: "main" }, { threadID: "child", output: "child" },
  ]);
});

test("completed tool output is identical in history and after streamed chunks", () => {
  const final = event("2", "TOOL_CALL_FINISHED", { tool_call: {
    tool_call_id: "c1", tool_name: "exec_command", status: 2,
    result_json: JSON.stringify({ output: "complete output", exit_code: 0 }),
  } });
  const streamed = toActivities([
    event("1", "TOOL_CALL_OUTPUT_DELTA", { tool_call: { tool_call_id: "c1", output_delta: "partial" } }), final,
  ]);
  const history = toActivities([final]);
  assert.equal(history[0].output, "complete output");
  assert.equal(streamed[0].output, history[0].output);
});

test("folds assistant and tool streams into stable activities", () => {
  const activities = toActivities([
    event("1", "TURN_STARTED", {
      message: { parts: [{ type: "text", text: "inspect" }] },
    }),
    event("2", "ASSISTANT_DELTA", {
      assistant_delta: { delta: "I will ", thinking_content_delta: "checking " },
    }),
    event("3", "ASSISTANT_DELTA", {
      assistant_delta: { delta: "inspect.", thinking_content_delta: "files" },
    }),
    event("4", "TOOL_CALL_STARTED", {
      tool_call: {
        tool_call_id: "c1",
        tool_name: "exec_command",
        arguments_json: "{\"cmd\":\"pwd\"}",
      },
    }),
    event("5", "TOOL_CALL_OUTPUT_DELTA", {
      tool_call: { tool_call_id: "c1", output_delta: "/repo" },
    }),
    event("6", "TOOL_CALL_FINISHED", {
      tool_call: {
        tool_call_id: "c1",
        status: "completed",
        result_json: "{\"exit_code\":0}",
      },
    }),
  ]);

  assert.deepEqual(activities.map((item) => item.kind), ["user", "assistant", "tool"]);
  assert.equal(activities[0].content, "inspect");
  assert.equal(activities[1].id, "assistant:t1:r1");
  assert.equal(activities[1].content, "I will inspect.");
  assert.equal(activities[1].thinking, "checking files");
  assert.equal(activities[2].id, "tool:t1:r1:c1");
  assert.deepEqual(activities[2].arguments, { cmd: "pwd" });
  assert.equal(activities[2].output, "/repo");
  assert.equal(activities[2].status, "completed");
  assert.equal(activities[2].exitCode, 0);
});

test("replaces streamed assistant text with the final message", () => {
  const activities = toActivities([
    event("1", "ASSISTANT_DELTA", {
      assistant_delta: { delta: "part" },
    }),
    event("2", "ASSISTANT_MESSAGE", {
      message: {
        parts: [{ type: "text", text: "final answer" }],
        thinking_content: "done",
      },
    }),
  ]);

  assert.equal(activities.length, 1);
  assert.deepEqual(activities[0], {
    id: "assistant:t1:r1",
    kind: "assistant",
    threadID: "t1",
    runID: "r1",
    content: "final answer",
    thinking: "done",
    streaming: false,
  });
});

test("keeps only the latest plan for a run", () => {
  const activities = toActivities([
    event("1", "PLAN_UPDATED", { plan: { explanation: "first", steps: [] } }),
    event("2", "PLAN_UPDATED", { plan: { explanation: "second", steps: [{ step: "ship", status: "pending" }] } }),
  ]);

  assert.equal(activities.length, 1);
  assert.equal(activities[0].kind, "plan");
  assert.equal(activities[0].explanation, "second");
  assert.equal(activities[0].steps[0].step, "ship");
});

function event(id, type, fields = {}) {
  return {
    event_id: id,
    event_type: type,
    thread_id: "t1",
    turn_id: "r1",
    created_at_ms: Number(id),
    ...fields,
  };
}
