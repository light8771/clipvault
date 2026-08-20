import { app } from './app-context.js';
import { armConfirm } from './confirm-arm.js';

// MCP 设置页「服务配置 / 能力与接入 / 访问令牌」子特性；
// 「访问记录」在 mcp-audit.js（按模块行数上限拆分）。

export function registerMCP() {
  app.mcpStatus = null;
  app.mcpNewToken = '';
  app.mcpTokens = [];

  const el = id => document.getElementById(id);
  const onPage = () => !!el('mcpPage');

  /* ── 服务状态与配置 ─────────────────────────────── */

  app.loadMCPStatus = async function loadMCPStatus() {
    if (!onPage()) return;
    try {
      const d = await app.fetch('/api/v1/mcp').then(r => r.json());
      app.mcpStatus = d;
      renderStatus(d);
    } catch (_) {
      el('mcpStatusText').textContent = '读取失败';
    }
  };

  function renderStatus(d) {
    const enabled = !!d.enabled;
    el('mcpEnabled').checked = enabled;
    const statusText = el('mcpStatusText');
    if (!enabled) {
      statusText.textContent = '未启用';
    } else if (d.running) {
      statusText.textContent = '运行中';
    } else {
      statusText.textContent = d.bindError ? '启动失败：' + d.bindError : '已停止';
    }
    el('mcpStatusHint').textContent = d.activated
      ? '授权已激活，MCP 服务可用'
      : '未激活：MCP 仅对已激活用户开放';
    el('mcpUrlRow').classList.toggle('hidden', !(enabled && d.running && d.url));
    el('mcpUrlText').value = d.url || '';
    el('mcpSensitiveDefault').checked = !!d.sensitiveDefault;
    applyListenAddrToForm(d.listenAddr || '127.0.0.1:17840');
    renderCapabilities(d.capabilities);
    updateLanWarning();
  }

  function renderCapabilities(capabilities) {
    if (!capabilities) return;
    const tools = capabilities.tools || [];
    el('mcpToolList').textContent = tools.length
      ? tools.join('、')
      : '未注册任何工具';
    const templates = capabilities.resourceTemplates || [];
    const statics = capabilities.staticResources || [];
    const resources = templates.concat(statics);
    el('mcpResourceList').textContent = resources.length
      ? resources.join('、')
      : '未注册任何资源';
  }

  function applyListenAddrToForm(addr) {
    const idx = addr.lastIndexOf(':');
    const host = idx > 0 ? addr.slice(0, idx) : '127.0.0.1';
    const port = idx > 0 ? addr.slice(idx + 1) : '17840';
    el('mcpPort').value = port;
    const scope = host === '127.0.0.1' || host === 'localhost' ? 'local'
      : (host === '0.0.0.0' || host === '::') ? 'lan' : 'custom';
    const radio = document.querySelector('input[name="mcpScope"][value="' + scope + '"]');
    if (radio) radio.checked = true;
    el('mcpCustomAddrRow').classList.toggle('hidden', scope !== 'custom');
    if (scope === 'custom') el('mcpCustomAddr').value = host;
  }

  function collectListenAddr() {
    const scope = document.querySelector('input[name="mcpScope"]:checked')?.value || 'local';
    const port = (el('mcpPort').value || '').trim() || '17840';
    let host = '127.0.0.1';
    if (scope === 'lan') host = '0.0.0.0';
    if (scope === 'custom') host = (el('mcpCustomAddr').value || '').trim();
    if (!host) {
      app.toast('请填写指定监听地址', 'err');
      return null;
    }
    return host + ':' + port;
  }

  function updateLanWarning() {
    const scope = document.querySelector('input[name="mcpScope"]:checked')?.value || 'local';
    el('mcpLanWarning').classList.toggle('hidden', scope === 'local');
    el('mcpCustomAddrRow').classList.toggle('hidden', scope !== 'custom');
  }

  app.mcpSave = async function mcpSave() {
    const listenAddr = collectListenAddr();
    if (!listenAddr) return;
    const enabled = el('mcpEnabled').checked;
    try {
      const res = await app.protectedFetch('/api/v1/mcp/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          enabled,
          listenAddr,
          sensitiveDefault: el('mcpSensitiveDefault').checked
        })
      });
      await app.requireOK(res, '保存失败');
      const d = await res.json();
      app.mcpStatus = { ...(app.mcpStatus || {}), ...d };
      renderStatus(app.mcpStatus);
      if (d.runtimeError) {
        app.toast('设置已保存，但 MCP 启动失败：' + d.runtimeError, 'err');
      } else {
        app.toast(enabled ? 'MCP 服务已启用' : 'MCP 服务已关闭');
      }
    } catch (e) {
      app.toast('保存失败：' + (e.message || e), 'err');
    }
  };

  app.mcpCopyURL = async function mcpCopyURL() {
    const url = el('mcpUrlText').value;
    if (!url) return;
    await copyText(url, 'MCP 端点已复制');
  };

  /* ── 接入示例 ───────────────────────────────────── */

  function mcpBaseURL() {
    const status = app.mcpStatus || {};
    if (status.url) return status.url.replace(/\/+$/, '');
    const addr = status.listenAddr || '';
    const idx = addr.lastIndexOf(':');
    if (idx > 0) {
      const host = addr.slice(0, idx);
      const shown = (host === '0.0.0.0' || host === '::') ? '127.0.0.1' : host;
      return 'http://' + shown + addr.slice(idx) + '/mcp';
    }
    return 'http://127.0.0.1:17840/mcp';
  }

  function renderConfigExample() {
    const pre = el('mcpConfigExample');
    const tpl = pre.dataset.tpl !== undefined ? pre.dataset.tpl : pre.textContent;
    pre.dataset.tpl = tpl;
    pre.textContent = tpl.split('{MCP_URL}').join(mcpBaseURL());
  }

  app.mcpCopyExample = async function mcpCopyExample() {
    await copyText(JSON.stringify({
      mcpServers: { clipvault: { url: mcpBaseURL(), headers: { Authorization: 'Bearer cv_你的MCP令牌' } } }
    }, null, 2), '接入 JSON 已复制');
  };

  app.mcpCopyConfig = async function mcpCopyConfig(button) {
    await copyText(el('mcpConfigExample').textContent, '已复制');
    const label = button.textContent;
    button.textContent = '已复制';
    setTimeout(() => { button.textContent = label; }, 1200);
  };

  /* ── 访问令牌 ───────────────────────────────────── */

  app.loadMCPTokens = async function loadMCPTokens() {
    if (!onPage()) return;
    const list = el('mcpTokenList');
    try {
      const d = await app.fetch('/api/v1/mcp/tokens').then(r => r.json());
      app.mcpTokens = d.tokens || [];
      renderTokens(app.mcpTokens);
      // 审计筛选栏的令牌下拉随之刷新（mcp-audit.js 提供）。
      if (app.renderMCPTokenFilter) app.renderMCPTokenFilter();
    } catch (_) {
      list.innerHTML = '<p class="dapi-empty">无法读取令牌列表，请查看本地日志</p>';
    }
  };

  function renderTokens(tokens) {
    const list = el('mcpTokenList');
    if (!tokens.length) {
      list.innerHTML = '<p class="dapi-empty">暂无令牌，点击「生成 Token」创建</p>';
      return;
    }
    list.innerHTML = tokens.map(token => {
      const stateLabel = { active: '有效', expired: '已过期', revoked: '已撤销' }[token.state] || token.state;
      const expiry = token.expiresAt ? '有效至 ' + app.formatLogTime(token.expiresAt) : '永不过期';
      const lastUsed = token.lastUsedAt ? '最后访问 ' + app.formatLogTime(token.lastUsedAt) : '从未使用';
      const sensitive = token.allowSensitive
        ? '<span class="dapi-tag warn" title="此 Token 可读取敏感记录">可读敏感数据</span>'
        : '<span class="dapi-tag" title="敏感记录对此 Token 不可见">仅普通数据</span>';
      const inactive = token.state !== 'active';
      const actions = !inactive
        ? '<div class="dapi-actions">' +
          '<button type="button" class="dapi-btn' + (token.allowSensitive ? '' : ' primary') + '" data-action="mcp-token-sensitive" data-id="' + token.id + '" data-allow="' + (token.allowSensitive ? 'false' : 'true') + '">' + (token.allowSensitive ? '禁止敏感数据' : '允许敏感数据') + '</button>' +
          '<button type="button" class="dapi-btn" data-action="mcp-token-regenerate" data-id="' + token.id + '">重新生成</button>' +
          '<button type="button" class="dapi-btn danger" data-action="mcp-token-revoke" data-id="' + token.id + '">撤销</button>' +
          '</div>'
        : '';
      return '<div class="dapi-card' + (inactive ? ' is-inactive' : '') + '">' +
        '<div class="dapi-card-head">' +
        '<span class="dapi-card-name">' + app.esc(token.name) + '</span>' +
        '<span class="dapi-badge ' + token.state + '">' + stateLabel + '</span>' +
        '</div>' +
        '<div class="dapi-meta">' +
        '<span class="dapi-tag' + (token.state === 'expired' ? ' warn' : '') + '">' + app.esc(expiry) + '</span>' +
        '<span class="dapi-tag">' + app.esc(lastUsed) + '</span>' +
        sensitive +
        '</div>' +
        actions +
        '</div>';
    }).join('');
  }

  function showNewToken(token) {
    app.mcpNewToken = token;
    el('mcpNewTokenValue').textContent = token;
    el('mcpNewTokenBox').classList.remove('hidden');
  }

  app.mcpTokenCreate = async function mcpTokenCreate() {
    const name = (el('mcpTokenName').value || '').trim();
    if (!name) {
      app.toast('请填写令牌名称', 'err');
      return;
    }
    try {
      const res = await app.protectedFetch('/api/v1/mcp/tokens', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name,
          expiry: el('mcpTokenExpiry').value
        })
      });
      await app.requireOK(res, '创建失败');
      const d = await res.json();
      el('mcpTokenName').value = '';
      showNewToken(d.token);
      app.toast('Token 已创建');
      await app.loadMCPTokens();
    } catch (e) {
      app.toast('创建失败：' + (e.message || e), 'err');
    }
  };

  app.mcpTokenCopy = async function mcpTokenCopy() {
    if (!app.mcpNewToken) return;
    await copyText(app.mcpNewToken, 'Token 已复制');
  };

  app.mcpTokenDismiss = function mcpTokenDismiss() {
    app.mcpNewToken = '';
    el('mcpNewTokenValue').textContent = '';
    el('mcpNewTokenBox').classList.add('hidden');
  };

  // 敏感数据授权：开启（扩大权限）需要二次确认，关闭直接执行。
  app.mcpTokenSensitive = function mcpTokenSensitive(button) {
    const target = button.dataset.allow === 'true';
    const apply = async () => {
      const res = await app.protectedFetch('/api/v1/mcp/tokens/sensitive', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id, allowSensitive: target })
      });
      await app.requireOK(res, '修改失败');
      app.toast(target ? '该 Token 现可读取敏感数据' : '该 Token 已限制为仅普通数据');
      await app.loadMCPTokens();
    };
    if (target) {
      armConfirm(button, '再次点击确认允许敏感数据', apply);
    } else {
      apply().catch(e => app.toast('修改失败：' + (e.message || e), 'err'));
    }
  };

  // 撤销是破坏性操作：第一次点击进入确认态，3 秒内再次点击才执行。
  app.mcpTokenRevoke = function mcpTokenRevoke(button) {
    armConfirm(button, '再次点击确认撤销', async () => {
      const res = await app.protectedFetch('/api/v1/mcp/tokens/revoke', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id })
      });
      await app.requireOK(res, '撤销失败');
      app.toast('Token 已撤销');
      await app.loadMCPTokens();
    });
  };

  app.mcpTokenRegenerate = function mcpTokenRegenerate(button) {
    armConfirm(button, '再次点击确认重新生成', async () => {
      const res = await app.protectedFetch('/api/v1/mcp/tokens/regenerate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id })
      });
      await app.requireOK(res, '重新生成失败');
      const d = await res.json();
      showNewToken(d.token);
      app.toast('已签发新 Token，旧 Token 已撤销');
      await app.loadMCPTokens();
    });
  };

  async function copyText(text, message) {
    try {
      await navigator.clipboard.writeText(text);
      app.toast(message);
    } catch (_) {
      app.toast('复制失败，请手动复制', 'err');
    }
  }

  /* ── 页面初始化 ─────────────────────────────────── */

  if (onPage()) {
    document.querySelectorAll('input[name="mcpScope"]').forEach(radio => {
      radio.addEventListener('change', updateLanWarning);
    });
    void app.loadMCPStatus().then(renderConfigExample);
    void app.loadMCPTokens();
  }
}
