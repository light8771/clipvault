import { app } from "./app-context.js";
import { createRemovableModal } from './modal-controller.js';

const t = window.clipvaultI18n.t;

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
    if (status) status.textContent = app.recoveryState.hasRecovery ? t('recovery.pwd_set') : t('recovery.pwd_status_error');
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
    if (Number.isNaN(date.getTime())) return t('recovery.time_unknown');
    return date.toLocaleString(window.clipvaultI18n.locale(), {
      hour12: false
    });
  };
  app.renderIsolatedDatabases = function renderIsolatedDatabases() {
    const list = document.getElementById('isolatedDatabaseList');
    const count = document.getElementById('isolatedDatabaseCount');
    const pending = document.getElementById('isolatedDatabasePending');
    if (!list || !count || !pending) return;
    const databases = app.recoveryState.isolatedDatabases;
    count.textContent = t('recovery.count', { count: databases.length });
    list.replaceChildren();
    if (app.recoveryState.pendingSwitchId) {
      pending.replaceChildren();
      const pendingText = document.createElement('span');
      pendingText.textContent = t('recovery.switch_pending');
      const cancelButton = document.createElement('button');
      cancelButton.type = 'button';
      cancelButton.className = 'btn-export';
      cancelButton.textContent = t('recovery.cancel_switch');
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
      empty.textContent = t('recovery.empty');
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
      meta.textContent = app.formatIsolatedSize(database.sizeBytes) + (database.canSwitch ? ' · ' + t('recovery.can_switch') : ' · ' + t('recovery.cannot_switch'));
      main.append(title, meta);
      const actions = document.createElement('div');
      actions.className = 'isolated-item-actions';
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'btn-export';
      button.textContent = app.recoveryState.pendingSwitchId === database.id ? t('recovery.waiting_restart') : t('recovery.switch_view');
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
        app.toast(data.error || t('recovery.cancel_failed'), 'err');
        return;
      }
      app.recoveryState.pendingSwitchId = '';
      app.renderIsolatedDatabases();
      app.toast(t('recovery.cancelled'));
    } catch (_) {
      app.toast(t('recovery.cancel_failed'), 'err');
    }
  };
  app.showIsolatedDatabaseDialog = function (id) {
    const database = app.recoveryState.isolatedDatabases.find(item => item.id === id);
    if (!database || !database.canSwitch || app.recoveryState.pendingSwitchId) return;
    const html = `
      <div class="modal-overlay" id="isolatedDatabaseOverlay">
        <div class="modal-box secondary-box isolated-database-modal" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${app.esc(t('recovery.switch_title'))}</span>
            <button class="modal-close" type="button" data-action="close-isolated-database" aria-label="${app.esc(t('common.close'))}">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <div class="migrate-notes">
              <p>${app.esc(t('recovery.switch_note'))}</p>
            </div>
            <form id="isolatedDatabaseForm" class="lock-form">
              <label class="migrate-label" for="isolatedDatabasePassword">${app.esc(t('recovery.password_label'))}</label>
              <input type="password" id="isolatedDatabasePassword" class="lock-input" placeholder="${app.esc(t('recovery.password_placeholder'))}" autocomplete="current-password">
              <button type="submit" class="lock-btn" id="isolatedDatabaseSubmit">${app.esc(t('recovery.verify_and_prepare'))}</button>
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
        error.textContent = t('recovery.enter_password');
        error.classList.remove('hidden');
        input.focus();
        return;
      }
      submit.disabled = true;
      submit.textContent = t('recovery.verifying');
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
          error.textContent = data.error || t('recovery.prepare_failed');
          error.classList.remove('hidden');
          return;
        }
        app.recoveryState.pendingSwitchId = database.id;
        app.closeIsolatedDatabaseDialog();
        app.renderIsolatedDatabases();
        app.toast(t('recovery.prepared'));
      } catch (_) {
        error.textContent = t('recovery.connect_failed');
        error.classList.remove('hidden');
      } finally {
        submit.disabled = false;
        submit.textContent = t('recovery.verify_and_prepare');
      }
    });
  };
  app.showRecoveryDialog = function (action) {
    const isChange = action === 'change';
    const isReset = action === 'reset';
    const title = isReset ? t('recovery.reset_title') : t('recovery.change_title');
    const oldLabel = isChange ? t('recovery.current_password') : null;
    const html = `
      <div class="modal-overlay" id="recDialogOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${app.esc(title)}</span>
            <button class="modal-close" type="button" data-action="close-recovery" aria-label="${app.esc(t('recovery.close_label'))}">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <form id="recDialogForm" class="lock-form">
              <input type="text" name="username" autocomplete="username" value="clipvault" class="u-inline-11" aria-hidden="true">
              ${oldLabel ? `<input type="password" id="recDlgOld" class="lock-input" placeholder="${app.esc(oldLabel)}" autocomplete="current-password">` : ''}
              <input type="password" id="recDlgNew" class="lock-input" placeholder="${app.esc(t('recovery.new_password'))}" autocomplete="new-password">
              <input type="password" id="recDlgConfirm" class="lock-input" placeholder="${app.esc(t('recovery.confirm_password'))}" autocomplete="new-password">
              <button type="submit" class="lock-btn">${app.esc(t('common.confirm'))}</button>
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
        errEl.textContent = t('recovery.password_mismatch');
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
          app.toast(isReset ? t('recovery.reset_done') : t('recovery.change_done'));
          app.recoveryState.hasRecovery = true;
          app.updateRecoveryPwdUI();
        } else {
          errEl.textContent = d.error || t('common.action_failed');
          errEl.classList.remove('hidden');
        }
      } catch (_) {
        errEl.textContent = t('common.network_error');
        errEl.classList.remove('hidden');
      }
    });
  };

  // ── View switch ───────────────────────────────────────────
}
