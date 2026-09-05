export function toActivities(events) {
  const activities = [];
  const tools = new Map();
  const plans = new Map();
  const assistantCounts = new Map();
  const assistants = new Map();

  for (const event of events || []) {
    const runKey = `${text(event?.thread_id)}:${text(event?.turn_id)}`;
    let assistant = assistants.get(runKey) || null;
    if (assistant && ["TURN_FINISHED", "TURN_INTERRUPTED", "ERROR"].includes(event?.event_type)) {
      assistant.streaming = false;
    }
    switch (event?.event_type) {
      case "TURN_STARTED":
        assistant = null;
        appendUser(activities, event);
        break;
      case "ASSISTANT_DELTA":
        assistant = appendAssistantDelta(activities, assistant, assistantCounts, event);
        break;
      case "ASSISTANT_MESSAGE":
        assistant = finishAssistant(activities, assistant, assistantCounts, event);
        break;
      case "TOOL_CALL_STARTED":
      case "TOOL_CALL_OUTPUT_DELTA":
      case "TOOL_CALL_FINISHED":
        assistant = null;
        mergeTool(activities, tools, event);
        break;
      case "PLAN_UPDATED":
        assistant = null;
        mergePlan(activities, plans, event);
        break;
      case "APPROVAL_REQUIRED":
      case "PLAN_INPUT_REQUIRED":
      case "INTERRUPT_REQUIRED":
        assistant = null;
        appendPending(activities, event);
        break;
      case "CONTEXT_COMPACTED":
        assistant = null;
        appendSystem(activities, event, "Context compacted");
        break;
      case "TURN_INTERRUPTED":
        assistant = null;
        appendSystem(activities, event, "Run interrupted");
        break;
      case "ERROR":
        assistant = null;
        appendError(activities, event);
        break;
      default:
        assistant = null;
        break;
    }
    if (assistant) assistants.set(runKey, assistant);
    else assistants.delete(runKey);
  }

  return activities;
}

function appendUser(activities, event) {
  const content = messageText(event.message);
  if (!content) return;
  activities.push({
    id: eventID(event, "user"),
    kind: "user",
    threadID: text(event.thread_id),
    runID: text(event.turn_id),
    content,
    local: Boolean(event.__local),
  });
}

function appendAssistantDelta(activities, current, counts, event) {
  const content = text(event.assistant_delta?.delta);
  const thinking = text(event.assistant_delta?.thinking_content_delta);
  if (!content && !thinking) return current;
  const assistant = current || newAssistant(activities, counts, event);
  assistant.content += content;
  assistant.thinking += thinking;
  assistant.streaming = true;
  return assistant;
}

function finishAssistant(activities, current, counts, event) {
  const content = messageText(event.message);
  const thinking = text(event.message?.thinking_content || event.message?.thinkingContent);
  if (!content && !thinking) return null;
  const assistant = current || newAssistant(activities, counts, event);
  assistant.content = content;
  assistant.thinking = thinking;
  assistant.streaming = false;
  return null;
}

function newAssistant(activities, counts, event) {
  const runKey = `${text(event.thread_id)}:${text(event.turn_id)}`;
  const count = (counts.get(runKey) || 0) + 1;
  counts.set(runKey, count);
  const suffix = count === 1 ? "" : `:${count}`;
  const assistant = {
    id: `assistant:${runKey}${suffix}`,
    kind: "assistant",
    threadID: text(event.thread_id),
    runID: text(event.turn_id),
    content: "",
    thinking: "",
    streaming: false,
  };
  activities.push(assistant);
  return assistant;
}

