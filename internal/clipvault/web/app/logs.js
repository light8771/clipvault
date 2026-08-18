import { app } from './app-context.js';

const LOG_PAGE_SIZE = 20;

export function registerLogs() {
  app.oplogState = { page: 0, total: 0 };
  app.shlogState = { page: 0, total: 0 };

  app.formatLogTime = function formatLogTime(iso) {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    const pad = n => String(n).padStart(2, '0');
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
      ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  };

  /* ── 操作记录（系统设置修改历史） ─────────────────────── */

  app.loadConfigChangeLog = async function loadConfigChangeLog() {
    const list = document.getElementById('oplogList');
    if (!list) return;
    const state = app.oplogState;
    try {
      const d = await app.fetch('/api/v1/logs/config-changes?limit=' + LOG_PAGE_SIZE + '&offset=' + state.page * LOG_PAGE_SIZE).then(r => r.json());
      state.total = d.total || 0;
      renderConfigChangeLog(d.entries || []);
    } catch (_) {
      document.getElementById('oplogTotal').textContent = '读取失败';
      list.innerHTML = '<p class="log-empty">无法读取操作记录，请查看本地日志</p>';
    }
  };

  function renderConfigChangeLog(entries) {
    const list = document.getElementById('oplogList');
    const state = app.oplogState;
    document.getElementById('oplogTotal').textContent = '共 ' + state.total + ' 次修改';
    if (!entries.length) {
      list.innerHTML = '<p class="log-empty">' + (state.page > 0 ? '本页没有记录' : '暂无设置修改记录') + '</p>';
    } else {
      list.innerHTML = entries.map(entry => {
        const changes = (entry.changes || []).map(change =>
          '<li class="log-change">' +
          '<span class="log-label">' + app.esc(change.label || change.path) + '</span>' +
          '<span class="log-values">' +
          '<code class="log-old">' + app.esc(change.old) + '</code>' +
          '<span class="log-arrow">→</span>' +
          '<code class="log-new">' + app.esc(change.new) + '</code>' +
          '</span></li>'
        ).join('');
        return '<div class="log-entry">' +
          '<div class="log-entry-head">' +
          '<span class="log-time">' + app.esc(app.formatLogTime(entry.changedAt)) + '</span>' +
          '<span class="log-count">' + (entry.changes || []).length + ' 项修改</span>' +
          '</div>' +
          '<ul class="log-changes">' + changes + '</ul>' +
          '</div>';
      }).join('');
    }
    renderLogPager('oplog', state);
  }

  app.turnConfigChangeLogPage = function turnConfigChangeLogPage(delta) {
    const state = app.oplogState;
    const next = state.page + delta;
    if (next < 0 || next * LOG_PAGE_SIZE >= state.total) return;
    state.page = next;
    void app.loadConfigChangeLog();
  };

  app.clearConfigChangeLog = function clearConfigChangeLog(button) {
    armClearButton(button, async () => {
      await app.requireOK(await app.protectedFetch('/api/v1/logs/config-changes', { method: 'DELETE' }), '清空失败');
      app.toast('操作记录已清空');
      app.oplogState.page = 0;
      await app.loadConfigChangeLog();
    });
  };

  /* ── 搜索记录（剪贴板搜索历史） ─────────────────────── */

  app.loadSearchHistory = async function loadSearchHistory() {
    const list = document.getElementById('shlogList');
    if (!list) return;
    const state = app.shlogState;
    try {
      const d = await app.fetch('/api/v1/logs/searches?limit=' + LOG_PAGE_SIZE + '&offset=' + state.page * LOG_PAGE_SIZE).then(r => r.json());
      state.total = d.total || 0;
      renderSearchHistory(d.entries || []);
    } catch (_) {
      document.getElementById('shlogTotal').textContent = '读取失败';
      list.innerHTML = '<p class="log-empty">无法读取搜索记录，请查看本地日志</p>';
    }
  };

  function renderSearchHistory(entries) {
    const list = document.getElementById('shlogList');
    const state = app.shlogState;
    document.getElementById('shlogTotal').textContent = '共 ' + state.total + ' 条搜索记录';
    if (!entries.length) {
      list.innerHTML = '<p class="log-empty">' + (state.page > 0 ? '本页没有记录' : '暂无搜索记录') + '</p>';
    } else {
      list.innerHTML = entries.map(entry => {
        const filters = Object.entries(entry.filters || {})
          .map(([key, value]) => '<span class="log-chip">' + app.esc(key) + '：' + app.esc(value) + '</span>')
          .join('');
        return '<div class="log-entry">' +
          '<div class="log-entry-head">' +
          '<span class="log-time">' + app.esc(app.formatLogTime(entry.searchedAt)) + '</span>' +
          '<span class="log-count">' + entry.resultCount + ' 条结果</span>' +
          '</div>' +
          '<div class="log-search-main">' +
          '<a class="log-query" href="/?q=' + encodeURIComponent(entry.query) + '" title="在历史页重新搜索">' + app.esc(entry.query) + '</a>' +
          (filters ? '<div class="log-chips">' + filters + '</div>' : '') +
          '</div>' +
          '</div>';
      }).join('');
    }
    renderLogPager('shlog', state);
  }

  app.turnSearchHistoryPage = function turnSearchHistoryPage(delta) {
    const state = app.shlogState;
    const next = state.page + delta;
    if (next < 0 || next * LOG_PAGE_SIZE >= state.total) return;
    state.page = next;
    void app.loadSearchHistory();
  };

  app.clearSearchHistory = function clearSearchHistory(button) {
    armClearButton(button, async () => {
      await app.requireOK(await app.protectedFetch('/api/v1/logs/searches', { method: 'DELETE' }), '清空失败');
      app.toast('搜索记录已清空');
      app.shlogState.page = 0;
      await app.loadSearchHistory();
    });
  };

  /* ── 共用 ─────────────────────────────────────────── */

  function renderLogPager(prefix, state) {
    const pager = document.getElementById(prefix + 'Pager');
    if (!pager) return;
    const pages = Math.max(1, Math.ceil(state.total / LOG_PAGE_SIZE));
    pager.classList.toggle('hidden', state.total <= LOG_PAGE_SIZE);
    document.getElementById(prefix + 'PageInfo').textContent = '第 ' + (state.page + 1) + ' / ' + pages + ' 页';
    document.getElementById(prefix + 'Prev').disabled = state.page <= 0;
    document.getElementById(prefix + 'Next').disabled = (state.page + 1) * LOG_PAGE_SIZE >= state.total;
  }

  // 清空是破坏性操作：第一次点击进入确认态，3 秒内再次点击才真正执行。
  function armClearButton(button, run) {
    if (button.dataset.armed === '1') {
      button.dataset.armed = '';
      button.textContent = button.dataset.label;
      void run().catch(err => app.toast(err && err.message ? err.message : '清空失败', 'err'));
      return;
    }
    button.dataset.armed = '1';
    button.dataset.label = button.textContent;
    button.textContent = '再次点击确认清空';
    setTimeout(() => {
      if (button.dataset.armed === '1') {
        button.dataset.armed = '';
        button.textContent = button.dataset.label;
      }
    }, 3000);
  }

  if (document.getElementById('oplogList')) void app.loadConfigChangeLog();
  if (document.getElementById('shlogList')) void app.loadSearchHistory();
}
