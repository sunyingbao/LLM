export function createTimelineAPI(request, streamRequest) {
  return {
    listTimeline: ({ sessionID, threadID, cursor, limit = 200, backward = false, signal }) => {
      const body = { session_id: String(sessionID), limit };
      if (threadID) body.thread_id = String(threadID);
      if (cursor) body.cursor = String(cursor);
      if (backward) body.backward = true;
      return request("list_timeline", body, { signal }).then(normalizeTimelineResponse);
    },
    subscribeTimeline: ({ sessionID, recoverQueueID, signal, onQueue, onEvent, onError }) => streamRequest("subscribe_timeline", {
      body: {
        session_id: String(sessionID),
        ...(recoverQueueID ? { recover_queue_id: recoverQueueID } : {}),
      },
      signal,
      onMessage: ({ payload }) => {
        const base = baseResponse(payload);
        if (statusCode(base) !== 0) {
          const error = new Error(statusMessage(base) || "stream error");
          error.retryStream = false;
          onError?.(error);
          throw error;
        }
        if (payload.queue_id) {
          onQueue?.(payload.queue_id);
          return;
        }
        const event = parseTimelineEvent(payload.event || payload.Event);
        if (event) onEvent?.(event);
      },
    }),
  };
}

export function createTimelineChannel(config) {
  const schedule = config.schedule || ((callback, delay) => setTimeout(callback, delay));
  const cancelSchedule = config.cancelSchedule || clearTimeout;
  let active = null;

  function follow(taskID, threadID = "") {
    close();
    if (!taskID) return false;
    active = {
      taskID: String(taskID),
      threadID: String(threadID || ""),
      token: Symbol(taskID),
      controller: new AbortController(),
      timer: null,
      retryDelay: 1200,
      cursor: config.loadCursor?.(String(taskID)) || "",
    };
    open(active);
    return true;
  }

  function close() {
    if (!active) return;
    active.controller.abort();
    if (active.timer !== null) cancelSchedule(active.timer);
    active = null;
  }

  async function open(connection) {
    const recoverQueueID = config.loadQueue(connection.taskID) || "";
    try {
      await config.api.subscribeTimeline({
        sessionID: connection.taskID,
        recoverQueueID,
        signal: connection.controller.signal,
        onQueue: (queueID) => {
          if (!isActive(connection)) return;
          connection.retryDelay = 1200;
          if (queueID) config.saveQueue(connection.taskID, queueID);
          config.onStatus?.(connection.taskID, "connected");
        },
        onEvent: (event) => {
          if (!isActive(connection)) return;
          config.onEvents(connection.taskID, [event]);
        },
        onError: (error) => {
          if (isActive(connection)) config.onError?.(connection.taskID, error);
        },
      });
      if (isActive(connection)) retry(connection);
    } catch (error) {
      if (!isActive(connection) || connection.controller.signal.aborted) return;
      if (error?.retryStream === false) {
        config.onStatus?.(connection.taskID, "polling");
        await poll(connection);
        return;
      }
      config.onError?.(connection.taskID, error);
      retry(connection);
    }
  }

  function retry(connection) {
    if (!isActive(connection) || connection.timer !== null) return;
    const delay = connection.retryDelay;
    connection.retryDelay = Math.min(Math.round(delay * 1.7), 10000);
    connection.timer = schedule(() => {
      connection.timer = null;
      if (!isActive(connection)) return;
      connection.controller = new AbortController();
      open(connection);
    }, delay);
  }

  async function poll(connection) {
    try {
      const payload = await config.api.listTimeline({
        sessionID: connection.taskID,
        threadID: connection.threadID,
        cursor: connection.cursor,
        limit: 200,
        signal: connection.controller.signal,
      });
      if (!isActive(connection)) return;
      if (payload.events?.length) config.onEvents(connection.taskID, payload.events);
      if (payload.page_info?.next_cursor) {
        connection.cursor = String(payload.page_info.next_cursor);
        config.saveCursor?.(connection.taskID, connection.cursor);
      }
      connection.timer = schedule(() => {
        connection.timer = null;
        if (isActive(connection)) poll(connection);
      }, config.pollDelay || 1500);
    } catch (error) {
      if (!isActive(connection) || connection.controller.signal.aborted) return;
      config.onError?.(connection.taskID, error);
      connection.timer = schedule(() => {
        connection.timer = null;
        if (isActive(connection)) poll(connection);
      }, config.pollDelay || 1500);
    }
  }

  function isActive(connection) {
    return active?.token === connection.token;
  }

  return { follow, close };
}

