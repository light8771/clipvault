import { app } from './app-context.js';
import { armConfirm } from './confirm-arm.js';

const t = window.clipvaultI18n.t;

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
      el('mcpStatusText').textContent = t('logs.load_failed');
    }
  };

  function renderStatus(d) {
    const enabled = !!d.enabled;
    el('mcpEnabled').checked = enabled;
    const statusText = el('mcpStatusText');
    if (!enabled) {
      statusText.textContent = t('channel.status_disabled');
    } else if (d.running) {
      statusText.textContent = t('channel.status_running');
    } else {
      statusText.textContent = d.bindError ? t('channel.status_bind_failed', { error: d.bindError }) : t('channel.status_stopped');
    }
    el('mcpStatusHint').textContent = d.activated
      ? t('mcp.status_activated')
      : t('mcp.status_not_activated');
    el('mcpUrlRow').classList.toggle('hidden', !(enabled && d.running && d.url));
    el('mcpUrlText').value = d.url || '';
    applyListenAddrToForm(d.listenAddr || '127.0.0.1:17840');
    renderCapabilities(d.capabilities);
    updateLanWarning();
  }

  function renderCapabilities(capabilities) {
    if (!capabilities) return;
    const tools = capabilities.tools || [];
    el('mcpToolList').textContent = tools.length
      ? tools.join(', ')
      : t('mcp.no_tools');
    const templates = capabilities.resourceTemplates || [];
    const statics = capabilities.staticResources || [];
    const resources = templates.concat(statics);
    el('mcpResourceList').textContent = resources.length
      ? resources.join(', ')
      : t('mcp.no_resources');
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
      app.toast(t('channel.need_custom_addr'), 'err');
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
          listenAddr
        })
      });
      await app.requireOK(res, t('common.save_failed'));
      const d = await res.json();
      app.mcpStatus = { ...(app.mcpStatus || {}), ...d };
      renderStatus(app.mcpStatus);
      if (d.runtimeError) {
        app.toast(t('channel.save_bind_failed', { error: d.runtimeError }), 'err');
      } else {
        app.toast(enabled ? t('mcp.enabled_toast') : t('mcp.disabled_toast'));
      }
    } catch (e) {
      app.toast(t('config.save_failed_with', { message: e.message || e }), 'err');
    }
  };

  app.mcpCopyURL = async function mcpCopyURL() {
    const url = el('mcpUrlText').value;
    if (!url) return;
    await copyText(url, t('mcp.url_copied'));
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
      mcpServers: { clipvault: { url: mcpBaseURL(), headers: { Authorization: 'Bearer ' + t('mcp.example_token') } } }
    }, null, 2), t('mcp.example_copied'));
  };

  app.mcpCopyConfig = async function mcpCopyConfig(button) {
    await copyText(el('mcpConfigExample').textContent, t('common.copied'));
    const label = button.textContent;
    button.textContent = t('common.copied');
    setTimeout(() => { button.textContent = label; }, 1200);
  };

  // 工具调用速览：内容为静态文案，直接取 pre 文本复制。
  app.mcpCopyCheatsheet = async function mcpCopyCheatsheet(button) {
    const pre = el('mcpCheatsheet');
    if (!pre) return;
    await copyText(pre.textContent, t('common.copied'));
    const label = button.textContent;
    button.textContent = t('common.copied');
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
      list.innerHTML = '<p class="dapi-empty">' + app.esc(t('channel.tokens_load_failed')) + '</p>';
    }
  };

  function renderTokens(tokens) {
    const list = el('mcpTokenList');
    if (!tokens.length) {
      list.innerHTML = '<p class="dapi-empty">' + app.esc(t('channel.tokens_empty')) + '</p>';
      return;
    }
    list.innerHTML = tokens.map(token => {
      const stateLabel = t('channel.token_state_' + token.state);
      const expiry = token.expiresAt ? t('channel.token_valid_until', { time: app.formatLogTime(token.expiresAt) }) : t('mcp.expiry_never');
      const lastUsed = token.lastUsedAt ? t('channel.token_last_used', { time: app.formatLogTime(token.lastUsedAt) }) : t('channel.token_never_used');
      const sensitive = token.allowSensitive
        ? '<span class="dapi-tag warn" title="' + app.esc(t('channel.sensitive_on_tip')) + '">' + app.esc(t('channel.sensitive_on')) + '</span>'
        : '<span class="dapi-tag" title="' + app.esc(t('channel.sensitive_off_tip')) + '">' + app.esc(t('channel.sensitive_off')) + '</span>';
      const inactive = token.state !== 'active';
      const actions = !inactive
        ? '<div class="dapi-actions">' +
          '<button type="button" class="dapi-btn' + (token.allowSensitive ? '' : ' primary') + '" data-action="mcp-token-sensitive" data-id="' + token.id + '" data-allow="' + (token.allowSensitive ? 'false' : 'true') + '">' + (token.allowSensitive ? app.esc(t('channel.sensitive_disable')) : app.esc(t('channel.sensitive_enable'))) + '</button>' +
          '<button type="button" class="dapi-btn" data-action="mcp-token-regenerate" data-id="' + token.id + '">' + app.esc(t('channel.regenerate')) + '</button>' +
          '<button type="button" class="dapi-btn danger" data-action="mcp-token-revoke" data-id="' + token.id + '">' + app.esc(t('channel.revoke')) + '</button>' +
          '</div>'
        : '';
      return '<div class="dapi-card' + (inactive ? ' is-inactive' : '') + '">' +
        '<div class="dapi-card-head">' +
        '<span class="dapi-card-name">' + app.esc(token.name) + '</span>' +
        '<span class="dapi-badge ' + token.state + '">' + app.esc(stateLabel) + '</span>' +
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
      app.toast(t('channel.need_token_name'), 'err');
      return;
    }
    try {
      const res = await app.protectedFetch('/api/v1/mcp/tokens', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name,
          expiresIn: el('mcpTokenExpiry').value
        })
      });
      await app.requireOK(res, t('channel.create_failed'));
      const d = await res.json();
      el('mcpTokenName').value = '';
      showNewToken(d.token);
      app.toast(t('channel.token_created'));
      await app.loadMCPTokens();
    } catch (e) {
      app.toast(t('channel.create_failed_with', { message: e.message || e }), 'err');
    }
  };

  app.mcpTokenCopy = async function mcpTokenCopy() {
    if (!app.mcpNewToken) return;
    await copyText(app.mcpNewToken, t('channel.token_copied'));
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
      await app.requireOK(res, t('channel.update_failed'));
      app.toast(target ? t('channel.sensitive_enabled_toast') : t('channel.sensitive_disabled_toast'));
      await app.loadMCPTokens();
    };
    if (target) {
      armConfirm(button, t('channel.confirm_sensitive_on'), apply);
    } else {
      apply().catch(e => app.toast(t('channel.update_failed_with', { message: e.message || e }), 'err'));
    }
  };

  // 撤销是破坏性操作：第一次点击进入确认态，3 秒内再次点击才执行。
  app.mcpTokenRevoke = function mcpTokenRevoke(button) {
    armConfirm(button, t('channel.confirm_revoke'), async () => {
      const res = await app.protectedFetch('/api/v1/mcp/tokens/revoke', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id })
      });
      await app.requireOK(res, t('channel.revoke_failed'));
      app.toast(t('channel.token_revoked'));
      await app.loadMCPTokens();
    });
  };

  app.mcpTokenRegenerate = function mcpTokenRegenerate(button) {
    armConfirm(button, t('channel.confirm_regenerate'), async () => {
      const res = await app.protectedFetch('/api/v1/mcp/tokens/regenerate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id })
      });
      await app.requireOK(res, t('channel.regenerate_failed'));
      const d = await res.json();
      showNewToken(d.token);
      app.toast(t('channel.token_regenerated'));
      await app.loadMCPTokens();
    });
  };

  async function copyText(text, message) {
    try {
      await navigator.clipboard.writeText(text);
      app.toast(message);
    } catch (_) {
      app.toast(t('common.copy_manual'), 'err');
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
