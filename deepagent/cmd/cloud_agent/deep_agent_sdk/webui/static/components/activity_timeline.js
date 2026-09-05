import { selectVisibleActivities } from "../state/selectors.js";
import { commandTool } from "../features/tools.js";

export function createActivityTimeline(root, actions) {
  if (!root) throw new Error("activity timeline root is missing");
  root.addEventListener("click", (event) => handleClick(event, actions));
  root.addEventListener("submit", (event) => handleSubmit(event, actions));

  return {
    render(state) {
      const activities = groupCommandActivities(selectVisibleActivities(state));
      const existing = new Map([...root.children].map((node) => [node.dataset.activityId, node]));
      const nodes = activities.map((activity) => {
        const current = existing.get(activity.id);
        const stateSnapshot = captureOpenState(current);
        const nextSignature = signature(activity, state.task.pending);
        const node = current?.dataset.signature === nextSignature
          ? current
          : withOpenState(activityNode(activity, state.task.pending), stateSnapshot);
        node.dataset.activityId = activity.id;
        node.dataset.signature = nextSignature;
        return node;
      });
      root.replaceChildren(...nodes);
    },
  };
}

function activityNode(activity, pending) {
  switch (activity.kind) {
    case "user":
      return textActivity("user-activity", activity.content);
    case "assistant":
      return assistantActivity(activity);
    case "tool":
      return toolActivity(activity);
    case "command-group":
      return commandGroupActivity(activity, pending);
    case "plan":
      return planActivity(activity);
    case "pending":
      return pendingActivity(activity, pending);
    case "error":
      return textActivity("system-activity error", activity.content);
    default:
      return textActivity("system-activity", activity.content || "Activity updated");
  }
}

function assistantActivity(activity) {
  const article = element("article", "assistant-activity");
  if (activity.thinking) {
    const thinking = element("details", "thinking-block");
    const summary = element("summary", "", activity.streaming ? "Thinking…" : "Thought process");
    const content = element("div", "rich-text", activity.thinking);
    thinking.append(summary, content);
    article.append(thinking);
  }
  article.append(element("div", "rich-text", activity.content));
  if (activity.streaming) article.append(element("span", "stream-cursor", ""));
  return article;
}

function toolActivity(activity) {
  const details = element("details", "tool-activity");
  const summary = element("summary", "tool-summary");
  const label = element("span", "tool-title", toolTitle(activity));
  const status = element("span", `tool-status ${toolStatus(activity)}`, toolStatus(activity));
  summary.append(label, status);
  details.append(summary);
  const body = element("div", "tool-body");
  if (activity.argumentsJSON) body.append(codeBlock(activity.argumentsJSON));
  if (activity.output) body.append(codeBlock(activity.output));
  if (activity.error) body.append(element("div", "tool-error", activity.error));
  details.append(body);
  return details;
}

function commandGroupActivity(activity) {
  const root = element("details", "command-group");
  const summary = element("summary", "command-summary");
  const title = element("span", "command-summary-title", commandGroupTitle(activity));
  const status = element("span", `command-group-status ${commandStatus(activity)}`, commandStatusLabel(activity));
  summary.append(title, status);
  root.append(summary);
  if (activity.commands?.length) {
    const list = element("div", "command-group-list");
    for (const command of activity.commands) {
      list.append(commandActivityItem(command));
    }
    root.append(list);
  }
  if (activity.status === "running" && !activity.commands?.every((item) => commandItemStatus(item) === "completed")) {
    root.open = true;
  }
  return root;
}

function commandActivityItem(activity) {
  const details = element("details", "command-item");
  details.dataset.commandId = String(activity.id || "");
  const summary = element("summary", "command-item-summary");
  const title = toolCommandText(activity);
  const status = commandItemStatus(activity);
  if (status === "running") details.open = true;
  summary.append(
    element("span", "command-item-title", title || "Command"),
    element("span", `tool-status ${status}`, commandItemStatusLabel(status)),
  );
  details.append(summary);
  const body = element("div", "tool-body");
  if (activity.argumentsJSON) body.append(codeBlock(activity.argumentsJSON));
  if (activity.output) body.append(codeBlock(activity.output));
  if (activity.error) body.append(element("div", "tool-error", activity.error));
  details.append(body);
  return details;
}

function commandStatus(activityGroup) {
  const hasError = activityGroup.commands.some((item) => item?.error);
  if (hasError) return "failed";
  const statuses = activityGroup.commands.map((item) => commandItemStatus(item));
  if (statuses.includes("running")) return "running";
  if (statuses.includes("failed")) return "failed";
  return "completed";
}

function commandStatusLabel(activityGroup) {
  if (activityGroup.status === "running") return "执行中";
  if (activityGroup.status === "failed") return "执行失败";
  return "执行完成";
}

