import { app } from './app-context.js';

// Only the newest refresh may replace the list or report a loading failure.
export function createLanguageLoader(render) {
  let generation = 0;
  return async function loadLanguages() {
    const request = ++generation;
    try {
      const response = await app.fetch('/api/v1/i18n/languages');
      if (!response.ok) throw new Error((await app.readAPIError(response)).message);
      const data = await response.json();
      if (request !== generation) return false;
      render(data);
      return true;
    } catch (err) {
      if (request === generation) app.toast(err.message || window.clipvaultI18n.t('common.network_error'), 'err');
      return false;
    }
  };
}

export function createLanguageEntriesLoader(getState, isVisible, render) {
  let generation = 0;
  return async function loadEntries() {
    const request = ++generation;
    const state = { ...getState() };
    const params = new URLSearchParams({ lang: state.code, page: String(state.page), pageSize: String(state.pageSize) });
    for (const [key, value] of [['q', state.query], ['module', state.module], ['status', state.status]]) {
      if (value) params.set(key, value);
    }
    const current = () => request === generation && isVisible() && getState().code === state.code;
    try {
      const response = await app.fetch('/api/v1/i18n/entries?' + params);
      if (!response.ok) throw new Error((await app.readAPIError(response)).message);
      const data = await response.json();
      if (current()) render(data);
    } catch (err) {
      if (current()) app.toast(err.message || window.clipvaultI18n.t('common.network_error'), 'err');
    }
  };
}
