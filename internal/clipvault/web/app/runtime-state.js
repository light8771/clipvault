import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

export function registerRuntimeState() {
  app.DOT = document.getElementById('dot');
  app.ST = document.getElementById('st');
  app.TOG = document.getElementById('tog');
  app.TOGTX = document.getElementById('togTx');
  app.refreshState = async function refreshState() {
    try {
      const state = await app.fetch('/api/v1/runtime').then(response => response.json());
      if (app.DOT) app.DOT.className = 'dot' + (state.paused ? ' paused' : '');
      if (app.ST) app.ST.textContent = state.paused ? t('runtime.paused') : t('runtime.recording');
      if (app.TOG) {
        app.TOG.className = 'btn-toggle' + (state.paused ? ' off' : '');
        app.TOG.disabled = false;
      }
      if (app.TOGTX) app.TOGTX.textContent = state.paused ? t('nav.resume') : t('nav.pause');
      const icon = document.getElementById('togIcon');
      if (icon) {
        icon.innerHTML = state.paused
          ? '<polygon points="5 3 19 12 5 21 5 3"/>'
          : '<circle cx="12" cy="12" r="10"/><line x1="10" y1="15" x2="10" y2="9"/><line x1="14" y1="15" x2="14" y2="9"/>';
      }
    } catch (_) {}
  };
}