export function normalizeTimelineResponse(payload) {
  return {
    ...payload,
    events: (payload?.events || payload?.Events || []).map(parseTimelineEvent).filter(Boolean),
  };
}

export function parseTimelineEvent(item) {
  if (!item) return null;
  if (item.event_json) return parseLegacyEvent(item.event_json);
  const payload = normalizePayload(item.payload);
  const eventType = field(item.event_type, "");
  return {
    ...shapePayload(eventType, payload),
    event_id: field(item.event_id, ""),
    event_type: eventType,
    session_id: field(item.session_id, ""),
    thread_id: field(item.thread_id, ""),
    turn_id: field(item.turn_id, ""),
    created_at_ms: Number(item.created_at_ms || 0),
  };
}

function shapePayload(type, payload) {
  switch (type) {
    case "TURN_STARTED":
    case "ASSISTANT_MESSAGE":
      return { message: payload, context_usage: payload.context_usage, consumed_message_ids: payload.consumed_message_ids || [] };
    case "ASSISTANT_DELTA":
      return { assistant_delta: payload };
    case "TOOL_CALL_STARTED":
    case "TOOL_CALL_OUTPUT_DELTA":
    case "TOOL_CALL_FINISHED":
      return { tool_call: payload, context_usage: payload.context_usage };
    case "APPROVAL_REQUIRED":
      return { approval: payload, consumed_message_ids: payload.consumed_message_ids || [] };
    case "INTERRUPT_REQUIRED":
      return { interrupt_required: payload, consumed_message_ids: payload.consumed_message_ids || [] };
    case "PLAN_UPDATED":
      return { plan_updated: payload, context_usage: payload.context_usage };
    case "PLAN_INPUT_REQUIRED":
      return { plan_input_required: payload, consumed_message_ids: payload.consumed_message_ids || [] };
    case "CONTEXT_COMPACTED":
      return { context_compacted: payload, context_usage: payload.context_usage };
    case "ERROR":
    case "TURN_INTERRUPTED":
      return { error: payload, context_usage: payload.context_usage, consumed_message_ids: payload.consumed_message_ids || [] };
    case "TURN_FINISHED":
      return { turn_finished: payload, context_usage: payload.context_usage, consumed_message_ids: payload.consumed_message_ids || [] };
    case "COMPACT_INTERRUPTED":
      return { compact_interrupted: payload };
    default:
      return { payload };
  }
}

function normalizePayload(payload) {
  if (!payload) return {};
  if (typeof payload === "object" && !Array.isArray(payload)) return payload;
  if (typeof payload === "string") {
    try {
      const value = JSON.parse(payload);
      if (value && typeof value === "object" && !Array.isArray(value)) return value;
    } catch (error) {
      return payloadError(error);
    }
  }
  return payloadError(new Error(`Unsupported timeline payload: ${String(payload)}`));
}

function parseLegacyEvent(value) {
  try {
    return JSON.parse(value);
  } catch (error) {
    return payloadError(error);
  }
}

function payloadError(error) {
  return { event_type: "ERROR", error: { message: "Failed to parse timeline event.", detail: String(error?.message || error) } };
}

function field(value, fallback) {
  return value === undefined || value === null || value === "" ? fallback : String(value);
}

function baseResponse(payload) {
  return payload?.BaseResp || payload?.base_resp || {};
}

function statusCode(base) {
  return Number(base.StatusCode || base.status_code || 0);
}

function statusMessage(base) {
  return base.StatusMessage || base.status_message || "";
}