function commandItemStatus(activity) {
  return toolStatus(activity);
}

function commandItemStatusLabel(status) {
  if (status === "running") return "正在运行";
  if (status === "failed") return "运行失败";
  return "已运行";
}

function toolCommandText(activity) {
  const command = activity?.arguments?.command ?? activity?.arguments?.cmd;
  if (Array.isArray(command)) return command.join(" ");
  return String(command || "");
}

function commandGroupTitle(activity) {
  const status = commandStatus(activity);
  if (status === "running") return "正在运行命令";
  if (status === "failed") return "命令执行失败";
  return "运行了命令";
}

function groupCommandActivities(activities) {
  const nodes = [];
  let activeGroup = null;

  for (const activity of activities) {
    if (!isCommandTool(activity)) {
      activeGroup = null;
      nodes.push(activity);
      continue;
    }
    if (!activeGroup || !canAppendToCommandGroup(activeGroup, activity)) {
      activeGroup = newCommandGroup(activity);
      nodes.push(activeGroup);
    }
    activeGroup.commands.push(activity);
    activeGroup.status = commandStatus(activeGroup);
  }
  return nodes;
}

function isCommandTool(activity) {
  return activity?.kind === "tool" && commandTool(activity);
}

function canAppendToCommandGroup(group, activity) {
  return group.threadID === activity.threadID && group.runID === activity.runID;
}

function newCommandGroup(activity) {
  return {
    id: `${activity.id}:group`,
    kind: "command-group",
    threadID: activity.threadID,
    runID: activity.runID,
    commands: [],
    status: commandStatus({ commands: [activity] }),
  };
}

function planActivity(activity) {
  const article = element("article", "plan-activity");
  const header = element("div", "plan-header");
  header.append(element("strong", "", "Plan"), element("span", "", planProgress(activity.steps)));
  article.append(header);
  if (activity.explanation) article.append(element("p", "plan-explanation", activity.explanation));
  const list = element("ol", "plan-steps");
  for (const step of activity.steps || []) {
    const item = element("li", `plan-step ${String(step.status || "pending").toLocaleLowerCase()}`);
    item.append(element("span", "plan-step-mark", stepMark(step.status)), element("span", "", step.step || ""));
    list.append(item);
  }
  article.append(list);
  return article;
}

function pendingActivity(activity, pending) {
  const active = pending?.id === activity.id;
  if (activity.pendingKind === "approval") return approvalActivity(activity, active, pending);
  if (activity.pendingKind === "plan") return planInputActivity(activity, active, pending);
  return interruptActivity(activity, active, pending);
}

function approvalActivity(activity, active, pending) {
  const approval = activity.event.approval || {};
  const article = element("article", `pending-card${active ? " active" : " handled"}`);
  article.append(pendingHeader("Approval required", approval.tool_name || "Tool call", active, pending));
  if (approval.arguments_json) article.append(codeBlock(approval.arguments_json));
  if (active) {
    const form = element("form", "approval-form");
    form.dataset.approvalForm = activity.id;
    const reason = element("textarea", "pending-textarea");
    reason.name = "reason";
    reason.rows = 2;
    reason.placeholder = "Optional feedback when rejecting";
    const reuse = element("label", "approval-reuse");
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.name = "allowInSession";
    reuse.append(checkbox, document.createTextNode(" Allow similar calls in this task"));
    const buttons = element("div", "pending-actions");
    const reject = button("Reject", "secondary-button");
    reject.dataset.approval = "reject";
    const approve = button("Approve", "primary-button");
    approve.dataset.approval = "approve";
    buttons.append(reject, approve);
    form.append(reason, reuse, buttons);
    article.append(form);
  }
  return article;
}

function planInputActivity(activity, active, pending) {
  const request = activity.event.plan_input_required || {};
  const article = element("article", `pending-card${active ? " active" : " handled"}`);
  article.append(pendingHeader("Plan input required", "The agent needs a decision", active, pending));
  if (active) {
    const form = element("form", "plan-input-form");
    form.dataset.planInputForm = activity.id;
    for (const [questionIndex, question] of (request.questions || []).entries()) {
      const fieldset = document.createElement("fieldset");
      fieldset.append(element("legend", "", question.question || question.header || "Choose an option"));
      for (const option of question.options || []) {
        const label = element("label", "plan-option");
        const input = document.createElement("input");
        input.type = "radio";
        input.name = `question-${questionIndex}`;
        input.value = [question.header, option.label, option.description].filter(Boolean).join(" - ");
        label.append(input, element("span", "", option.label || "Option"));
        fieldset.append(label);
      }
      form.append(fieldset);
    }
    form.append(pendingTextarea("Add your answer"), submitButton("Send answer"));
    article.append(form);
  }
  return article;
}

