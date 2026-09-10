import { app } from "./app-context.js";
import { trapModalFocus, createRemovableModal } from './modal-controller.js';
import { createOperationState } from './operation-state.js';

const t = window.clipvaultI18n.t;

export function registerAuth() {
  app.authState = {
    hasLockPassword: false,
    hasSecondaryPassword: false,
    authenticated: false,
    secondaryAuthed: false
  };
  app.secondaryResolve = null;
  app.secondaryAuthPromise = null;
  app.LOCK_OVERLAY = document.getElementById('lockOverlay');
  app.LOCK_FORM = document.getElementById('lockForm');
  app.LOCK_INPUT = document.getElementById('lockPassword');
  app.LOCK_ERR = document.getElementById('lockErr');
  app.SECONDARY_MODAL = document.getElementById('secondaryModal');
  app.SECONDARY_FORM = document.getElementById('secondaryForm');
  app.SECONDARY_INPUT = document.getElementById('secondaryPassword');
  app.SECONDARY_ERR = document.getElementById('secondaryErr');
  const secondaryOperation = createOperationState(app.SECONDARY_FORM);
  let secondaryGeneration = 0;
  app.checkAuthStatus = async function checkAuthStatus() {
    try {
      const d = await app.fetch('/api/v1/session').then(r => r.json());
      app.authState = d;
      app.appStore.update({
        authenticated: d.authenticated,
        secondaryAuthenticated: d.secondaryAuthed
      });
      if (d.hasLockPassword && !d.authenticated) {
        app.showLockScreen();
        return false;
      }
      app.hideLockScreen();
      return true;
    } catch (_) {
      return false;
    }
  };
  app.showLockScreen = function showLockScreen() {
    app.LOCK_OVERLAY.classList.add('show');
    app.LOCK_INPUT.value = '';
    app.LOCK_ERR.classList.add('hidden');
    setTimeout(() => app.LOCK_INPUT.focus(), 50);
  };
  app.hideLockScreen = function hideLockScreen() {
    app.LOCK_OVERLAY.classList.remove('show');
  };
  app.LOCK_FORM.addEventListener('submit', async e => {
    e.preventDefault();
    const pwd = app.LOCK_INPUT.value;
    app.LOCK_ERR.classList.add('hidden');
    try {
      const res = await app.fetch('/api/v1/session/login', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          password: pwd
        })
      });
      const d = await res.json();
      if (d.ok) {
        app.hideLockScreen();
        app.authState.authenticated = true;
        app.appStore.update({
          authenticated: true
        });
        app.init();
      } else {
        app.LOCK_ERR.textContent = d.error || t('auth.lock_wrong_password');
        app.LOCK_ERR.classList.remove('hidden');
        app.LOCK_INPUT.value = '';
        app.LOCK_INPUT.focus();
      }
    } catch (_) {
      app.LOCK_ERR.textContent = t('common.network_error');
      app.LOCK_ERR.classList.remove('hidden');
    }
  });

  // ── Secondary auth ───────────────────────────────────────
  app.pendingSecondaryToken = null;
  app.protectedItemRequestQueue = Promise.resolve();
  app.withSecondaryToken = function withSecondaryToken(init) {
    const opts = Object.assign({}, init || {});
    const headers = new Headers(opts.headers || {});
    if (app.pendingSecondaryToken) {
      headers.set('X-ClipVault-Secondary-Token', app.pendingSecondaryToken);
      app.pendingSecondaryToken = null;
    }
    opts.headers = headers;
    return opts;
  };
  app.secondaryFetch = function secondaryFetch(url, init) {
    return app.fetch(url, app.withSecondaryToken(init));
  };
  app.freshSecondaryFetch = function freshSecondaryFetch(url, init) {
    const opts = Object.assign({}, init || {});
    const headers = new Headers(opts.headers || {});
    if (app.pendingSecondaryToken) {
      headers.set('X-ClipVault-Fresh-Secondary', app.pendingSecondaryToken);
      app.pendingSecondaryToken = null;
    }
    opts.headers = headers;
    return app.fetch(url, opts);
  };
  app.expireSecondaryAuth = function expireSecondaryAuth() {
    app.authState.secondaryAuthed = false;
    app.pendingSecondaryToken = null;
    app.appStore.update({
      secondaryAuthenticated: false
    });
  };
  app.protectedFetch = function protectedFetch(url, init, options) {
    const fresh = options?.fresh === true;
    const send = () => fresh ? app.freshSecondaryFetch(url, init) : app.secondaryFetch(url, init);
    return app.requestWithSecondaryAuth(send, app.requestSecondaryAuth, app.expireSecondaryAuth, {
      fresh
    });
  };
  app.requireOK = async function requireOK(response, fallback) {
    if (response.ok) return response;
    const failure = await app.readAPIError(response, fallback);
    throw new app.APIError(failure.code, failure.message, failure.status);
  };
  app.requestSecondaryAuth = function requestSecondaryAuth(forceFresh = false) {
    if (!app.authState.hasSecondaryPassword) return Promise.resolve(true);
    if (!forceFresh && app.pendingSecondaryToken) return Promise.resolve(true);
    if (app.secondaryAuthPromise) return app.secondaryAuthPromise;
    const run = (async () => {
      if (!forceFresh) {
        try {
          const st = await app.fetch('/api/v1/session').then(r => r.json());
          app.authState.secondaryAuthed = st.secondaryAuthed;
          app.appStore.update({
            secondaryAuthenticated: st.secondaryAuthed
          });
          if (st.secondaryAuthed) return true;
        } catch (_) {}
      }
      return new Promise(resolve => {
        secondaryGeneration++;
        app.secondaryTrigger = document.activeElement;
        app.secondaryResolve = resolve;
        app.SECONDARY_MODAL.classList.remove('hidden');
        app.SECONDARY_INPUT.value = '';
        app.SECONDARY_ERR.classList.add('hidden');
        setTimeout(() => app.SECONDARY_INPUT.focus(), 50);
      });
    })();
    app.secondaryAuthPromise = run.finally(() => {
      app.secondaryAuthPromise = null;
    });
    return app.secondaryAuthPromise;
  };
  app.closeSecondaryModal = function () {
    secondaryGeneration++;
    secondaryOperation.finish();
    app.SECONDARY_INPUT.value = '';
    app.SECONDARY_MODAL.classList.add('hidden');
    if (app.secondaryResolve) {
      app.secondaryResolve(false);
      app.secondaryResolve = null;
    }
    if (app.secondaryTrigger?.isConnected && typeof app.secondaryTrigger.focus === 'function') app.secondaryTrigger.focus();
    app.secondaryTrigger = null;
  };
  app.SECONDARY_FORM.addEventListener('submit', async e => {
    e.preventDefault();
    if (!app.secondaryResolve || secondaryOperation.busy) return;
    const generation = secondaryGeneration;
    const pwd = app.SECONDARY_INPUT.value;
    app.SECONDARY_ERR.classList.add('hidden');
    secondaryOperation.begin(t('auth.verifying'));
    try {
      const res = await app.fetch('/api/v1/session/secondary', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          password: pwd
        })
      });
      const d = await res.json();
      if (generation !== secondaryGeneration) return;
      secondaryOperation.finish();
      if (res.ok && d.ok) {
        app.SECONDARY_INPUT.value = '';
        app.SECONDARY_MODAL.classList.add('hidden');
        app.authState.secondaryAuthed = d.secondaryCached === true;
        app.appStore.update({
          secondaryAuthenticated: app.authState.secondaryAuthed
        });
        app.pendingSecondaryToken = d.secondaryToken || null;
        app.toast(d.message || t('auth.secondary_ok'));
        if (app.secondaryResolve) {
          app.secondaryResolve(true);
          app.secondaryResolve = null;
        }
        if (app.secondaryTrigger?.isConnected && typeof app.secondaryTrigger.focus === 'function') app.secondaryTrigger.focus();
        app.secondaryTrigger = null;
      } else if (d.code === 'lock_session_expired') {
        app.SECONDARY_MODAL.classList.add('hidden');
        app.authState.authenticated = false;
        app.appStore.update({ authenticated: false });
        app.expireSecondaryAuth();
        app.showLockScreen();
        app.LOCK_ERR.textContent = d.error || t('auth.lock_session_expired');
        app.LOCK_ERR.classList.remove('hidden');
        if (app.secondaryResolve) {
          app.secondaryResolve(false);
          app.secondaryResolve = null;
        }
        app.secondaryTrigger = null;
      } else {
        app.SECONDARY_ERR.textContent = d.error || t('auth.secondary_wrong_password');
        app.SECONDARY_ERR.classList.remove('hidden');
        app.SECONDARY_INPUT.value = '';
        app.SECONDARY_INPUT.focus();
      }
    } catch (_) {
      if (generation !== secondaryGeneration) return;
      app.SECONDARY_ERR.textContent = t('common.network_error');
      app.SECONDARY_ERR.classList.remove('hidden');
    } finally {
      if (generation === secondaryGeneration) secondaryOperation.finish();
    }
  });
  app.SECONDARY_MODAL.addEventListener('click', e => {
    if (e.target === app.SECONDARY_MODAL) app.closeSecondaryModal();
  });
  app.SECONDARY_MODAL.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      e.preventDefault();
      app.closeSecondaryModal();
      return;
    }
    trapModalFocus(app.SECONDARY_MODAL, e);
  });

  // ── Password set/change dialog ───────────────────────────
  app.showPwdDialog = function (type, action) {
    if (document.getElementById('pwdDialogOverlay')) return;
    const isChange = action === 'change';
    const isRemove = action === 'remove';
    const nameKey = type === 'lock' ? 'auth.pwd_name_lock' : 'auth.pwd_name_secondary';
    const passwordName = t(nameKey);
    const verbKey = isRemove ? 'auth.pwd_verb_remove' : isChange ? 'auth.pwd_verb_change' : 'auth.pwd_verb_set';
    const title = t('auth.pwd_title', { verb: t(verbKey), name: passwordName });
    const oldLabel = isChange || isRemove ? t('auth.pwd_current') : null;
    const removeWarning = type === 'lock' ? t('auth.pwd_remove_lock_warning') : t('auth.pwd_remove_secondary_warning');
    const submitLabel = isRemove
      ? t('auth.pwd_submit_remove', { name: passwordName })
      : t('auth.pwd_submit', { verb: t(verbKey), name: passwordName });
    const html = `
      <div class="modal-overlay" id="pwdDialogOverlay">
        <div class="modal-box secondary-box" role="dialog" aria-modal="true" aria-labelledby="pwdDialogTitle" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title" id="pwdDialogTitle">${app.esc(title)}</span>
            <button class="modal-close" type="button" aria-label="${app.esc(t('auth.pwd_close_label'))}" data-action="close-password">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <form id="pwdDialogForm" class="lock-form">
              <input type="text" name="username" autocomplete="username" value="clipvault" class="u-inline-11" aria-hidden="true">
              ${oldLabel ? `<label class="field-label" for="pwdDlgOld">${app.esc(oldLabel)}</label><input type="password" id="pwdDlgOld" class="lock-input" autocomplete="current-password">` : ''}
              ${isRemove ? `<p class="password-remove-warning" role="note">${app.esc(removeWarning)}</p>` : `
              <label class="field-label" for="pwdDlgNew">${app.esc(t('auth.pwd_new'))}</label>
              <input type="password" id="pwdDlgNew" class="lock-input" autocomplete="new-password" aria-describedby="pwdDlgHint">
              <p class="field-hint" id="pwdDlgHint">${app.esc(t('auth.pwd_rule_hint'))}</p>
              <label class="field-label" for="pwdDlgConfirm">${app.esc(t('auth.pwd_confirm'))}</label>
              <input type="password" id="pwdDlgConfirm" class="lock-input" autocomplete="new-password">`}
              <label class="password-show"><input type="checkbox" id="pwdDlgShow"> ${app.esc(t('auth.pwd_show'))}</label>
              <button type="submit" class="lock-btn${isRemove ? ' danger' : ''}">${app.esc(submitLabel)}</button>
            </form>
            <p class="lock-err hidden" id="pwdDlgErr" role="alert" aria-live="assertive"></p>
          </div>
        </div>
      </div>
    `;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('pwdDialogOverlay');
    const form = document.getElementById('pwdDialogForm');
    const errEl = document.getElementById('pwdDlgErr');
    const oldInput = document.getElementById('pwdDlgOld');
    const newInput = document.getElementById('pwdDlgNew');
    const confirmInput = document.getElementById('pwdDlgConfirm');
    const showInput = document.getElementById('pwdDlgShow');
    const operation = createOperationState(overlay, form.parentElement);
    const modal = createRemovableModal(overlay, {
      initialFocus: oldInput || newInput,
      canClose: () => !operation.busy
    });
    app.closePwdDialog = modal.close;
    showInput.addEventListener('change', () => {
      const visible = showInput.checked;
      [oldInput, newInput, confirmInput].filter(Boolean).forEach(input => {
        input.type = visible ? 'text' : 'password';
      });
    });
    form.addEventListener('submit', async e => {
      e.preventDefault();
      if (operation.busy) return;
      const oldPwd = oldInput ? oldInput.value : '';
      const newPwd = newInput ? newInput.value : '';
      const confirmPwd = confirmInput ? confirmInput.value : '';
      errEl.classList.add('hidden');
      if (!isRemove && newPwd !== confirmPwd) {
        errEl.textContent = t('auth.pwd_mismatch');
        errEl.classList.remove('hidden');
        return;
      }
      if (!isRemove && (Array.from(newPwd).length < 8 || newPwd.trim() !== newPwd)) {
        errEl.textContent = t('auth.pwd_rule_error');
        errEl.classList.remove('hidden');
        return;
      }
      operation.begin(t('common.loading'));
      try {
        const res = await app.fetch('/api/v1/session/password', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({
            type,
            action,
            oldPassword: oldPwd,
            newPassword: newPwd
          })
        });
        const d = await res.json();
        if (res.ok && d.ok) {
          operation.finish();
          modal.close();
          const successMessage = t('auth.pwd_done', { verb: t(verbKey), name: passwordName });
          app.toast(d.runtimeError ? t('auth.pwd_done_runtime', { message: successMessage, error: d.runtimeError }) : successMessage, d.runtimeError ? 'err' : undefined);
          await app.checkAuthStatus();
          app.updatePwdStatusUI();
        } else {
          errEl.textContent = d.error || t('common.action_failed');
          errEl.classList.remove('hidden');
        }
      } catch (_) {
        errEl.textContent = t('common.network_error');
        errEl.classList.remove('hidden');
      } finally {
        if (operation.busy) operation.finish();
      }
    });
  };
  app.updatePwdStatusUI = function updatePwdStatusUI() {
    const lockSet = app.authState.hasLockPassword;
    const secSet = app.authState.hasSecondaryPassword;
    const lockStatus = document.getElementById('lockPwdStatus');
    const lockSetBtn = document.getElementById('lockPwdSetBtn');
    const lockChangeBtn = document.getElementById('lockPwdChangeBtn');
    const lockRemoveBtn = document.getElementById('lockPwdRemoveBtn');
    const secStatus = document.getElementById('secondaryPwdStatus');
    const secChangeBtn = document.getElementById('secondaryPwdChangeBtn');
    if (lockStatus) lockStatus.textContent = lockSet ? t('security.pwd_set_status') : t('security.pwd_not_set');
    if (lockSetBtn) lockSetBtn.classList.toggle('hidden', lockSet);
    if (lockChangeBtn) lockChangeBtn.classList.toggle('hidden', !lockSet);
    if (lockRemoveBtn) lockRemoveBtn.classList.toggle('hidden', !lockSet);
    if (secStatus) secStatus.textContent = secSet ? t('recovery.pwd_set') : t('recovery.pwd_status_error');
    if (secChangeBtn) secChangeBtn.classList.toggle('hidden', !secSet);
    app.updateRecoveryPwdUI();
  };
}
