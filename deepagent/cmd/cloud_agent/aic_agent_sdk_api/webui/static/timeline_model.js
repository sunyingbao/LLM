import { EventType, currentThread, normalizeID } from "./state.js";

export function buildTimelineModel(state) {
  const pending = latestPendingInput(state.events, state);
  const items = buildTimelineItems(state.events, state);
  const runtime = runtimeState(state, pending);
  if (runtime.active && !pending) {
    items.push({
      type: "running",
      writing: activeTurnHasAssistantDelta(state.events, state),
    });
  }
  return { items, pending, runtime };
}

export function latestPendingInput(events, state) {
  const threadID = selectedThreadID(state);
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i];
    if (!eventBelongsToThread(event, threadID)) continue;
    const type = event.event_type;
    if (type === EventType.APPROVAL_REQUIRED || type === EventType.PLAN_INPUT_REQUIRED || type === EventType.INTERRUPT_REQUIRED) {
      return state?.resolvedPendingInputs?.has(event.__stable_id) ? null : event;
    }
    if (type === EventType.TURN_STARTED || type === EventType.TURN_FINISHED || type === EventType.TURN_INTERRUPTED || type === EventType.ERROR) {
      return null;
    }
  }
  return null;
}

export function pendingResolution(event, state) {
  return state?.resolvedPendingInputs?.get(event.__stable_id) || null;
}

export function runtimeState(state, pending = latestPendingInput(state.events, state)) {
  if (state.stopRequested) return { label: "stopping", className: "stopping", active: false };
  if (pending) {
    if (pending.event_type === EventType.APPROVAL_REQUIRED) {
      return { label: "waiting approval", className: "blocked", active: false };
    }
    if (pending.event_type === EventType.INTERRUPT_REQUIRED) {
      return { label: "waiting follow-up", className: "blocked", active: false };
    }
    return { label: "waiting input", className: "blocked", active: false };
  }

  const threadRuntime = runtimeFromThreadStatus(currentThread(state));
  const lifecycle = latestTurnLifecycle(state.events, state);
  if (lifecycle?.state === "running") {
    if (!lifecycle.event?.__local && threadRuntime && !threadRuntime.active) return threadRuntime;
    return { label: "running", className: "running", active: true };
  }
  if (lifecycle?.state === "finished" || lifecycle?.state === "interrupted") return { label: "idle", className: "", active: false };
  if (lifecycle?.state === "error") return { label: "error", className: "error", active: false };
  if (threadRuntime) return threadRuntime;
  return state.running ? { label: "running", className: "running", active: true } : { label: "idle", className: "", active: false };
}

function runtimeFromThreadStatus(thread) {
  switch (Number(thread?.status || 0)) {
    case 1:
      return { label: "idle", className: "", active: false };
    case 2:
      return { label: "ready", className: "", active: false };
    case 3:
      return { label: "running", className: "running", active: true };
    case 4:
      return { label: "blocked", className: "blocked", active: false };
    case 5:
      return { label: "closing", className: "stopping", active: false };
    case 6:
      return { label: "closed", className: "", active: false };
    default:
      return null;
  }
}

function buildTimelineItems(events, state) {
  const threadID = selectedThreadID(state);
  const items = [];
  const tools = new Map();
  const plans = new Map();
  let assistant = null;

  for (const event of events) {
    if (!eventBelongsToThread(event, threadID)) continue;
    switch (event.event_type) {
      case EventType.TURN_STARTED:
        assistant = null;
        if (messageContent(event.message)) items.push({ type: "user", content: messageContent(event.message) });
        break;
      case EventType.ASSISTANT_DELTA:
        assistant = appendAssistantDelta(items, assistant, event);
        break;
      case EventType.ASSISTANT_MESSAGE:
        assistant = finishAssistantMessage(items, assistant, event);
        break;
      case EventType.TOOL_CALL_STARTED:
      case EventType.TOOL_CALL_OUTPUT_DELTA:
      case EventType.TOOL_CALL_FINISHED:
        assistant = null;
        upsertToolItem(items, tools, event);
        break;
      case EventType.PLAN_UPDATED:
        assistant = null;
        upsertPlanItem(items, plans, event);
        break;
      case EventType.CONTEXT_COMPACTED:
        assistant = null;
        items.push({ type: "system", text: "Context compacted" });
        break;
      case EventType.TURN_INTERRUPTED:
        assistant = null;
        items.push({ type: "system", text: "Turn interrupted" });
        break;
      case EventType.ERROR:
        assistant = null;
        items.push({ type: "system", text: event.error?.message || "Error", kind: "error" });
        break;
      default:
        break;
    }
  }

  return items.filter((item) => item.type !== "tool" || !shouldHideTool(item.tool || {}));
}

