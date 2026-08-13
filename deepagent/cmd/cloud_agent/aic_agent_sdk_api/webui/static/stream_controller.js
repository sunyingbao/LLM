export function createStreamController(cfg) {
  let abort = null;
  let reconnectTimer = null;
  let reconnectDelayMs = 1200;
  let generation = 0;
  let activeSessionID = "";

  function open(sessionID) {
    if (!sessionID) return false;
    close();
    activeSessionID = String(sessionID);
    const currentGeneration = ++generation;
    const currentAbort = new AbortController();
    abort = currentAbort;
    const recoverQueueID = cfg.loadQueue(activeSessionID);

    cfg.openStream({
      sessionID: activeSessionID,
      recoverQueueID,
      signal: currentAbort.signal,
      onQueue: (queueID) => {
        if (!isActive(activeSessionID, currentGeneration)) return;
        reconnectDelayMs = 1200;
        if (queueID) cfg.saveQueue(activeSessionID, queueID);
        cfg.onStatus(recoverQueueID ? "Recovered stream connection" : "");
      },
      onEvent: (event) => {
        if (!isActive(activeSessionID, currentGeneration)) return;
        cfg.onEvent(event);
      },
      onError: (error) => {
        if (!isActive(activeSessionID, currentGeneration)) return;
        cfg.onStatus(error.message || "Stream error", true);
      },
    }).then(() => {
      if (!isActive(activeSessionID, currentGeneration) || currentAbort.signal.aborted) return;
      if (abort === currentAbort) abort = null;
      cfg.onStatus("Stream disconnected. Reconnecting...", true);
      scheduleReconnect(activeSessionID, currentGeneration);
    }).catch((error) => {
      if (!isActive(activeSessionID, currentGeneration) || currentAbort.signal.aborted) return;
      if (abort === currentAbort) abort = null;
      if (error?.retryStream === false) {
        cfg.onStatus("Stream unavailable. Using polling fallback.", true);
        return;
      }
      cfg.onStatus("Stream disconnected. Reconnecting...", true);
      scheduleReconnect(activeSessionID, currentGeneration);
    });
    return true;
  }

  function close() {
    generation++;
    activeSessionID = "";
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (abort) {
      abort.abort();
      abort = null;
    }
  }

  function resetBackoff() {
    reconnectDelayMs = 1200;
  }

  function scheduleReconnect(sessionID, expectedGeneration) {
    if (reconnectTimer) return;
    const delay = reconnectDelayMs;
    reconnectDelayMs = Math.min(Math.round(delay * 1.7), 10000);
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      if (!isActive(sessionID, expectedGeneration)) return;
      open(sessionID);
    }, delay);
  }

  function isActive(sessionID, expectedGeneration) {
    return activeSessionID === String(sessionID) && generation === expectedGeneration;
  }

  return { open, close, resetBackoff };
}