function mergeTool(activities, tools, event) {
  const tool = event.tool_call || {};
  const key = `${text(event.thread_id)}:${text(event.turn_id)}:${toolKey(event)}`;
  let activity = tools.get(key);
  if (!activity) {
    activity = {
      id: `tool:${key}`,
      kind: "tool",
      threadID: text(event.thread_id),
      runID: text(event.turn_id),
      toolCallID: text(tool.tool_call_id),
      toolName: "",
      arguments: null,
      argumentsJSON: "",
      output: "",
      result: null,
      resultJSON: "",
      status: "running",
      exitCode: null,
      error: "",
    };
    tools.set(key, activity);
    activities.push(activity);
  }
  if (tool.tool_name) activity.toolName = tool.tool_name;
  if (tool.arguments_json) {
    activity.argumentsJSON = tool.arguments_json;
    activity.arguments = parseJSON(tool.arguments_json);
  }
  if (tool.output_delta) activity.output += tool.output_delta;
  if (tool.result_json) {
    activity.resultJSON = tool.result_json;
    activity.result = parseJSON(tool.result_json);
    activity.exitCode = numberOrNull(activity.result?.exit_code ?? activity.result?.exitCode);
    const result = activity.result;
    if (typeof result?.output === "string") {
      activity.output = result.output;
    } else if (result?.stdout !== undefined || result?.stderr !== undefined) {
      activity.output = [result.stdout, result.stderr].filter(Boolean).join("\n");
    } else if (!activity.output) {
      activity.output = typeof result?.data === "string" ? result.data : activity.resultJSON;
    }
  }
  if (tool.error_message) activity.error = tool.error_message;
  if (tool.status !== undefined && tool.status !== null && tool.status !== "") {
    activity.status = tool.status;
  } else if (event.event_type === "TOOL_CALL_FINISHED") {
    activity.status = activity.error ? "failed" : "completed";
  }
}

function mergePlan(activities, plans, event) {
  const key = `${text(event.thread_id)}:${text(event.turn_id)}`;
  const plan = event.plan_updated || event.plan || {};
  let activity = plans.get(key);
  if (!activity) {
    activity = {
      id: `plan:${key}`,
      kind: "plan",
      threadID: text(event.thread_id),
      runID: text(event.turn_id),
      explanation: "",
      steps: [],
    };
    plans.set(key, activity);
    activities.push(activity);
  }
  activity.explanation = text(plan.explanation);
  activity.steps = Array.isArray(plan.plan) ? plan.plan : Array.isArray(plan.steps) ? plan.steps : [];
}

function appendPending(activities, event) {
  activities.push({
    id: eventID(event, "pending"),
    kind: "pending",
    pendingKind: pendingKind(event.event_type),
    threadID: text(event.thread_id),
    runID: text(event.turn_id),
    event,
  });
}

function appendSystem(activities, event, content) {
  activities.push({
    id: eventID(event, "system"),
    kind: "system",
    threadID: text(event.thread_id),
    runID: text(event.turn_id),
    content,
  });
}

function appendError(activities, event) {
  activities.push({
    id: eventID(event, "error"),
    kind: "error",
    threadID: text(event.thread_id),
    runID: text(event.turn_id),
    content: text(event.error?.message || event.error?.detail || "Error"),
  });
}

function pendingKind(type) {
  if (type === "APPROVAL_REQUIRED") return "approval";
  if (type === "PLAN_INPUT_REQUIRED") return "plan";
  return "interrupt";
}

function messageText(message) {
  return (Array.isArray(message?.parts) ? message.parts : [])
    .filter((part) => part?.type === "text" && part.text)
    .map((part) => part.text)
    .join("");
}

function toolKey(event) {
  const tool = event.tool_call || {};
  return text(tool.tool_call_id) || `${text(tool.tool_name)}:${text(tool.arguments_json)}`;
}

function eventID(event, prefix) {
  return text(event.event_id) || `${prefix}:${text(event.thread_id)}:${text(event.turn_id)}:${Number(event.created_at_ms || 0)}`;
}

function parseJSON(value) {
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

function numberOrNull(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
}

function text(value) {
  return value === undefined || value === null ? "" : String(value);
}
