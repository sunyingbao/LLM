import assert from "node:assert/strict";
import test from "node:test";

import { createComposer } from "../static/components/composer.js";

test("clears the focused composer after submission", () => {
  const elements = {
    "[data-composer]": element({ value: "sent message", scrollHeight: 40 }),
    "[data-send]": element(),
    "[data-plan-mode]": element(),
    "[data-compact]": element(),
    "[data-composer-status]": element(),
    "[data-pending-slot]": element(),
  };
  const root = {
    querySelector(selector) {
      return elements[selector];
    },
  };
  globalThis.document = { activeElement: elements["[data-composer]"] };
  const composer = createComposer(root, {});

  composer.render({
    catalog: { selectedTaskID: "1" },
    task: { draft: "", pending: null, planMode: false, runState: "running", submitError: "" },
    ui: { error: "" },
  });

  assert.equal(elements["[data-composer]"].value, "");
});

function element(fields = {}) {
  return {
    value: "",
    dataset: {},
    style: {},
    classList: { toggle() {} },
    addEventListener() {},
    setAttribute() {},
    replaceChildren() {},
    ...fields,
  };
}
