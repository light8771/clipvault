import { app } from './app-context.js';
import { armConfirm } from './confirm-arm.js';

const t = window.clipvaultI18n.t;

// MCP 页「访问记录」子特性：统计概览、筛选、表格详情、分页与导出。
// 服务配置/令牌/接入示例逻辑在 mcp.js；这里只关心 MCP 通道审计。

export function registerMCPAudit() {
  const el = id => document.getElementById(id);
  const onPage = () => !!el('mcpAuditList');

  app.mcpAuditState = {
    page: 0,
    pageSize: 20,
    total: 0,
    expanded: new Set(),
    filters: { operation: '', result: '', tokenId: '', q: '' }
  };

  function auditQueryString() {
    const state = app.mcpAuditState;
    const f = state.filters;
    const params = new URLSearchParams();
    params.set('limit', state.pageSize);
    params.set('offset', state.page * state.pageSize);
    if (f.operation) params.set('operation', f.operation);
    if (f.result) params.set('result', f.result);
    if (f.tokenId) params.set('tokenId', f.tokenId);
    if (f.q) params.set('q', f.q);
    return params.toString();
  }

  function readFilterControls() {
    const f = app.mcpAuditState.filters;
    f.operation = el('mcpFilterOp').value;
    f.result = el('mcpFilterResult').value;
    f.tokenId = el('mcpFilterToken').value;
    f.q = (el('mcpFilterQuery').value || '').trim();
  }

  app.loadMCPAudit = async function loadMCPAudit() {
    if (!onPage()) return;
    const state = app.mcpAuditState;
    const list = el('mcpAuditList');
    try {
      const d = await app.fetch('/api/v1/mcp/audit?' + auditQueryString()).then(r => r.json());
      state.total = d.total || 0;
      if (d.stats) renderAuditStats(d.stats);
      renderAudit(d.entries || []);
    } catch (_) {
      el('mcpAuditTotal').textContent = t('logs.load_failed');
      list.innerHTML = '<tr><td colspan="7" class="dapi-empty">' + app.esc(t('audit.load_failed')) + '</td></tr>';
    }
  };

  function renderAuditStats(stats) {
    el('mcpStatTotal').textContent = stats.total;
    el('mcpStatSuccess').textContent = stats.success;
    el('mcpStatError').textContent = stats.error;
    el('mcpStatAvg').textContent = stats.avgDurationMs + 'ms';
    el('mcpStat24h').textContent = stats.last24h;
  }

  function auditResultBadge(entry) {
    const ok = entry.statusCode < 400;
    const cls = ok ? 'active' : (entry.statusCode < 500 ? 'expired' : 'revoked');
    const label = ok ? String(entry.statusCode) : entry.statusCode + (entry.errorCode ? ' · ' + entry.errorCode : '');
    return '<span class="dapi-badge ' + cls + '">' + app.esc(label) + '</span>';
  }

  function auditRowHTML(entry) {
    const state = app.mcpAuditState;
    const op = entry.operation || '';
    const resource = entry.resourceUri || '';
    const expanded = state.expanded.has(String(entry.id));
    const sensitive = entry.sensitiveAccessed
      ? '<span class="dapi-tag warn">' + app.esc(t('audit.sensitive_yes')) + '</span>'
      : '<span class="dapi-muted">—</span>';
    let row = '<tr class="dapi-audit-row' + (expanded ? ' is-open' : '') + '" data-audit-id="' + entry.id + '" data-action="mcp-audit-toggle">' +
      '<td class="col-time">' + app.esc(app.formatLogTime(entry.timestamp)) + '</td>' +
      '<td class="col-token">' + app.esc(entry.tokenName || t('audit.unauthenticated')) + '</td>' +
      '<td class="col-op"><span class="dapi-op-cell">' +
      '<span class="dapi-op-label">' + app.esc(op) + '</span>' +
      (resource ? '<span class="dapi-op-endpoint"><code>' + app.esc(resource) + '</code></span>' : '') +
      '</span></td>' +
      '<td class="col-summary">' + sensitive + '</td>' +
      '<td class="col-result"><span class="dapi-result-cell">' + auditResultBadge(entry) +
      '<span class="dapi-result-count">' + (entry.itemCount ? app.esc(t('audit.item_count', { count: entry.itemCount })) : (entry.itemId ? '#' + entry.itemId : '—')) + '</span></span></td>' +
      '<td class="col-ms">' + entry.durationMs + 'ms</td>' +
      '<td class="col-ip">' + app.esc(entry.remoteIP || '—') + '</td>' +
      '</tr>';
    if (expanded) {
      row += '<tr class="dapi-audit-detail"><td colspan="7">' +
        '<div class="dapi-detail-grid">' +
        '<span class="dapi-detail-label">' + app.esc(t('audit.detail_operation')) + '</span><code>' + app.esc(op) + '</code>' +
        (resource ? '<span class="dapi-detail-label">' + app.esc(t('audit.detail_resource')) + '</span><code>' + app.esc(resource) + '</code>' : '') +
        '<span class="dapi-detail-label">' + app.esc(t('audit.detail_returned')) + '</span><code>' + app.esc(t('audit.detail_result', { count: entry.itemCount || 0, duration: entry.durationMs })) + '</code>' +
        (entry.itemId ? '<span class="dapi-detail-label">' + app.esc(t('audit.detail_item')) + '</span><code>#' + entry.itemId + '</code>' : '') +
        (entry.errorCode ? '<span class="dapi-detail-label">' + app.esc(t('audit.detail_error_code')) + '</span><code>' + app.esc(entry.errorCode) + '</code>' : '') +
        '<span class="dapi-detail-label">' + app.esc(t('audit.detail_sensitive')) + '</span><code>' + (entry.sensitiveAccessed ? app.esc(t('audit.yes')) : app.esc(t('audit.no'))) + '</code>' +
        (entry.tokenName ? '<span class="dapi-detail-label">' + app.esc(t('audit.detail_token')) + '</span><code>' + app.esc(entry.tokenName) + '</code>' : '') +
        '</div>' +
        '</td></tr>';
    }
    return row;
  }

  function renderAudit(entries) {
    const state = app.mcpAuditState;
    const list = el('mcpAuditList');
    const filtering = state.filters.operation || state.filters.result || state.filters.tokenId || state.filters.q;
    el('mcpAuditTotal').textContent = filtering
      ? t('audit.total_filtered', { count: state.total })
      : t('audit.total', { count: state.total });
    if (!entries.length) {
      list.innerHTML = '<tr><td colspan="7" class="dapi-empty">' +
        (state.page > 0 ? app.esc(t('audit.empty_page')) : (filtering ? app.esc(t('audit.empty_filtered')) : app.esc(t('audit.empty')))) + '</td></tr>';
    } else {
      list.innerHTML = entries.map(auditRowHTML).join('');
    }
    renderAuditPager();
  }

  function renderAuditPager() {
    const state = app.mcpAuditState;
    const pager = el('mcpAuditPager');
    const size = state.pageSize;
    const pages = Math.max(1, Math.ceil(state.total / size));
    pager.classList.toggle('hidden', state.total <= size && state.page === 0);
    el('mcpAuditPrev').disabled = state.page <= 0;
    el('mcpAuditFirst').disabled = state.page <= 0;
    el('mcpAuditNext').disabled = state.page >= pages - 1;
    el('mcpAuditLast').disabled = state.page >= pages - 1;
    const btns = el('mcpAuditPageBtns');
    const start = Math.max(0, Math.min(state.page - 2, pages - 5));
    const end = Math.min(pages, Math.max(5, start + 5));
    let html = '';
    if (start > 0) html += '<span class="dapi-page-ellipsis">…</span>';
    for (let p = start; p < end; p++) {
      const current = p === state.page;
      html += '<button type="button" class="log-page-btn dapi-page-num' + (current ? ' is-current' : '') + '"' +
        (current ? ' disabled' : '') + ' data-action="mcp-audit-page" data-page="' + p + '">' + (p + 1) + '</button>';
    }
    if (end < pages) html += '<span class="dapi-page-ellipsis">…</span>';
    btns.innerHTML = html;
  }

  app.toggleMCPAuditRow = function toggleMCPAuditRow(row) {
    const id = row.dataset.auditId;
    const expanded = app.mcpAuditState.expanded;
    if (expanded.has(id)) expanded.delete(id); else expanded.add(id);
    void app.loadMCPAudit();
  };

  app.turnMCPAuditPage = function turnMCPAuditPage(delta) {
    const state = app.mcpAuditState;
    const next = state.page + delta;
    if (next < 0 || next * state.pageSize >= state.total) return;
    state.page = next;
    void app.loadMCPAudit();
  };

  app.gotoMCPAuditPage = function gotoMCPAuditPage(page) {
    const state = app.mcpAuditState;
    const pages = Math.max(1, Math.ceil(state.total / state.pageSize));
    const next = Math.max(0, Math.min(pages - 1, page));
    if (next === state.page) return;
    state.page = next;
    void app.loadMCPAudit();
  };

  app.applyMCPAuditFilters = function applyMCPAuditFilters() {
    readFilterControls();
    app.mcpAuditState.page = 0;
    void app.loadMCPAudit();
  };

  app.resetMCPAuditFilters = function resetMCPAuditFilters() {
    el('mcpFilterOp').value = '';
    el('mcpFilterResult').value = '';
    el('mcpFilterToken').value = '';
    el('mcpFilterQuery').value = '';
    app.applyMCPAuditFilters();
  };

  app.refreshMCPAudit = function refreshMCPAudit() {
    void app.loadMCPAudit();
  };

  app.exportMCPAudit = async function exportMCPAudit(button) {
    const label = button.textContent;
    try {
      const res = await app.fetch('/api/v1/mcp/audit/export?' + auditQueryString());
      await app.requireOK(res, t('audit.export_failed'));
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = 'clipvault-mcp-audit.csv';
      document.body.appendChild(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      button.textContent = t('audit.exported');
      setTimeout(() => { button.textContent = label; }, 1200);
    } catch (e) {
      app.toast(t('audit.export_failed_with', { message: e.message || e }), 'err');
    }
  };

  app.clearMCPAudit = function clearMCPAudit(button) {
    armConfirm(button, t('audit.confirm_clear'), async () => {
      await app.requireOK(await app.protectedFetch('/api/v1/mcp/audit', { method: 'DELETE' }), t('audit.clear_failed'));
      app.toast(t('mcp.audit_cleared'));
      app.mcpAuditState.page = 0;
      app.mcpAuditState.expanded.clear();
      await app.loadMCPAudit();
    });
  };

  // 令牌下拉选项随令牌列表刷新（按名称展示，值是 token id）。
  // 由 mcp.js 的 loadMCPTokens 在拿到列表后调用。
  app.renderMCPTokenFilter = function renderMCPTokenFilter() {
    const select = el('mcpFilterToken');
    if (!select) return;
    const current = select.value;
    const tokens = app.mcpTokens || [];
    select.innerHTML = '<option value="">' + app.esc(t('audit.all_tokens')) + '</option>' +
      tokens.map(token =>
        '<option value="' + app.esc(token.id) + '">' + app.esc(token.name) + '</option>').join('');
    if (Array.from(select.options).some(option => option.value === current)) select.value = current;
  };

  if (onPage()) {
    ['mcpFilterOp', 'mcpFilterResult', 'mcpFilterToken'].forEach(id => {
      el(id).addEventListener('change', app.applyMCPAuditFilters);
    });
    el('mcpFilterQuery').addEventListener('keydown', event => {
      if (event.key === 'Enter') app.applyMCPAuditFilters();
    });
    el('mcpAuditPageSize').addEventListener('change', () => {
      app.mcpAuditState.pageSize = parseInt(el('mcpAuditPageSize').value, 10) || 20;
      app.mcpAuditState.page = 0;
      void app.loadMCPAudit();
    });
    void app.loadMCPAudit();
  }
}
