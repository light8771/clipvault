import { app } from "./app-context.js";
export function registerAuth() {
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
        app.LOCK_ERR.textContent = d.error || '密码错误';
        app.LOCK_ERR.classList.remove('hidden');
        app.LOCK_INPUT.value = '';
        app.LOCK_INPUT.focus();
      }
    } catch (_) {
      app.LOCK_ERR.textContent = '网络错误';
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
    app.SECONDARY_MODAL.classList.add('hidden');
    if (app.secondaryResolve) {
      app.secondaryResolve(false);
      app.secondaryResolve = null;
    }
  };
  app.SECONDARY_FORM.addEventListener('submit', async e => {
    e.preventDefault();
    const pwd = app.SECONDARY_INPUT.value;
    app.SECONDARY_ERR.classList.add('hidden');
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
      if (d.ok) {
        app.SECONDARY_MODAL.classList.add('hidden');
        app.authState.secondaryAuthed = d.secondaryCached === true;
        app.appStore.update({
          secondaryAuthenticated: app.authState.secondaryAuthed
        });
        app.pendingSecondaryToken = d.secondaryToken || null;
        app.toast(d.message || '二次验证成功');
        if (app.secondaryResolve) {
          app.secondaryResolve(true);
          app.secondaryResolve = null;
        }
      } else {
        app.SECONDARY_ERR.textContent = d.error || '二次密码错误';
        app.SECONDARY_ERR.classList.remove('hidden');
        app.SECONDARY_INPUT.value = '';
        app.SECONDARY_INPUT.focus();
      }
    } catch (_) {
      app.SECONDARY_ERR.textContent = '网络错误';
      app.SECONDARY_ERR.classList.remove('hidden');
    }
  });
  app.SECONDARY_MODAL.addEventListener('click', e => {
    if (e.target === app.SECONDARY_MODAL) app.closeSecondaryModal();
  });

  // ── Password set/change dialog ───────────────────────────
  app.showPwdDialog = function (type, action) {
    const trigger = document.activeElement;
    const isChange = action === 'change';
    const isRemove = action === 'remove';
    const passwordName = type === 'lock' ? '锁屏密码' : '二次密码';
    const title = passwordName + (isRemove ? '删除' : isChange ? '修改' : '设置');
    const oldLabel = isChange || isRemove ? '当前密码' : null;
    const removeWarning = type === 'lock' ? '删除后，任何能打开此服务的人都可以直接进入 ClipVault。' : '删除后，敏感数据、导出、备份和安全设置将不再受二次密码保护。';
    const html = `
      <div class="modal-overlay" id="pwdDialogOverlay">
        <div class="modal-box secondary-box" role="dialog" aria-modal="true" aria-labelledby="pwdDialogTitle" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title" id="pwdDialogTitle">${title}</span>
            <button class="modal-close" type="button" aria-label="关闭密码设置" data-action="close-password">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <form id="pwdDialogForm" class="lock-form">
              <input type="text" name="username" autocomplete="username" value="clipvault" class="u-inline-11" aria-hidden="true">
              ${oldLabel ? `<label class="field-label" for="pwdDlgOld">${oldLabel}</label><input type="password" id="pwdDlgOld" class="lock-input" autocomplete="current-password">` : ''}
              ${isRemove ? `<p class="password-remove-warning" role="note">${removeWarning}</p>` : `
              <label class="field-label" for="pwdDlgNew">新密码</label>
              <input type="password" id="pwdDlgNew" class="lock-input" autocomplete="new-password" aria-describedby="pwdDlgHint">
              <p class="field-hint" id="pwdDlgHint">至少 8 个字符，首尾不能有空格</p>
              <label class="field-label" for="pwdDlgConfirm">确认新密码</label>
              <input type="password" id="pwdDlgConfirm" class="lock-input" autocomplete="new-password">`}
              <label class="password-show"><input type="checkbox" id="pwdDlgShow"> 显示密码</label>
              <button type="submit" class="lock-btn${isRemove ? ' danger' : ''}">${isRemove ? '确认删除' : isChange ? '修改' : '设置'}${passwordName}</button>
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
    setTimeout(() => (oldInput || newInput).focus(), 50);
    app.closePwdDialog = function () {
      overlay.remove();
      if (trigger && trigger.focus) trigger.focus();
    };
    showInput.addEventListener('change', () => {
      const visible = showInput.checked;
      [oldInput, newInput, confirmInput].filter(Boolean).forEach(input => {
        input.type = visible ? 'text' : 'password';
      });
    });
    overlay.addEventListener('keydown', e => {
      if (e.key === 'Escape') {
        e.preventDefault();
        app.closePwdDialog();
        return;
      }
      if (e.key !== 'Tab') return;
      const focusable = Array.from(overlay.querySelectorAll('button:not([disabled]), input:not([disabled])')).filter(el => el.offsetParent !== null);
      if (!focusable.length) return;
      const first = focusable[0],
        last = focusable[focusable.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    });
    overlay.addEventListener('click', e => {
      if (e.target === overlay) app.closePwdDialog();
    });
    form.addEventListener('submit', async e => {
      e.preventDefault();
      const oldPwd = oldInput ? oldInput.value : '';
      const newPwd = newInput ? newInput.value : '';
      const confirmPwd = confirmInput ? confirmInput.value : '';
      errEl.classList.add('hidden');
      if (!isRemove && newPwd !== confirmPwd) {
        errEl.textContent = '两次输入的密码不一致';
        errEl.classList.remove('hidden');
        return;
      }
      if (!isRemove && (Array.from(newPwd).length < 8 || newPwd.trim() !== newPwd)) {
        errEl.textContent = '密码至少需要 8 个字符，且首尾不能有空格';
        errEl.classList.remove('hidden');
        return;
      }
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
        if (d.ok) {
          app.closePwdDialog();
          const successMessage = passwordName + '已' + (isRemove ? '删除' : isChange ? '修改' : '设置');
          app.toast(d.runtimeError ? successMessage + '，但' + d.runtimeError : successMessage, d.runtimeError ? 'err' : undefined);
          await app.checkAuthStatus();
          app.updatePwdStatusUI();
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
  app.updatePwdStatusUI = function updatePwdStatusUI() {
    const lockSet = app.authState.hasLockPassword;
    const secSet = app.authState.hasSecondaryPassword;
    const lockStatus = document.getElementById('lockPwdStatus');
    const lockSetBtn = document.getElementById('lockPwdSetBtn');
    const lockChangeBtn = document.getElementById('lockPwdChangeBtn');
    const lockRemoveBtn = document.getElementById('lockPwdRemoveBtn');
    const secStatus = document.getElementById('secondaryPwdStatus');
    const secChangeBtn = document.getElementById('secondaryPwdChangeBtn');
    if (lockStatus) lockStatus.textContent = lockSet ? '已设置' : '未设置';
    if (lockSetBtn) lockSetBtn.classList.toggle('hidden', lockSet);
    if (lockChangeBtn) lockChangeBtn.classList.toggle('hidden', !lockSet);
    if (lockRemoveBtn) lockRemoveBtn.classList.toggle('hidden', !lockSet);
    if (secStatus) secStatus.textContent = secSet ? '已设置（必需）' : '状态异常';
    if (secChangeBtn) secChangeBtn.classList.toggle('hidden', !secSet);
    app.updateRecoveryPwdUI();
  };
}
