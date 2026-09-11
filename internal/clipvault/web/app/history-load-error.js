import { app } from './app-context.js';

export function showHistoryLoadError(error) {
  const message = window.clipvaultI18n.t('history.search_failed');
  app.LIST.innerHTML = '<div class="empty" role="alert">' + app.esc(message) + '</div>';
  app.RC.textContent = message;
  document.getElementById('pagination').classList.add('hidden');
  if (error?.code === 'secondary_cancelled') return;
  console.error('History search failed', error);
  app.toast(error instanceof app.APIError ? error.message : message, 'err');
}
