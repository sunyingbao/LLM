import assert from "node:assert/strict";
import test from "node:test";

import { defaultInspectorCollapsed } from "../static/features/index.js";

test("starts the inspector open on wide screens and collapsed on narrow screens", () => {
  assert.equal(defaultInspectorCollapsed(1536, null), false);
  assert.equal(defaultInspectorCollapsed(1179, null), true);
  assert.equal(defaultInspectorCollapsed(899, null), true);
});

test("restores an explicit inspector preference on every viewport", () => {
  assert.equal(defaultInspectorCollapsed(1536, "true"), true);
  assert.equal(defaultInspectorCollapsed(899, "false"), false);
});
