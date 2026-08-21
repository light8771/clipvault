import { app } from './app-context.js';
import { armConfirm } from './confirm-arm.js';

const t = window.clipvaultI18n.t;

// 数据接口「服务配置 / 接口文档 / 访问令牌」子特性；
// 「访问记录」在 data-api-audit.js（按模块行数上限拆分）。

export function registerDataAPI() {
  app.dapiStatus = null;
  app.dapiNewToken = '';
  app.dapiTokens = [];

  const el = id => document.getElementById(id);
  const onPage = () => !!el('dapiPage');

  /* ── 服务状态与配置 ─────────────────────────────── */

  app.loadDataAPIStatus = async function loadDataAPIStatus() {
    if (!onPage()) return;
    try {
      const d = await app.fetch('/api/v1/data-api').then(r => r.json());
      app.dapiStatus = d;
      renderStatus(d);
    } catch (_) {
      el('dapiStatusText').textContent = t('logs.load_failed');
    }
  };

  function renderStatus(d) {
    const enabled = !!d.enabled;
    el('dapiEnabled').checked = enabled;
    const statusText = el('dapiStatusText');
    if (!enabled) {
      statusText.textContent = t('channel.status_disabled');
    } else if (d.running) {
      statusText.textContent = t('channel.status_running');
    } else {
      statusText.textContent = d.bindError ? t('channel.status_bind_failed', { error: d.bindError }) : t('channel.status_stopped');
    }
    el('dapiStatusHint').textContent = d.activated
      ? t('channel.status_activated')
      : t('channel.status_not_activated');
    el('dapiUrlRow').classList.toggle('hidden', !(enabled && d.running && d.url));
    el('dapiUrlText').value = d.url || '';
    applyListenAddrToForm(d.listenAddr || '127.0.0.1:17839');
    updateLanWarning();
  }

  function applyListenAddrToForm(addr) {
    const idx = addr.lastIndexOf(':');
    const host = idx > 0 ? addr.slice(0, idx) : '127.0.0.1';
    const port = idx > 0 ? addr.slice(idx + 1) : '17839';
    el('dapiPort').value = port;
    const scope = host === '127.0.0.1' || host === 'localhost' ? 'local'
      : (host === '0.0.0.0' || host === '::') ? 'lan' : 'custom';
    const radio = document.querySelector('input[name="dapiScope"][value="' + scope + '"]');
    if (radio) radio.checked = true;
    el('dapiCustomAddrRow').classList.toggle('hidden', scope !== 'custom');
    if (scope === 'custom') el('dapiCustomAddr').value = host;
  }

  function collectListenAddr() {
    const scope = document.querySelector('input[name="dapiScope"]:checked')?.value || 'local';
    const port = (el('dapiPort').value || '').trim() || '17839';
    let host = '127.0.0.1';
    if (scope === 'lan') host = '0.0.0.0';
    if (scope === 'custom') host = (el('dapiCustomAddr').value || '').trim();
    if (!host) {
      app.toast(t('channel.need_custom_addr'), 'err');
      return null;
    }
    return host + ':' + port;
  }

  function updateLanWarning() {
    const scope = document.querySelector('input[name="dapiScope"]:checked')?.value || 'local';
    el('dapiLanWarning').classList.toggle('hidden', scope === 'local');
    el('dapiCustomAddrRow').classList.toggle('hidden', scope !== 'custom');
  }

  app.dataApiSave = async function dataApiSave() {
    const listenAddr = collectListenAddr();
    if (!listenAddr) return;
    const enabled = el('dapiEnabled').checked;
    try {
      const res = await app.protectedFetch('/api/v1/data-api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          enabled,
          listenAddr
        })
      });
      await app.requireOK(res, t('common.save_failed'));
      const d = await res.json();
      app.dapiStatus = { ...(app.dapiStatus || {}), ...d };
      renderStatus(app.dapiStatus);
      if (d.runtimeError) {
        app.toast(t('channel.save_bind_failed', { error: d.runtimeError }), 'err');
      } else {
        app.toast(enabled ? t('dapi.enabled_toast') : t('dapi.disabled_toast'));
      }
    } catch (e) {
      app.toast(t('config.save_failed_with', { message: e.message || e }), 'err');
    }
  };

  app.dataApiCopyURL = async function dataApiCopyURL() {
    const url = el('dapiUrlText').value;
    if (!url) return;
    await copyText(url, t('dapi.url_copied'));
  };

  /* ── 接口文档弹窗 ─────────────────────────────────── */

  // 文档里的 base URL 不写死：打开时按当前实际监听地址替换 {BASE_URL}。
  // 模板缓存在元素 dataset.tpl 上，保证反复打开幂等。
  function dataAPIBaseURL() {
    const status = app.dapiStatus || {};
    if (status.url) return status.url.replace(/\/+$/, '');
    const addr = status.listenAddr || '';
    const idx = addr.lastIndexOf(':');
    if (idx > 0) {
      const host = addr.slice(0, idx);
      const shown = (host === '0.0.0.0' || host === '::') ? '127.0.0.1' : host;
      return 'http://' + shown + addr.slice(idx);
    }
    return 'http://127.0.0.1:17839';
  }

  app.openDataAPIDoc = function openDataAPIDoc() {
    const modal = el('dapiDocModal');
    if (!modal) return;
    const base = dataAPIBaseURL();
    modal.querySelectorAll('pre, code').forEach(node => {
      const tpl = node.dataset.tpl !== undefined ? node.dataset.tpl : node.textContent;
      if (!tpl.includes('{BASE_URL}')) return;
      node.dataset.tpl = tpl;
      node.textContent = tpl.split('{BASE_URL}').join(base);
    });
    modal.classList.remove('hidden');
  };

  app.dataApiDocNav = function dataApiDocNav(button) {
    const target = button.dataset.target ? document.getElementById(button.dataset.target) : null;
    if (target) target.scrollIntoView({ behavior: app.scrollBehavior(), block: 'start' });
  };

  app.closeDataAPIDoc = function closeDataAPIDoc() {
    const modal = el('dapiDocModal');
    if (!modal || modal.classList.contains('hidden')) return;
    modal.classList.add('hidden');
  };

  // 复制文档里任意代码块：data-copy-target 指向元素 id。
  app.dataApiCopyDoc = async function dataApiCopyDoc(button) {
    const target = button.dataset.copyTarget ? document.getElementById(button.dataset.copyTarget) : null;
    if (!target) return;
    await copyText(target.textContent, t('common.copied'));
    const label = button.textContent;
    button.textContent = t('common.copied');
    setTimeout(() => { button.textContent = label; }, 1200);
  };

  async function copyText(text, message) {
    try {
      await navigator.clipboard.writeText(text);
      app.toast(message);
    } catch (_) {
      app.toast(t('common.copy_manual'), 'err');
    }
  }

  /* ── 访问令牌 ───────────────────────────────────── */

  app.loadDataAPITokens = async function loadDataAPITokens() {
    if (!onPage()) return;
    const list = el('dapiTokenList');
    try {
      const d = await app.fetch('/api/v1/data-api/tokens').then(r => r.json());
      app.dapiTokens = d.tokens || [];
      renderTokens(app.dapiTokens);
      // 审计筛选栏的令牌下拉随之刷新（data-api-audit.js 提供）。
      if (app.renderDataAPITokenFilter) app.renderDataAPITokenFilter();
    } catch (_) {
      list.innerHTML = '<p class="dapi-empty">' + t('channel.tokens_load_failed') + '</p>';
    }
  };

  function renderTokens(tokens) {
    const list = el('dapiTokenList');
    if (!tokens.length) {
      list.innerHTML = '<p class="dapi-empty">' + t('channel.tokens_empty') + '</p>';
      return;
    }
    list.innerHTML = tokens.map(token => {
      const stateLabel = t('channel.token_state_' + token.state);
      const expiry = token.expiresAt ? t('channel.token_valid_until', { time: app.formatLogTime(token.expiresAt) }) : t('mcp.expiry_never');
      const lastUsed = token.lastUsedAt ? t('channel.token_last_used', { time: app.formatLogTime(token.lastUsedAt) }) : t('channel.token_never_used');
      const sensitive = token.allowSensitive
        ? '<span class="dapi-tag warn" title="' + t('channel.sensitive_on_tip') + '">' + t('channel.sensitive_on') + '</span>'
        : '<span class="dapi-tag" title="' + t('channel.sensitive_off_tip') + '">' + t('channel.sensitive_off') + '</span>';
      const inactive = token.state !== 'active';
      const actions = !inactive
        ? '<div class="dapi-actions">' +
          '<button type="button" class="dapi-btn' + (token.allowSensitive ? '' : ' primary') + '" data-action="data-api-token-sensitive" data-id="' + token.id + '" data-allow="' + (token.allowSensitive ? 'false' : 'true') + '">' + (token.allowSensitive ? t('channel.sensitive_disable') : t('channel.sensitive_enable')) + '</button>' +
          '<button type="button" class="dapi-btn" data-action="data-api-token-regenerate" data-id="' + token.id + '">' + t('channel.regenerate') + '</button>' +
          '<button type="button" class="dapi-btn danger" data-action="data-api-token-revoke" data-id="' + token.id + '">' + t('channel.revoke') + '</button>' +
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
    app.dapiNewToken = token;
    el('dapiNewTokenValue').textContent = token;
    el('dapiNewTokenBox').classList.remove('hidden');
  }

  app.dataApiTokenCreate = async function dataApiTokenCreate() {
    const name = (el('dapiTokenName').value || '').trim();
    if (!name) {
      app.toast(t('channel.need_token_name'), 'err');
      return;
    }
    try {
      const res = await app.protectedFetch('/api/v1/data-api/tokens', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name,
          expiresIn: el('dapiTokenExpiry').value
        })
      });
      await app.requireOK(res, t('channel.create_failed'));
      const d = await res.json();
      el('dapiTokenName').value = '';
      showNewToken(d.token);
      app.toast(t('channel.token_created'));
      await app.loadDataAPITokens();
    } catch (e) {
      app.toast(t('channel.create_failed_with', { message: e.message || e }), 'err');
    }
  };

  app.dataApiTokenCopy = async function dataApiTokenCopy() {
    if (!app.dapiNewToken) return;
    await copyText(app.dapiNewToken, t('channel.token_copied'));
  };

  app.dataApiTokenDismiss = function dataApiTokenDismiss() {
    app.dapiNewToken = '';
    el('dapiNewTokenValue').textContent = '';
    el('dapiNewTokenBox').classList.add('hidden');
  };

  // 敏感数据授权：开启（扩大权限）需要二次确认，关闭直接执行。
  app.dataApiTokenSensitive = function dataApiTokenSensitive(button) {
    const target = button.dataset.allow === 'true';
    const apply = async () => {
      const res = await app.protectedFetch('/api/v1/data-api/tokens/sensitive', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id, allowSensitive: target })
      });
      await app.requireOK(res, t('channel.update_failed'));
      app.toast(target ? t('channel.sensitive_enabled_toast') : t('channel.sensitive_disabled_toast'));
      await app.loadDataAPITokens();
    };
    if (target) {
      armConfirm(button, t('channel.confirm_sensitive_on'), apply);
    } else {
      apply().catch(e => app.toast(t('channel.update_failed_with', { message: e.message || e }), 'err'));
    }
  };

  // 撤销是破坏性操作：第一次点击进入确认态，3 秒内再次点击才执行。
  app.dataApiTokenRevoke = function dataApiTokenRevoke(button) {
    armConfirm(button, t('channel.confirm_revoke'), async () => {
      const res = await app.protectedFetch('/api/v1/data-api/tokens/revoke', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id })
      });
      await app.requireOK(res, t('channel.revoke_failed'));
      app.toast(t('channel.token_revoked'));
      await app.loadDataAPITokens();
    });
  };

  app.dataApiTokenRegenerate = function dataApiTokenRegenerate(button) {
    armConfirm(button, t('channel.confirm_regenerate'), async () => {
      const res = await app.protectedFetch('/api/v1/data-api/tokens/regenerate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id })
      });
      await app.requireOK(res, t('channel.regenerate_failed'));
      const d = await res.json();
      showNewToken(d.token);
      app.toast(t('channel.token_regenerated'));
      await app.loadDataAPITokens();
    });
  };

  /* ── 页面初始化 ─────────────────────────────────── */

  if (onPage()) {
    document.querySelectorAll('input[name="dapiScope"]').forEach(radio => {
      radio.addEventListener('change', updateLanWarning);
    });
    document.addEventListener('keydown', e => {
      if (e.key === 'Escape') app.closeDataAPIDoc();
    });
    void app.loadDataAPIStatus();
    void app.loadDataAPITokens();
  }
}
