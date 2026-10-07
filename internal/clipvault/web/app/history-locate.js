import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

export function registerHistoryLocate() {
  let revision = 0;
  let controller;
  app.cancelLocate = () => {
    revision++;
    controller?.abort();
    if (app.locateActive) {
      app.searchRevision++;
      app.searchController?.abort();
    }
    app.locateActive = false;
    app.pendingLocateId = 0;
  };
  app.doLocate = async id => {
    app.cancelLocate();
    const request = revision;
    controller = new AbortController();
    app.locateActive = true;
    app.searchRevision++;
    app.searchController?.abort();
    clearTimeout(app.debounce);
    try {
      const response = await app.fetch('/api/v1/items/position?id=' + id, { signal: controller.signal });
      const data = await response.json();
      if (request !== revision) return;
      if (!response.ok || data.error) throw new Error(data.error || t('history.locate_failed'));
      if (!Number.isInteger(data.position) || data.position < 0) throw new Error(t('history.locate_failed'));
      app.resetSubtypeFilter?.(true);
      app.currentPage = Math.floor(data.position / (parseInt(app.PAGESIZE.value) || 50));
      app.Q.value = '';
      app.currentKind = '';
      app.currentGroupID = 0;
      app.currentSourceExes = [];
      app.filterDateFrom = '';
      app.filterDateTo = '';
      document.querySelectorAll('.filter-chip').forEach(chip => chip.classList.remove('active'));
      document.querySelector('.filter-chip[data-kind=""]').classList.add('active');
      for (const id of ['dateFrom', 'dateTo']) {
        const input = document.getElementById(id);
        if (input) input.value = '';
      }
      document.getElementById('clearDateBtn')?.classList.add('hidden');
      app.renderSrcExeDropdown();
      const loaded = await app.loadSourceExesForHistory('content');
      if (!loaded || request !== revision) return;
      app.pendingLocateId = id;
      await app.search({ locate: true });
    } catch (error) {
      if (request === revision && error?.name !== 'AbortError') {
        app.toast(error.message || t('history.locate_failed'), 'err');
      }
    } finally {
      if (request === revision) {
        app.locateActive = false;
        app.pendingLocateId = 0;
        controller = null;
      }
    }
  };
}
