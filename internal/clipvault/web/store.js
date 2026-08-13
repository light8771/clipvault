export function createStore(initialState = {}) {
  let state = Object.freeze({ ...initialState });
  const listeners = new Set();

  return Object.freeze({
    getState() { return state; },
    update(update) {
      const patch = typeof update === 'function' ? update(state) : update;
      if (!patch || typeof patch !== 'object') return state;
      const next = Object.freeze({ ...state, ...patch });
      if (Object.keys(patch).every(key => Object.is(state[key], next[key]))) return state;
      state = next;
      for (const listener of [...listeners]) listener(state);
      return state;
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    }
  });
}

export const appStore = createStore({
  authenticated: false,
  secondaryAuthenticated: false,
  paused: false,
  view: 'history',
  loading: false
});
