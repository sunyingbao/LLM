export const EventType = {
  ASSISTANT_MESSAGE: "ASSISTANT_MESSAGE",
  ASSISTANT_DELTA: "ASSISTANT_DELTA",
  TOOL_CALL_STARTED: "TOOL_CALL_STARTED",
  TOOL_CALL_OUTPUT_DELTA: "TOOL_CALL_OUTPUT_DELTA",
  TOOL_CALL_FINISHED: "TOOL_CALL_FINISHED",
  APPROVAL_REQUIRED: "APPROVAL_REQUIRED",
  INTERRUPT_REQUIRED: "INTERRUPT_REQUIRED",
  PLAN_UPDATED: "PLAN_UPDATED",
  PLAN_INPUT_REQUIRED: "PLAN_INPUT_REQUIRED",
  CONTEXT_COMPACTED: "CONTEXT_COMPACTED",
  TURN_STARTED: "TURN_STARTED",
  TURN_FINISHED: "TURN_FINISHED",
  TURN_INTERRUPTED: "TURN_INTERRUPTED",
  ERROR: "ERROR",
};

export const InputMode = {
  IMPL_PLAN: 1,
  COMPACT_CONTEXT: 2,
};

export function createState() {
  return {
    projects: [],
    selectedProjectName: "",
    userInfo: null,
    sessions: [],
    selectedSessionID: "",
    selectedThreadID: "",
    sessionView: null,
    events: [],
    eventIDs: new Set(),
    nextEventSeq: 1,
    pendingUserEventIDs: new Map(),
    resolvedPendingInputs: new Map(),
    nextCursor: "",
    running: false,
    stopRequested: false,
    planMode: false,
    contextUsage: null,
    pollTimer: null,
    fileTree: new Map(),
    selectedFilePath: "",
    filePreview: null,
    filesCollapsed: false,
    forceTimelineScroll: false,
  };
}

export function normalizeID(value) {
  if (value === undefined || value === null) return "";
  return String(value);
}

export function mergeEvents(state, events) {
  let changed = false;
  for (const event of events || []) {
    if (event.event_type === EventType.ASSISTANT_MESSAGE) {
      removeEventByID(state, assistantDeltaEventID(event));
    }
    const rawID = rawEventID(event);
    const coalescedID = coalescedStreamEventID(event);
    if (coalescedID) {
      const existing = state.events.find((item) => item.__stable_id === coalescedID);
      if (existing) {
        if (hasMergedRawEvent(existing, rawID)) continue;
        rememberMergedRawEvent(existing, rawID);
        mergeStreamEvent(existing, event);
        if (event.context_usage) state.contextUsage = event.context_usage;
        if (!event.__local) state.running = false;
        changed = true;
        continue;
      }
    }
    const id = coalescedID || rawID;
    if (state.eventIDs.has(id)) continue;
    forgetLocalUserEvent(state, event);
    event.__stable_id = id;
    event.__arrival_seq = state.nextEventSeq++;
    if (coalescedID) {
      rememberMergedRawEvent(event, rawID);
    }
    state.eventIDs.add(id);
    state.events.push(event);
    if (event.context_usage) state.contextUsage = event.context_usage;
    if (!event.__local) state.running = false;
    changed = true;
  }
  if (changed) {
    state.events.sort(compareEvents);
  }
  return changed;
}

function rawEventID(event) {
  const eventID = normalizeEventID(event?.event_id);
  if (eventID) return eventID;
  return `${event?.thread_id || ""}:${event?.turn_id || ""}:${event?.created_at_ms || ""}:${event?.event_type || ""}`;
}

function normalizeEventID(value) {
  const id = normalizeID(value);
  return id === "0" ? "" : id;
}

function coalescedStreamEventID(event) {
  if (!event) return "";
  switch (event.event_type) {
    case EventType.ASSISTANT_DELTA:
      return assistantDeltaEventID(event);
    case EventType.TOOL_CALL_OUTPUT_DELTA:
      return toolOutputDeltaEventID(event);
    default:
      return "";
  }
}

function assistantDeltaEventID(event) {
  const threadID = normalizeID(event?.thread_id);
  const turnID = normalizeID(event?.turn_id);
  if (!threadID || !turnID) return "";
  return `stream_delta:assistant:${threadID}:${turnID}`;
}

function toolOutputDeltaEventID(event) {
  const threadID = normalizeID(event?.thread_id);
  const turnID = normalizeID(event?.turn_id);
  const tool = event?.tool_call || {};
  const toolID = normalizeID(tool.tool_call_id || tool.tool_name);
  if (!threadID || !turnID || !toolID) return "";
  return `stream_delta:tool:${threadID}:${turnID}:${toolID}`;
}

