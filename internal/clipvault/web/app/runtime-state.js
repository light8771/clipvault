import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

export function registerRuntimeState() {
  app.DOT = document.getElementById('dot');
  app.ST = document.getElementById('st');
  app.TOG = document.getElementById('tog');
  app.TOGTX = document.getElementById('togTx');
  let revision = 0;
  let busy = false;
  const readState = async response => {
    if (!response.ok) throw new Error('Runtime request failed');
    const state = await response.json();
    if (!state || typeof state.paused !== 'boolean') throw new Error('Invalid runtime state');
    return state;
  };
  const render = state => {
    if (app.DOT) app.DOT.className = 'dot' + (state.paused ? ' paused' : '');
    if (app.ST) app.ST.textContent = state.paused ? t('runtime.paused') : t('runtime.recording');
    if (app.TOG) {
      app.TOG.className = 'btn-toggle' + (state.paused ? ' off' : '');
      app.TOG.disabled = busy;
    }
    if (app.TOGTX) app.TOGTX.textContent = state.paused ? t('nav.resume') : t('nav.pause');
    const icon = document.getElementById('togIcon');
    if (icon) {
      icon.innerHTML = state.paused
        ? '<polygon points="5 3 19 12 5 21 5 3"/>'
        : '<circle cx="12" cy="12" r="10"/><line x1="10" y1="15" x2="10" y2="9"/><line x1="14" y1="15" x2="14" y2="9"/>';
    }
  };
  app.refreshState = async function refreshState() {
    if (busy) return false;
    const request = ++revision;
    try {
      const state = await readState(await app.fetch('/api/v1/runtime'));
      if (request !== revision) return false;
      render(state);
      return true;
    } catch (_) { return false; }
  };
  app.TOG?.addEventListener('click', async () => {
    if (busy) return;
    busy = true;
    revision++;
    app.TOG.disabled = true;
    try {
      const state = await readState(await app.fetch('/api/v1/runtime/pause', { method: 'POST' }));
      render(state);
    } catch (_) {
      app.toast(t('common.action_failed'), 'err');
    } finally {
      busy = false;
      app.TOG.disabled = false;
    }
  });
}