function interruptActivity(activity, active, pending) {
  const request = activity.event.interrupt_required || {};
  const questions = request.info?.questions || [];
  const article = element("article", `pending-card${active ? " active" : " handled"}`);
  article.append(pendingHeader("Follow-up required", questions.join(" · ") || "The agent needs more information", active, pending));
  if (active) {
    const form = element("form", "interrupt-form");
    form.dataset.interruptForm = activity.id;
    form.append(pendingTextarea("Add your answer"), submitButton("Send answer"));
    article.append(form);
  }
  return article;
}

function pendingHeader(title, subtitle, active, pending) {
  const header = element("div", "pending-header");
  const text = element("div", "");
  text.append(element("strong", "", title), element("p", "", subtitle));
  const status = pending?.submitting ? "Submitting" : active ? "Pending" : "Handled";
  header.append(text, element("span", "pending-status", status));
  if (pending?.error) header.append(element("p", "pending-error", pending.error));
  return header;
}

function handleClick(event, actions) {
  const action = event.target.closest("[data-approval]");
  if (!action) return;
  const form = action.closest("[data-approval-form]");
  const approved = action.dataset.approval === "approve";
  const options = {
    reason: form.elements.reason.value,
    allowInSession: form.elements.allowInSession.checked,
  };
  run(actions.submitApproval?.(form.dataset.approvalForm, approved, options));
}

function handleSubmit(event, actions) {
  const plan = event.target.closest("[data-plan-input-form]");
  if (plan) {
    event.preventDefault();
    const answers = [...plan.querySelectorAll("input:checked")].map((input) => input.value);
    run(actions.submitPlanInput?.(plan.dataset.planInputForm, answers, plan.querySelector("textarea").value));
    return;
  }
  const interrupt = event.target.closest("[data-interrupt-form]");
  if (interrupt) {
    event.preventDefault();
    run(actions.submitInterrupt?.(interrupt.dataset.interruptForm, interrupt.querySelector("textarea").value));
  }
}

function textActivity(className, content) {
  return element("article", className, content);
}

function codeBlock(content) {
  const pre = element("pre", "tool-code");
  pre.append(element("code", "", formatJSON(content)));
  return pre;
}

function pendingTextarea(placeholder) {
  const textarea = element("textarea", "pending-textarea");
  textarea.rows = 3;
  textarea.placeholder = placeholder;
  return textarea;
}

function submitButton(text) {
  const row = element("div", "pending-actions");
  const submit = button(text, "primary-button");
  submit.type = "submit";
  row.append(submit);
  return row;
}

function button(text, className) {
  const value = element("button", className, text);
  value.type = "button";
  return value;
}

function element(tag, className, text) {
  const value = document.createElement(tag);
  if (className) value.className = className;
  if (text !== undefined) value.textContent = text;
  return value;
}

function formatJSON(value) {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

function signature(activity, pending) {
  const pendingState = pending?.id === activity.id
    ? { submitting: pending.submitting, error: pending.error }
    : null;
  return JSON.stringify([activity, pendingState], (_, value) => value instanceof Set || value instanceof Map ? [...value] : value);
}

function toolTitle(activity) {
  return activity.toolName || "Tool";
}

function toolStatus(activity) {
  if (activity.error || Number(activity.status) === 3) return "failed";
  if (activity.status === "completed" || Number(activity.status) === 2) return "completed";
  return "running";
}

function planProgress(steps) {
  const items = steps || [];
  if (!items.length) return "";
  const completed = items.filter((step) => step.status === "completed").length;
  return `${completed}/${items.length}`;
}

function stepMark(status) {
  if (status === "completed") return "✓";
  if (status === "in_progress") return "●";
  return "○";
}

function run(promise) {
  promise?.catch(() => {});
}

function captureOpenState(node) {
  if (!node) return null;
  const commandOpen = {};
  node.querySelectorAll("[data-command-id]").forEach((item) => {
    commandOpen[item.dataset.commandId] = item.open;
  });
  return {
    rootOpen: node.tagName === "DETAILS" && node.open,
    commandOpen,
  };
}

function withOpenState(node, stateSnapshot) {
  if (!node || !stateSnapshot) return node;
  if (node.tagName === "DETAILS" && typeof stateSnapshot.rootOpen === "boolean") {
    node.open = stateSnapshot.rootOpen;
  }
  for (const detail of node.querySelectorAll("[data-command-id]")) {
    const restore = stateSnapshot.commandOpen[detail.dataset.commandId];
    if (typeof restore === "boolean") detail.open = restore;
  }
  return node;
}
