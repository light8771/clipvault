import { app } from './app-context.js';
import { armConfirm } from './confirm-arm.js';

// 数据接口「访问记录」子特性：统计概览、筛选、表格详情、分页与导出。
// 服务配置/令牌/文档逻辑在 data-api.js；这里只关心审计列表。

const AUDIT_OP_LABELS = {
  query_items: '查询',
  get_item: '取单条',
  batch_get: '批量获取',
  get_content: '取内容',
  get_metadata: '元数据',
  auth: '认证失败'
};

export function registerDataAPIAudit() {
  const el = id => document.getElementById(id);
  const onPage = () => !!el('dapiAuditList');

  app.dapiAuditState = {
    page: 0,
    pageSize: 20,
    total: 0,
    expanded: new Set(), // 展开详情的记录 id
    filters: { operation: '', result: '', tokenId: '', q: '' }
  };

  function auditQueryString() {
    const state = app.dapiAuditState;
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

  // 把筛选控件的当前值读入状态（select 变更即筛选，关键词回车或点「筛选」提交）。
  function readFilterControls() {
    const f = app.dapiAuditState.filters;
    f.operation = el('dapiFilterOp').value;
    f.result = el('dapiFilterResult').value;
    f.tokenId = el('dapiFilterToken').value;
    f.q = (el('dapiFilterQuery').value || '').trim();
  }

  app.loadDataAPIAudit = async function loadDataAPIAudit() {
    if (!onPage()) return;
    const state = app.dapiAuditState;
    const list = el('dapiAuditList');
    try {
      const d = await app.fetch('/api/v1/data-api/audit?' + auditQueryString()).then(r => r.json());
      state.total = d.total || 0;
      if (d.stats) renderAuditStats(d.stats);
      renderAudit(d.entries || []);
    } catch (_) {
      el('dapiAuditTotal').textContent = '读取失败';
      list.innerHTML = '<tr><td colspan="7" class="dapi-empty">无法读取访问记录，请查看本地日志</td></tr>';
    }
  };

  function renderAuditStats(stats) {
    el('dapiStatTotal').textContent = stats.total;
    el('dapiStatSuccess').textContent = stats.success;
    el('dapiStatError').textContent = stats.error;
    el('dapiStatAvg').textContent = stats.avgDurationMs + 'ms';
    el('dapiStat24h').textContent = stats.last24h;
  }

  function auditResultBadge(entry) {
    const ok = entry.statusCode < 400;
    const cls = ok ? 'active' : (entry.statusCode < 500 ? 'expired' : 'revoked');
    const label = ok ? String(entry.statusCode) : entry.statusCode + (entry.errorCode ? ' · ' + entry.errorCode : '');
    return '<span class="dapi-badge ' + cls + '">' + app.esc(label) + '</span>';
  }

  function auditResultText(entry) {
    if (entry.operation === 'query_items' || entry.operation === 'batch_get' || entry.operation === 'get_metadata') {
      return entry.itemCount + ' 条';
    }
    if (entry.itemId) return '#' + entry.itemId;
    return '—';
  }

  function renderAudit(entries) {
    const state = app.dapiAuditState;
    const list = el('dapiAuditList');
    const filtering = state.filters.operation || state.filters.result || state.filters.tokenId || state.filters.q;
    el('dapiAuditTotal').textContent = filtering
      ? '筛出 ' + state.total + ' 条'
      : '共 ' + state.total + ' 条访问记录';
    if (!entries.length) {
      list.innerHTML = '<tr><td colspan="7" class="dapi-empty">' +
        (state.page > 0 ? '本页没有记录' : (filtering ? '没有符合条件的记录' : '暂无访问记录')) + '</td></tr>';
    } else {
      list.innerHTML = entries.map(auditRowHTML).join('');
    }
    renderAuditPager();
  }

  function auditRowHTML(entry) {
    const state = app.dapiAuditState;
    const opLabel = AUDIT_OP_LABELS[entry.operation] || entry.operation;
    const summary = entry.requestSummary || '';
    const summaryShort = summary.length > 64 ? summary.slice(0, 64) + '…' : summary;
    const hasBody = !!entry.requestBody;
    const sensitiveTag = entry.sensitiveAccessed
      ? ' <span class="dapi-tag warn" title="本次调用实际返回了敏感记录">敏感</span>'
      : '';
    // 展开集合以字符串 id 为键（dataset.auditId 是字符串，entry.id 是数字）。
    const expanded = state.expanded.has(String(entry.id));
    let row = '<tr class="dapi-audit-row' + (expanded ? ' is-open' : '') + '" data-audit-id="' + entry.id + '" data-action="data-api-audit-toggle">' +
      '<td class="col-time">' + app.esc(app.formatLogTime(entry.timestamp)) + '</td>' +
      '<td class="col-token">' + app.esc(entry.tokenName || '（未认证）') + '</td>' +
      '<td class="col-op"><span class="dapi-op-cell">' +
      '<span class="dapi-op-label">' + app.esc(opLabel) + '</span>' +
      '<span class="dapi-op-endpoint"><span class="dapi-method ' + app.esc(entry.method.toLowerCase()) + '">' + app.esc(entry.method) + '</span><code>' + app.esc(entry.endpoint) + '</code></span>' +
      '</span></td>' +
      '<td class="col-summary">' + (hasBody
        ? '<span class="dapi-summary dapi-summary-link" title="点击行查看完整请求体">' + app.esc(summaryShort || '点击查看请求体') + '</span>'
        : (summaryShort ? '<code class="dapi-summary">' + app.esc(summaryShort) + '</code>' : '<span class="dapi-muted">—</span>')) + sensitiveTag + '</td>' +
      '<td class="col-result"><span class="dapi-result-cell">' + auditResultBadge(entry) + '<span class="dapi-result-count">' + app.esc(auditResultText(entry)) + '</span></span></td>' +
      '<td class="col-ms">' + entry.durationMs + 'ms</td>' +
      '<td class="col-ip">' + app.esc(entry.remoteIP || '—') + '</td>' +
      '</tr>';
    if (expanded) {
      row += '<tr class="dapi-audit-detail"><td colspan="7">' +
        '<div class="dapi-detail-grid">' +
        '<span class="dapi-detail-label">请求</span><code>' + app.esc(entry.method + ' ' + entry.endpoint) + '</code>' +
        '<span class="dapi-detail-label">响应</span><code>' + app.esc(auditResultText(entry)) + ' · ' + entry.durationMs + 'ms</code>' +
        '<span class="dapi-detail-label">敏感访问</span><code>' + (entry.sensitiveAccessed ? '是' : '否') + '</code>' +
        (entry.itemId ? '<span class="dapi-detail-label">记录</span><code>#' + entry.itemId + '</code>' : '') +
        (entry.errorCode ? '<span class="dapi-detail-label">错误码</span><code>' + app.esc(entry.errorCode) + '</code>' : '') +
        (entry.tokenName ? '<span class="dapi-detail-label">令牌</span><code>' + app.esc(entry.tokenName) + '</code>' : '') +
        '</div>' +
        (hasBody ? auditBodyHTML(entry) : '') +
        '</td></tr>';
    }
    return row;
  }

  // 完整请求体块：能解析 JSON 就美化缩进展示；截断/非 JSON 时按原文展示。
  function auditBodyHTML(entry) {
    let text = entry.requestBody;
    try {
      text = JSON.stringify(JSON.parse(entry.requestBody), null, 2);
    } catch (_) { /* 保留原文（截断的 JSON 或非 JSON 内容） */ }
    return '<div class="dapi-detail-body">' +
      '<span class="dapi-detail-label">请求体</span>' +
      '<pre class="dapi-body-pre">' + app.esc(text) + '</pre>' +
      '</div>';
  }

  function renderAuditPager() {
    const state = app.dapiAuditState;
    const pager = el('dapiAuditPager');
    const size = state.pageSize;
    const pages = Math.max(1, Math.ceil(state.total / size));
    pager.classList.toggle('hidden', state.total <= size && state.page === 0);
    el('dapiAuditPrev').disabled = state.page <= 0;
    el('dapiAuditFirst').disabled = state.page <= 0;
    el('dapiAuditNext').disabled = state.page >= pages - 1;
    el('dapiAuditLast').disabled = state.page >= pages - 1;
    // 页码按钮：当前页附近最多 5 个，越界用省略号提示。
    const btns = el('dapiAuditPageBtns');
    const start = Math.max(0, Math.min(state.page - 2, pages - 5));
    const end = Math.min(pages, Math.max(5, start + 5));
    let html = '';
    if (start > 0) html += '<span class="dapi-page-ellipsis">…</span>';
    for (let p = start; p < end; p++) {
      const current = p === state.page;
      html += '<button type="button" class="log-page-btn dapi-page-num' + (current ? ' is-current' : '') + '"' +
        (current ? ' disabled' : '') + ' data-action="data-api-audit-page" data-page="' + p + '">' + (p + 1) + '</button>';
    }
    if (end < pages) html += '<span class="dapi-page-ellipsis">…</span>';
    btns.innerHTML = html;
  }

  // 展开状态存 id 集合，切换后重新拉取当前页数据（同时刷新统计，代价可接受）。
  app.toggleDataAPIAuditRow = function toggleDataAPIAuditRow(row) {
    const id = row.dataset.auditId;
    const expanded = app.dapiAuditState.expanded;
    if (expanded.has(id)) expanded.delete(id); else expanded.add(id);
    void app.loadDataAPIAudit();
  };

  app.turnDataAPIAuditPage = function turnDataAPIAuditPage(delta) {
    const state = app.dapiAuditState;
    const next = state.page + delta;
    if (next < 0 || next * state.pageSize >= state.total) return;
    state.page = next;
    void app.loadDataAPIAudit();
  };

  app.gotoDataAPIAuditPage = function gotoDataAPIAuditPage(page) {
    const state = app.dapiAuditState;
    const pages = Math.max(1, Math.ceil(state.total / state.pageSize));
    const next = Math.max(0, Math.min(pages - 1, page));
    if (next === state.page) return;
    state.page = next;
    void app.loadDataAPIAudit();
  };

  app.applyDataAPIAuditFilters = function applyDataAPIAuditFilters() {
    readFilterControls();
    app.dapiAuditState.page = 0;
    void app.loadDataAPIAudit();
  };

  app.resetDataAPIAuditFilters = function resetDataAPIAuditFilters() {
    el('dapiFilterOp').value = '';
    el('dapiFilterResult').value = '';
    el('dapiFilterToken').value = '';
    el('dapiFilterQuery').value = '';
    app.applyDataAPIAuditFilters();
  };

  app.refreshDataAPIAudit = function refreshDataAPIAudit() {
    void app.loadDataAPIAudit();
  };

  app.exportDataAPIAudit = async function exportDataAPIAudit(button) {
    const label = button.textContent;
    try {
      const res = await app.fetch('/api/v1/data-api/audit/export?' + auditQueryString());
      await app.requireOK(res, '导出失败');
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = 'clipvault-audit.csv';
      document.body.appendChild(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      button.textContent = '已导出';
      setTimeout(() => { button.textContent = label; }, 1200);
    } catch (e) {
      app.toast('导出失败：' + (e.message || e), 'err');
    }
  };

  app.clearDataAPIAudit = function clearDataAPIAudit(button) {
    armConfirm(button, '再次点击确认清空', async () => {
      await app.requireOK(await app.protectedFetch('/api/v1/data-api/audit', { method: 'DELETE' }), '清空失败');
      app.toast('访问记录已清空');
      app.dapiAuditState.page = 0;
      app.dapiAuditState.expanded.clear();
      await app.loadDataAPIAudit();
    });
  };

  // 令牌下拉选项随令牌列表刷新（按名称展示，值是 token id）。
  // 由 data-api.js 的 loadDataAPITokens 在拿到列表后调用。
  app.renderDataAPITokenFilter = function renderDataAPITokenFilter() {
    const select = el('dapiFilterToken');
    if (!select) return;
    const current = select.value;
    const tokens = app.dapiTokens || [];
    select.innerHTML = '<option value="">全部令牌</option>' +
      tokens.map(token =>
        '<option value="' + app.esc(token.id) + '">' + app.esc(token.name) + '</option>').join('');
    if (Array.from(select.options).some(option => option.value === current)) select.value = current;
  };

  if (onPage()) {
    // 筛选控件：下拉变更即筛选；关键词回车提交；每页条数变化回到第一页。
    ['dapiFilterOp', 'dapiFilterResult', 'dapiFilterToken'].forEach(id => {
      el(id).addEventListener('change', app.applyDataAPIAuditFilters);
    });
    el('dapiFilterQuery').addEventListener('keydown', event => {
      if (event.key === 'Enter') app.applyDataAPIAuditFilters();
    });
    el('dapiAuditPageSize').addEventListener('change', () => {
      app.dapiAuditState.pageSize = parseInt(el('dapiAuditPageSize').value, 10) || 20;
      app.dapiAuditState.page = 0;
      void app.loadDataAPIAudit();
    });
    void app.loadDataAPIAudit();
  }
}
