import { app } from './app-context.js';
import { stopCompositeCards } from './composite-content.js';

export function clearHistoryContent() {
  app.cancelLocate?.();
  app.searchRevision = (app.searchRevision || 0) + 1;
  app.searchController?.abort();
  stopCompositeCards();
  if (app.currentModalId) app.closeModal();
  app.revokeFileObjectURLs();
  app.items.clear();
  app.filePaths.clear();
  app.pendingLocateId = 0;
  app.LIST.innerHTML = '';
  app.RC.textContent = '';
  document.getElementById('pagination').classList.add('hidden');
}

export function showHistoryLoadError(error) {
  clearHistoryContent();
  const message = window.clipvaultI18n.t('history.search_failed');
  app.LIST.innerHTML = '<div class="empty" role="alert">' + app.esc(message) + '</div>';
  app.RC.textContent = message;
  if (error?.code === 'secondary_cancelled') return;
  console.error('History search failed', error);
  app.toast(error instanceof app.APIError ? error.message : message, 'err');
}

export function showEmptyHistory() {
  stopCompositeCards();
  app.LIST.innerHTML = `<div class="empty">
        <svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2">
          <path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/>
          <rect x="9" y="3" width="6" height="4" rx="1"/>
          <line x1="9" y1="12" x2="15" y2="12"/>
          <line x1="9" y1="16" x2="13" y2="16"/>
        </svg>
        <span>${app.esc(window.clipvaultI18n.t('history.empty'))}</span>
      </div>`;
}
