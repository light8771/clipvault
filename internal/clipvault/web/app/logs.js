import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

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
      document.getElementById('oplogTotal').textContent = t('logs.load_failed');
      list.innerHTML = '<p class="log-empty">' + t('logs.oplog_read_failed') + '</p>';
    }
  };

  function renderConfigChangeLog(entries) {
    const list = document.getElementById('oplogList');
    const state = app.oplogState;
    document.getElementById('oplogTotal').textContent = t('logs.oplog_total', { count: state.total });
    if (!entries.length) {
      list.innerHTML = '<p class="log-empty">' + (state.page > 0 ? t('logs.page_empty') : t('logs.oplog_empty')) + '</p>';
    } else {
      list.innerHTML = entries.map(entry => {
        // 操作记录持久化的是词条 key（config_field.* / config_value.*），
        // 展示时翻译；历史遗留的明文（旧库或未登记路径的回退）原样展示。
        const changes = (entry.changes || []).map(change => {
          const label = change.label || change.path;
          const isFieldKey = label.startsWith('config_field.');
          const renderValue = value =>
            typeof value === 'string' && value.startsWith('config_value.') ? t(value) : value;
          return '<li class="log-change">' +
          '<span class="log-label">' + app.esc(isFieldKey ? t(label) : label) + '</span>' +
          '<span class="log-values">' +
          '<code class="log-old">' + app.esc(renderValue(change.old)) + '</code>' +
          '<span class="log-arrow">→</span>' +
          '<code class="log-new">' + app.esc(renderValue(change.new)) + '</code>' +
          '</span></li>';
        }).join('');
        return '<div class="log-entry">' +
          '<div class="log-entry-head">' +
          '<span class="log-time">' + app.esc(app.formatLogTime(entry.changedAt)) + '</span>' +
          '<span class="log-count">' + t('logs.change_count', { count: (entry.changes || []).length }) + '</span>' +
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
      await app.requireOK(await app.protectedFetch('/api/v1/logs/config-changes', { method: 'DELETE' }), t('logs.clear_failed'));
      app.toast(t('logs.oplog_cleared'));
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
      document.getElementById('shlogTotal').textContent = t('logs.load_failed');
      list.innerHTML = '<p class="log-empty">' + t('logs.shlog_read_failed') + '</p>';
    }
  };

  function renderSearchHistory(entries) {
    const list = document.getElementById('shlogList');
    const state = app.shlogState;
    document.getElementById('shlogTotal').textContent = t('logs.shlog_total', { count: state.total });
    if (!entries.length) {
      list.innerHTML = '<p class="log-empty">' + (state.page > 0 ? t('logs.page_empty') : t('logs.shlog_empty')) + '</p>';
    } else {
      list.innerHTML = entries.map(entry => {
        // filters 持久化为「词条 key → 词条 key 或原始值」，展示时翻译
        // （shlog.filter.* 为标签 key，值为词条 key 或普通文本如分组 ID）。
        const filters = Object.entries(entry.filters || {})
          .map(([key, value]) => {
            const label = key.startsWith('shlog.filter.') ? t(key) : key;
            const display = typeof value === 'string' && (value.indexOf('.') > 0) && !/^\d+$/.test(value)
              ? t(value)
              : value;
            return '<span class="log-chip">' + app.esc(label) + '：' + app.esc(display) + '</span>';
          })
          .join('');
        return '<div class="log-entry">' +
          '<div class="log-entry-head">' +
          '<span class="log-time">' + app.esc(app.formatLogTime(entry.searchedAt)) + '</span>' +
          '<span class="log-count">' + t('logs.result_count', { count: entry.resultCount }) + '</span>' +
          '</div>' +
          '<div class="log-search-main">' +
          '<a class="log-query" href="/?q=' + encodeURIComponent(entry.query) + '" title="' + t('logs.research') + '">' + app.esc(entry.query) + '</a>' +
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
      await app.requireOK(await app.protectedFetch('/api/v1/logs/searches', { method: 'DELETE' }), t('logs.clear_failed'));
      app.toast(t('logs.shlog_cleared'));
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
    document.getElementById(prefix + 'PageInfo').textContent = t('logs.page_of', { page: state.page + 1, pages: pages });
    document.getElementById(prefix + 'Prev').disabled = state.page <= 0;
    document.getElementById(prefix + 'Next').disabled = (state.page + 1) * LOG_PAGE_SIZE >= state.total;
  }

  // 清空是破坏性操作：第一次点击进入确认态，3 秒内再次点击才真正执行。
  function armClearButton(button, run) {
    if (button.dataset.armed === '1') {
      button.dataset.armed = '';
      button.textContent = button.dataset.label;
      void run().catch(err => app.toast(err && err.message ? err.message : t('logs.clear_failed'), 'err'));
      return;
    }
    button.dataset.armed = '1';
    button.dataset.label = button.textContent;
    button.textContent = t('logs.confirm_clear');
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
