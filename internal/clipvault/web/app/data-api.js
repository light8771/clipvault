import { app } from './app-context.js';
import { armConfirm } from './confirm-arm.js';

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
      el('dapiStatusText').textContent = '读取失败';
    }
  };

  function renderStatus(d) {
    const enabled = !!d.enabled;
    el('dapiEnabled').checked = enabled;
    const statusText = el('dapiStatusText');
    if (!enabled) {
      statusText.textContent = '未启用';
    } else if (d.running) {
      statusText.textContent = '运行中';
    } else {
      statusText.textContent = d.bindError ? '启动失败：' + d.bindError : '已停止';
    }
    el('dapiStatusHint').textContent = d.activated
      ? '授权已激活，数据接口可用'
      : '未激活：数据接口仅对已激活用户开放';
    el('dapiUrlRow').classList.toggle('hidden', !(enabled && d.running && d.url));
    el('dapiUrlText').value = d.url || '';
    el('dapiSensitiveDefault').checked = !!d.sensitiveDefault;
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
      app.toast('请填写指定监听地址', 'err');
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
          listenAddr,
          sensitiveDefault: el('dapiSensitiveDefault').checked
        })
      });
      await app.requireOK(res, '保存失败');
      const d = await res.json();
      app.dapiStatus = { ...(app.dapiStatus || {}), ...d };
      renderStatus(app.dapiStatus);
      if (d.runtimeError) {
        app.toast('设置已保存，但接口启动失败：' + d.runtimeError, 'err');
      } else {
        app.toast(enabled ? '数据接口已启用' : '数据接口已关闭');
      }
    } catch (e) {
      app.toast('保存失败：' + (e.message || e), 'err');
    }
  };

  app.dataApiCopyURL = async function dataApiCopyURL() {
    const url = el('dapiUrlText').value;
    if (!url) return;
    await copyText(url, '访问地址已复制');
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
    await copyText(target.textContent, '已复制');
    const label = button.textContent;
    button.textContent = '已复制';
    setTimeout(() => { button.textContent = label; }, 1200);
  };

  async function copyText(text, message) {
    try {
      await navigator.clipboard.writeText(text);
      app.toast(message);
    } catch (_) {
      app.toast('复制失败，请手动复制', 'err');
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
      list.innerHTML = '<p class="dapi-empty">无法读取令牌列表，请查看本地日志</p>';
    }
  };

  function renderTokens(tokens) {
    const list = el('dapiTokenList');
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
          '<button type="button" class="dapi-btn' + (token.allowSensitive ? '' : ' primary') + '" data-action="data-api-token-sensitive" data-id="' + token.id + '" data-allow="' + (token.allowSensitive ? 'false' : 'true') + '">' + (token.allowSensitive ? '禁止敏感数据' : '允许敏感数据') + '</button>' +
          '<button type="button" class="dapi-btn" data-action="data-api-token-regenerate" data-id="' + token.id + '">重新生成</button>' +
          '<button type="button" class="dapi-btn danger" data-action="data-api-token-revoke" data-id="' + token.id + '">撤销</button>' +
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
      app.toast('请填写令牌名称', 'err');
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
      await app.requireOK(res, '创建失败');
      const d = await res.json();
      el('dapiTokenName').value = '';
      showNewToken(d.token);
      app.toast('Token 已创建');
      await app.loadDataAPITokens();
    } catch (e) {
      app.toast('创建失败：' + (e.message || e), 'err');
    }
  };

  app.dataApiTokenCopy = async function dataApiTokenCopy() {
    if (!app.dapiNewToken) return;
    await copyText(app.dapiNewToken, 'Token 已复制');
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
      await app.requireOK(res, '修改失败');
      app.toast(target ? '该 Token 现可读取敏感数据' : '该 Token 已限制为仅普通数据');
      await app.loadDataAPITokens();
    };
    if (target) {
      armConfirm(button, '再次点击确认允许敏感数据', apply);
    } else {
      apply().catch(e => app.toast('修改失败：' + (e.message || e), 'err'));
    }
  };

  // 撤销是破坏性操作：第一次点击进入确认态，3 秒内再次点击才执行。
  app.dataApiTokenRevoke = function dataApiTokenRevoke(button) {
    armConfirm(button, '再次点击确认撤销', async () => {
      const res = await app.protectedFetch('/api/v1/data-api/tokens/revoke', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id })
      });
      await app.requireOK(res, '撤销失败');
      app.toast('Token 已撤销');
      await app.loadDataAPITokens();
    });
  };

  app.dataApiTokenRegenerate = function dataApiTokenRegenerate(button) {
    armConfirm(button, '再次点击确认重新生成', async () => {
      const res = await app.protectedFetch('/api/v1/data-api/tokens/regenerate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: button.dataset.id })
      });
      await app.requireOK(res, '重新生成失败');
      const d = await res.json();
      showNewToken(d.token);
      app.toast('已签发新 Token，旧 Token 已撤销');
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
