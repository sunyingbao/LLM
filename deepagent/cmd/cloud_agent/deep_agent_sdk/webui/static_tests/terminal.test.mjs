import assert from "node:assert/strict";
import test from "node:test";

import { terminalEntries } from "../static/features/terminal.js";

test("projects command tools into terminal entries", () => {
  const entries = terminalEntries([
    {
      kind: "tool",
      id: "c1",
      toolName: "exec_command",
      arguments: { cmd: "go test ./..." },
      output: "ok",
      status: "completed",
      exitCode: 0,
    },
    { kind: "tool", id: "c2", toolName: "read_file", status: "completed" },
  ]);

  assert.deepEqual(entries, [{
    id: "c1",
    command: "go test ./...",
    output: "ok",
    status: "completed",
    exitCode: 0,
  }]);
});

test("reads command and output from completed tool results", () => {
  const entries = terminalEntries([{
    kind: "tool",
    id: "c1",
    toolName: "execute",
    arguments: { command: ["go", "test", "./deepagent/..."] },
    result: { output: "FAIL", exit_code: 1 },
    status: 3,
  }]);

  assert.equal(entries[0].command, "go test ./deepagent/...");
  assert.equal(entries[0].output, "FAIL");
  assert.equal(entries[0].exitCode, 1);
  assert.equal(entries[0].status, "failed");
});