function mergeStreamEvent(existing, event) {
  if (event.event_type === EventType.ASSISTANT_DELTA) {
    const delta = event.assistant_delta?.delta || "";
    const thinkingDelta = event.assistant_delta?.thinking_content_delta || "";
    if (delta || thinkingDelta) {
      existing.assistant_delta = {
        ...(existing.assistant_delta || {}),
        delta: `${existing.assistant_delta?.delta || ""}${delta}`,
        thinking_content_delta: `${existing.assistant_delta?.thinking_content_delta || ""}${thinkingDelta}`,
      };
    }
    return;
  }
  if (event.event_type === EventType.TOOL_CALL_OUTPUT_DELTA) {
    const current = existing.tool_call || {};
    const next = event.tool_call || {};
    existing.tool_call = { ...current, ...next };
    if (next.output_delta) {
      existing.tool_call.output_delta = `${current.output_delta || ""}${next.output_delta}`;
    }
  }
}

function hasMergedRawEvent(event, rawID) {
  return Boolean(rawID && event.__merged_raw_event_ids?.has(rawID));
}

function rememberMergedRawEvent(event, rawID) {
  if (!rawID) return;
  if (!event.__merged_raw_event_ids) {
    event.__merged_raw_event_ids = new Set();
  }
  event.__merged_raw_event_ids.add(rawID);
}

function removeEventByID(state, id) {
  if (!id || !state.eventIDs.has(id)) return false;
  state.eventIDs.delete(id);
  state.events = state.events.filter((event) => event.__stable_id !== id);
  return true;
}

export function appendLocalUserMessage(state, message, content) {
  const messageID = normalizeID(message?.message_id);
  if (!messageID || !content) return false;
  const id = `local_user:${messageID}`;
  if (state.eventIDs.has(id)) return false;
  state.pendingUserEventIDs.set(messageID, id);
  state.eventIDs.add(id);
  state.events.push({
    __stable_id: id,
    __arrival_seq: state.nextEventSeq++,
    event_id: id,
    event_type: EventType.TURN_STARTED,
    session_id: state.selectedSessionID,
    thread_id: normalizeID(message?.thread_id || state.selectedThreadID),
    created_at_ms: Date.now(),
    message: {
      parts: [{ type: "text", text: content }],
      message_id: messageID,
    },
    __local: true,
  });
  return true;
}

function compareEvents(a, b) {
  const at = Number(a.created_at_ms || 0);
  const bt = Number(b.created_at_ms || 0);
  if (at !== bt) return at - bt;
  const aid = String(a.event_id || a.__stable_id || "");
  const bid = String(b.event_id || b.__stable_id || "");
  if (aid !== bid) return aid.localeCompare(bid);
  return Number(a.__arrival_seq || 0) - Number(b.__arrival_seq || 0);
}

function forgetLocalUserEvent(state, event) {
  const messageID = normalizeID(event?.message?.message_id);
  const consumed = Array.isArray(event?.consumed_message_ids) ? event.consumed_message_ids.map(normalizeID) : [];
  const ids = messageID ? [messageID, ...consumed] : consumed;
  for (const id of ids) {
    const localEventID = state.pendingUserEventIDs.get(id);
    if (!localEventID) continue;
    state.pendingUserEventIDs.delete(id);
    state.eventIDs.delete(localEventID);
    state.events = state.events.filter((item) => item.__stable_id !== localEventID);
  }
}

export function resetTimeline(state) {
  state.events = [];
  state.eventIDs = new Set();
  state.nextEventSeq = 1;
  state.pendingUserEventIDs = new Map();
  state.resolvedPendingInputs = new Map();
  state.nextCursor = "";
  state.running = false;
  state.stopRequested = false;
  state.contextUsage = null;
}

export function currentSession(state) {
  if (state.sessionView?.session) return state.sessionView.session;
  return state.sessions.find((session) => normalizeID(session.session_id) === normalizeID(state.selectedSessionID));
}

export function currentThread(state) {
  const threads = (state.sessionView?.threads || []).filter((thread) => Number(thread.status || 0) !== 6);
  if (state.selectedThreadID) {
    const selected = threads.find((thread) => normalizeID(thread.thread_id) === normalizeID(state.selectedThreadID));
    if (selected) return selected;
  }
  return threads.find((thread) => Number(thread.role || 0) === 1) || threads[0] || null;
}