function appendAssistantDelta(items, current, event) {
  const delta = event.assistant_delta?.delta || "";
  const thinkingDelta = event.assistant_delta?.thinking_content_delta || "";
  if (!delta && !thinkingDelta) return current;
  if (current?.streaming) {
    current.content += delta;
    current.thinkingContent = `${current.thinkingContent || ""}${thinkingDelta}`;
    return current;
  }
  const item = { type: "assistant", content: delta, thinkingContent: thinkingDelta, streaming: true };
  items.push(item);
  return item;
}

function finishAssistantMessage(items, current, event) {
  const content = messageContent(event.message);
  const thinkingContent = messageThinkingContent(event.message);
  if (!String(content).trim() && !String(thinkingContent).trim()) return null;
  if (current?.streaming) {
    current.content = content;
    current.thinkingContent = thinkingContent;
    current.streaming = false;
    return null;
  }
  items.push({ type: "assistant", content, thinkingContent, streaming: false });
  return null;
}

function messageContent(message) {
  const parts = Array.isArray(message?.parts) ? message.parts : [];
  return parts
    .filter((part) => part?.type === "text" && part.text)
    .map((part) => part.text)
    .join("");
}

function messageThinkingContent(message) {
  return message?.thinking_content || message?.thinkingContent || "";
}

function upsertToolItem(items, tools, event) {
  const key = toolEventKey(event);
  if (!key) return;
  let item = tools.get(key);
  if (!item) {
    item = { type: "tool", tool: {} };
    tools.set(key, item);
    items.push(item);
  }
  item.tool = mergeTool(item.tool, event.tool_call || {});
}

function mergeTool(existing, next) {
  const tool = { ...existing, ...next };
  if (!tool.tool_name) tool.tool_name = existing.tool_name || next.tool_name || "";
  if (!tool.arguments_json) tool.arguments_json = existing.arguments_json || next.arguments_json || "";
  if (next.output_delta) {
    tool.output_delta = `${existing.output_delta || ""}${next.output_delta}`;
  }
  if (!tool.result_json) tool.result_json = existing.result_json || next.result_json || "";
  if (!tool.error_message) tool.error_message = existing.error_message || next.error_message || "";
  return tool;
}

function upsertPlanItem(items, plans, event) {
  const key = turnEventKey(event);
  let item = plans.get(key);
  if (!item) {
    item = { type: "plan", event };
    plans.set(key, item);
    items.push(item);
    return;
  }
  item.event = event;
}

function latestTurnLifecycle(events, state) {
  const threadID = selectedThreadID(state);
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i];
    if (!eventBelongsToThread(event, threadID)) continue;
    switch (event.event_type) {
      case EventType.TURN_STARTED:
        return { state: "running", event };
      case EventType.TURN_FINISHED:
        return { state: "finished", event };
      case EventType.TURN_INTERRUPTED:
        return { state: "interrupted", event };
      case EventType.ERROR:
        return { state: "error", event };
      default:
        break;
    }
  }
  return null;
}

function activeTurnHasAssistantDelta(events, state) {
  const threadID = selectedThreadID(state);
  let sawDelta = false;
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i];
    if (!eventBelongsToThread(event, threadID)) continue;
    switch (event.event_type) {
      case EventType.ASSISTANT_DELTA:
        sawDelta = true;
        break;
      case EventType.TURN_STARTED:
        return sawDelta;
      case EventType.TURN_FINISHED:
      case EventType.TURN_INTERRUPTED:
      case EventType.ERROR:
      case EventType.APPROVAL_REQUIRED:
      case EventType.PLAN_INPUT_REQUIRED:
      case EventType.INTERRUPT_REQUIRED:
        return false;
      default:
        break;
    }
  }
  return sawDelta;
}

function selectedThreadID(state) {
  return normalizeID(currentThread(state)?.thread_id || state?.selectedThreadID);
}

function eventBelongsToThread(event, threadID) {
  return !threadID || normalizeID(event.thread_id) === threadID;
}

function shouldHideTool(tool) {
  if ((tool.tool_name || "").toLowerCase() !== "ls") return false;
  if (Number(tool.status) !== 2) return false;
  const result = parseJSON(tool.result_json);
  if (!result) return false;
  return (result.data === null || result.data === undefined || (Array.isArray(result.data) && result.data.length === 0)) && !result.errmsg;
}

function parseJSON(raw) {
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch (error) {
    return null;
  }
}

function turnEventKey(event) {
  return `${event.thread_id || ""}:${event.turn_id || ""}`;
}

function toolEventKey(event) {
  const tool = event.tool_call || {};
  if (tool.tool_call_id) return tool.tool_call_id;
  return `${event.thread_id || ""}:${event.turn_id || ""}:${tool.tool_name || ""}:${tool.arguments_json || ""}`;
}
