import { app } from "./app-context.js";
import { createRemovableModal } from './modal-controller.js';
export function registerRecovery() {
  app.recoveryState = {
    hasRecovery: false,
    isolatedDatabases: [],
    pendingSwitchId: ''
  };
  app.loadRecoveryStatus = async function loadRecoveryStatus() {
    try {
      const res = await app.protectedFetch('/api/v1/recovery');
      if (!res.ok) return;
      const d = await res.json();
      app.recoveryState.hasRecovery = d.hasRecovery;
      app.recoveryState.isolatedDatabases = Array.isArray(d.isolatedDatabases) ? d.isolatedDatabases : [];
      app.recoveryState.pendingSwitchId = String(d.pendingSwitchId || '');
      app.updateRecoveryPwdUI();
      app.renderIsolatedDatabases();
    } catch (_) {}
  };
  app.updateRecoveryPwdUI = function updateRecoveryPwdUI() {
    const status = document.getElementById('recoveryPwdStatus');
    const changeBtn = document.getElementById('recoveryPwdChangeBtn');
    const resetBtn = document.getElementById('recoveryPwdResetBtn');
    if (status) status.textContent = app.recoveryState.hasRecovery ? '已设置（必需）' : '状态异常';
    if (changeBtn) changeBtn.classList.toggle('hidden', !app.recoveryState.hasRecovery);
    if (resetBtn) resetBtn.classList.toggle('hidden', !app.recoveryState.hasRecovery);
  };
  app.formatIsolatedSize = function formatIsolatedSize(bytes) {
    const value = Number(bytes) || 0;
    if (value < 1024) return value + ' B';
    if (value < 1024 * 1024) return (value / 1024).toFixed(1) + ' KiB';
    if (value < 1024 * 1024 * 1024) return (value / 1024 / 1024).toFixed(1) + ' MiB';
    return (value / 1024 / 1024 / 1024).toFixed(2) + ' GiB';
  };
  app.formatIsolatedTime = function formatIsolatedTime(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '时间未知';
    return date.toLocaleString('zh-CN', {
      hour12: false
    });
  };
  app.renderIsolatedDatabases = function renderIsolatedDatabases() {
    const list = document.getElementById('isolatedDatabaseList');
    const count = document.getElementById('isolatedDatabaseCount');
    const pending = document.getElementById('isolatedDatabasePending');
    if (!list || !count || !pending) return;
    const databases = app.recoveryState.isolatedDatabases;
    count.textContent = databases.length + ' 份';
    list.replaceChildren();
    if (app.recoveryState.pendingSwitchId) {
      pending.replaceChildren();
      const pendingText = document.createElement('span');
      pendingText.textContent = '数据库切换已准备完成。请从托盘退出 ClipVault，再重新打开；不要直接关闭这个浏览器页面代替退出应用。';
      const cancelButton = document.createElement('button');
      cancelButton.type = 'button';
      cancelButton.className = 'btn-export';
      cancelButton.textContent = '取消切换';
      cancelButton.addEventListener('click', app.cancelIsolatedDatabaseSwitch);
      pending.append(pendingText, cancelButton);
      pending.classList.remove('hidden');
    } else {
      pending.replaceChildren();
      pending.classList.add('hidden');
    }
    if (!databases.length) {
      const empty = document.createElement('p');
      empty.className = 'isolated-empty';
      empty.textContent = '没有隔离的旧数据库。';
      list.appendChild(empty);
      return;
    }
    databases.forEach(database => {
      const row = document.createElement('div');
      row.className = 'isolated-item' + (database.canSwitch ? '' : ' is-unavailable');
      const main = document.createElement('div');
      main.className = 'isolated-item-main';
      const title = document.createElement('div');
      title.className = 'isolated-item-title';
      title.textContent = app.formatIsolatedTime(database.isolatedAt);
      const meta = document.createElement('div');
      meta.className = 'isolated-item-meta';
      meta.textContent = app.formatIsolatedSize(database.sizeBytes) + (database.canSwitch ? ' · 可验证恢复' : ' · 缺少恢复文件，无法切换');
      main.append(title, meta);
      const actions = document.createElement('div');
      actions.className = 'isolated-item-actions';
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'btn-export';
      button.textContent = app.recoveryState.pendingSwitchId === database.id ? '等待重启' : '切换查看';
      button.disabled = !database.canSwitch || Boolean(app.recoveryState.pendingSwitchId);
      button.addEventListener('click', () => app.showIsolatedDatabaseDialog(database.id));
      actions.appendChild(button);
      row.append(main, actions);
      list.appendChild(row);
    });
  };
  app.cancelIsolatedDatabaseSwitch = async function cancelIsolatedDatabaseSwitch() {
    try {
      const res = await app.protectedFetch('/api/v1/recovery/isolated-switch', {
        method: 'DELETE'
      }, {
        fresh: true
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data.ok) {
        app.toast(data.error || '无法取消数据库切换', 'err');
        return;
      }
      app.recoveryState.pendingSwitchId = '';
      app.renderIsolatedDatabases();
      app.toast('数据库切换已取消');
    } catch (_) {
      app.toast('无法取消数据库切换', 'err');
    }
  };
  app.showIsolatedDatabaseDialog = function (id) {
    const database = app.recoveryState.isolatedDatabases.find(item => item.id === id);
    if (!database || !database.canSwitch || app.recoveryState.pendingSwitchId) return;
    const html = `
      <div class="modal-overlay" id="isolatedDatabaseOverlay">
        <div class="modal-box secondary-box isolated-database-modal" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">切换到旧数据库</span>
            <button class="modal-close" type="button" data-action="close-isolated-database" aria-label="关闭">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <div class="migrate-notes">
              <p>验证成功后，本次切换会在下次完整启动时执行。当前数据库不会被覆盖，而是作为另一份隔离数据保留，之后可用它自己的恢复密码切回。</p>
            </div>
            <form id="isolatedDatabaseForm" class="lock-form">
              <label class="migrate-label" for="isolatedDatabasePassword">这份旧数据库的数据恢复密码</label>
              <input type="password" id="isolatedDatabasePassword" class="lock-input" placeholder="输入旧数据库的恢复密码" autocomplete="current-password">
              <button type="submit" class="lock-btn" id="isolatedDatabaseSubmit">验证并准备切换</button>
            </form>
            <p class="lock-err hidden" id="isolatedDatabaseError"></p>
          </div>
        </div>
      </div>`;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('isolatedDatabaseOverlay');
    const form = document.getElementById('isolatedDatabaseForm');
    const input = document.getElementById('isolatedDatabasePassword');
    const submit = document.getElementById('isolatedDatabaseSubmit');
    const error = document.getElementById('isolatedDatabaseError');
    app.closeIsolatedDatabaseDialog = createRemovableModal(overlay, { initialFocus: input }).close;
    form.addEventListener('submit', async event => {
      event.preventDefault();
      error.classList.add('hidden');
      if (!input.value) {
        error.textContent = '请输入这份旧数据库的数据恢复密码';
        error.classList.remove('hidden');
        input.focus();
        return;
      }
      submit.disabled = true;
      submit.textContent = '正在验证...';
      try {
        const res = await app.protectedFetch('/api/v1/recovery/isolated-switch', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({
            id: database.id,
            password: input.value
          })
        }, {
          fresh: true
        });
        const data = await res.json().catch(() => ({}));
        input.value = '';
        if (!res.ok || !data.ok) {
          error.textContent = data.error || '无法准备数据库切换';
          error.classList.remove('hidden');
          return;
        }
        app.recoveryState.pendingSwitchId = database.id;
        app.closeIsolatedDatabaseDialog();
        app.renderIsolatedDatabases();
        app.toast('切换已准备，请退出 ClipVault 后重新打开');
      } catch (_) {
        error.textContent = '无法连接本机服务，请稍后重试';
        error.classList.remove('hidden');
      } finally {
        submit.disabled = false;
        submit.textContent = '验证并准备切换';
      }
    });
  };
  app.showRecoveryDialog = function (action) {
    const isChange = action === 'change';
    const isReset = action === 'reset';
    const title = '数据恢复密码' + (isReset ? '重置' : '修改');
    const oldLabel = isChange ? '当前恢复密码' : null;
    const html = `
      <div class="modal-overlay" id="recDialogOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${title}</span>
            <button class="modal-close" type="button" data-action="close-recovery" aria-label="关闭恢复密码对话框">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <form id="recDialogForm" class="lock-form">
              <input type="text" name="username" autocomplete="username" value="clipvault" class="u-inline-11" aria-hidden="true">
              ${oldLabel ? `<input type="password" id="recDlgOld" class="lock-input" placeholder="${oldLabel}" autocomplete="current-password">` : ''}
              <input type="password" id="recDlgNew" class="lock-input" placeholder="新恢复密码" autocomplete="new-password">
              <input type="password" id="recDlgConfirm" class="lock-input" placeholder="确认新恢复密码" autocomplete="new-password">
              <button type="submit" class="lock-btn">确定</button>
            </form>
            <p class="lock-err hidden" id="recDlgErr"></p>
          </div>
        </div>
      </div>
    `;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('recDialogOverlay');
    const form = document.getElementById('recDialogForm');
    const errEl = document.getElementById('recDlgErr');
    const oldInput = document.getElementById('recDlgOld');
    const newInput = document.getElementById('recDlgNew');
    const confirmInput = document.getElementById('recDlgConfirm');
    app.closeRecDialog = createRemovableModal(overlay, {
      initialFocus: oldInput || newInput
    }).close;
    form.addEventListener('submit', async e => {
      e.preventDefault();
      const oldPwd = oldInput ? oldInput.value : '';
      const newPwd = newInput.value;
      const confirmPwd = confirmInput.value;
      errEl.classList.add('hidden');
      if (newPwd !== confirmPwd) {
        errEl.textContent = '两次输入的密码不一致';
        errEl.classList.remove('hidden');
        return;
      }
      try {
        const baseUrl = isReset ? '/api/v1/recovery/reset' : '/api/v1/recovery/password';
        const body = isChange ? {
          oldPassword: oldPwd,
          newPassword: newPwd
        } : {
          password: newPwd
        };
        const res = await app.protectedFetch(baseUrl, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify(body)
        }, {
          fresh: isReset
        });
        const d = await res.json();
        if (d.ok) {
          app.closeRecDialog();
          app.toast('数据恢复密码已' + (isReset ? '重置' : '修改'));
          app.recoveryState.hasRecovery = true;
          app.updateRecoveryPwdUI();
        } else {
          errEl.textContent = d.error || '操作失败';
          errEl.classList.remove('hidden');
        }
      } catch (_) {
        errEl.textContent = '网络错误';
        errEl.classList.remove('hidden');
      }
    });
  };

  // ── View switch ───────────────────────────────────────────
}
