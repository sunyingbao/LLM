export function createSubmitGate() {
  const active = new Set();

  async function run(key, task) {
    if (active.has(key)) return false;
    active.add(key);
    try {
      await task();
      return true;
    } finally {
      active.delete(key);
    }
  }

  function busy(key) {
    return active.has(key);
  }

  return { run, busy };
}

export function bindTap(element, handler) {
  let lastTouchAt = 0;
  element.addEventListener("touchend", (event) => {
    event.preventDefault();
    lastTouchAt = Date.now();
    handler(event);
  }, { passive: false });
  element.addEventListener("click", (event) => {
    if (Date.now() - lastTouchAt < 800) return;
    handler(event);
  });
}
