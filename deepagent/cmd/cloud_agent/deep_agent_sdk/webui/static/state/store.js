export function createStore(reduce, initial) {
  let state = initial;
  const listeners = new Set();

  return {
    getState() {
      return state;
    },
    dispatch(action) {
      const next = reduce(state, action);
      if (next === state) return;
      state = next;
      for (const listener of listeners) listener(state, action);
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}
