import { apiFetch, handleSecondaryRequired } from './api.js';
import { appStore } from './store.js';

(function(){
	const fetch = apiFetch;
	let closeSecondaryModal, showPwdDialog, closePwdDialog;
	let showIsolatedDatabaseDialog, closeIsolatedDatabaseDialog;
	let showRecoveryDialog, closeRecDialog, switchView;
	let openLicenseSettings, copyPurchaseInfo, activateLicense;
	let closeModal, modalCopy, snavTo, openMigrateDialog, closeMigrateDialog;
	let openSourceAppsDialog, closeSourceAppsDialog, saveSourceAppsDialog, removePattern;
	let showExportScopeDialog, closeExportScopeDialog, confirmExportScope, doExport, doExportCvexport;
	let closeCvexportDialog, showImportDialog, closeImportDialog;
	let doBackupDirNow, doBackupDirFullNow, doBackupEmailNow, doBackupEmailFullNow, doBackupEmailTest;
	let showEmailBackupHistory, closeGroupEditDialog, closeConfirmDialog;
	const actions = {
		'switch-view': el => switchView(el.dataset.view),
		'open-license-settings': () => openLicenseSettings(),
		'copy-purchase-info': () => copyPurchaseInfo(),
		'activate-license': () => activateLicense(),
		'settings-nav': el => snavTo(el.dataset.section, el.dataset.nav),
		'open-source-apps': () => openSourceAppsDialog(),
		'close-source-apps': () => closeSourceAppsDialog(),
		'save-source-apps': () => saveSourceAppsDialog(),
		'show-password': el => showPwdDialog(el.dataset.passwordType, el.dataset.passwordMode),
		'show-recovery': el => showRecoveryDialog(el.dataset.mode),
		'open-groups': el => openGroupModal(el.dataset.mode),
		'export': el => doExport(el.dataset.format),
		'export-encrypted': () => doExportCvexport(),
		'show-import': () => showImportDialog(),
		'backup-directory': () => doBackupDirNow(),
		'backup-directory-full': () => doBackupDirFullNow(),
		'backup-email': () => doBackupEmailNow(),
		'backup-email-full': () => doBackupEmailFullNow(),
		'backup-email-test': () => doBackupEmailTest(),
		'backup-email-history': () => showEmailBackupHistory(),
		'close-view-modal': () => closeModal(),
		'modal-copy': () => modalCopy(),
		'close-secondary': () => closeSecondaryModal()
		,'close-password': () => closePwdDialog()
		,'close-isolated-database': () => closeIsolatedDatabaseDialog()
		,'close-recovery': () => closeRecDialog()
		,'close-migrate': () => closeMigrateDialog()
		,'close-export-scope': () => closeExportScopeDialog()
		,'confirm-export-scope': () => confirmExportScope()
		,'close-encrypted-export': () => closeCvexportDialog()
		,'close-import': () => closeImportDialog()
		,'import': () => doImport()
		,'close-group-edit': () => closeGroupEditDialog()
		,'close-confirm': () => closeConfirmDialog()
		,'file-download': el => doDownload(Number(el.dataset.itemId), Number(el.dataset.fileIndex))
		,'file-open-directory': el => doOpenDir(Number(el.dataset.itemId), Number(el.dataset.fileIndex))
		,'file-hash': el => doFileHash(Number(el.dataset.itemId), Number(el.dataset.fileIndex))
		,'file-view': el => doFileView(Number(el.dataset.itemId), Number(el.dataset.fileIndex))
		,'remove-pattern': el => removePattern(Number(el.dataset.index))
		,'view-item': el => doView(Number(el.dataset.itemId))
		,'locate-item': el => doLocate(Number(el.dataset.itemId))
		,'copy-item': el => doCopy(Number(el.dataset.itemId), el)
		,'star-item': el => doStar(Number(el.dataset.itemId), el)
		,'assign-group': el => openGroupModal('assign', { itemId: Number(el.dataset.itemId) })
		,'delete-item': el => doDelete(Number(el.dataset.itemId), el.closest('article'))
	};
	document.addEventListener('click', event => {
		if (event.target.closest('[data-modal-panel]')) event.stopPropagation();
		const target = event.target.closest('[data-action]');
		if (!target) return;
		const action = actions[target.dataset.action];
		if (!action) return;
		event.preventDefault();
		action(target, event);
	});

  const Q    = document.getElementById('q'),
        PAGESIZE = document.getElementById('pagesize'),
        LIST = document.getElementById('list'),
        DOT  = document.getElementById('dot'),
        ST   = document.getElementById('st'),
        BADGE= document.getElementById('badge'),
        TOG  = document.getElementById('tog'),
        TOGTX= document.getElementById('togTx'),
        RC   = document.getElementById('rc'),
        AR   = document.getElementById('ar'),
        TC   = document.getElementById('tc');

  let debounce = 0, arTimer = 0, currentKind = '';
  let currentPage = 0, totalCount = 0, fullTotalCount = 0;
  let pendingLocateId = 0; // 定位后需要高亮的 item id
  let locateActive = false; // 定位进行中，阻止 resetSearch 干扰
  let filterDateFrom = '', filterDateTo = '';
  let currentGroupID = 0; // 0 = no group filter
  // 来源应用（exe）下拉与别名/白名单状态
  let sourceAliases = {};      // { [rawExeName]: customName }
  let sourceWhitelist = null;  // null = 搜索下拉显示全部
  let sourceAppsDraft = null;  // 弹窗草稿；取消时不污染当前配置
  let allSourceExes = [];      // [{name,count}]
  let currentSourceExes = [];  // 当前多选选中的真实 exe 名
	// freeLimit 由后端 GET /api/v1/license 下发，权威值是 Go 侧的 FreeTierVisibleLimit。
	// 前端不再自带默认条数：取不到时保持 0（含义为"限额未知"），所有带具体数字的限额文案一律不渲染。
	let licenseState = { licensed: true, enforced: true, status: 'loading', machineCode: '', version: '', freeLimit: 0 };
	let fullMachineId = '';
  const items = new Map();
  const filePaths = new Map();
  const NON_TEXT_KINDS = ['image','video','archive','office','files'];
  const IMAGE_EXTS = ['jpg','jpeg','png','gif','bmp','webp','svg','ico','tiff','tif','heic','heif','avif'];

  // ── Group state ───────────────────────────────────────────
  let allGroups = []; // flat list from API
  const GROUP_EXPAND = new Set(); // expanded group ids in group modal
  let groupModalMode = null; // 'filter' | 'assign' | 'manage'
  let groupModalOpts = {};   // { itemId, onSelect }

  // ── Auth state ───────────────────────────────────────────
  let authState = { hasLockPassword: false, hasSecondaryPassword: false, authenticated: false, secondaryAuthed: false };
  let secondaryResolve = null; // pending secondary auth promise resolver
  let secondaryAuthPromise = null;

  const LOCK_OVERLAY = document.getElementById('lockOverlay');
  const LOCK_FORM    = document.getElementById('lockForm');
  const LOCK_INPUT   = document.getElementById('lockPassword');
  const LOCK_ERR     = document.getElementById('lockErr');
  const SECONDARY_MODAL = document.getElementById('secondaryModal');
  const SECONDARY_FORM = document.getElementById('secondaryForm');
  const SECONDARY_INPUT = document.getElementById('secondaryPassword');
  const SECONDARY_ERR   = document.getElementById('secondaryErr');

  async function checkAuthStatus() {
    try {
      const d = await fetch('/api/v1/session').then(r => r.json());
      authState = d;
      appStore.update({ authenticated: d.authenticated, secondaryAuthenticated: d.secondaryAuthed });
      if (d.hasLockPassword && !d.authenticated) {
        showLockScreen();
        return false;
      }
      hideLockScreen();
      return true;
    } catch (_) {
      return false;
    }
  }

  function showLockScreen() {
    LOCK_OVERLAY.classList.add('show');
    LOCK_INPUT.value = '';
    LOCK_ERR.classList.add('hidden');
    setTimeout(() => LOCK_INPUT.focus(), 50);
  }

  function hideLockScreen() {
    LOCK_OVERLAY.classList.remove('show');
  }

  LOCK_FORM.addEventListener('submit', async (e) => {
    e.preventDefault();
    const pwd = LOCK_INPUT.value;
    LOCK_ERR.classList.add('hidden');
    try {
      const res = await fetch('/api/v1/session/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: pwd }),
      });
      const d = await res.json();
      if (d.ok) {
        hideLockScreen();
        authState.authenticated = true;
        appStore.update({ authenticated: true });
        init();
      } else {
        LOCK_ERR.textContent = d.error || '密码错误';
        LOCK_ERR.classList.remove('hidden');
        LOCK_INPUT.value = '';
        LOCK_INPUT.focus();
      }
    } catch (_) {
      LOCK_ERR.textContent = '网络错误';
      LOCK_ERR.classList.remove('hidden');
    }
  });

  // ── Secondary auth ───────────────────────────────────────
  let pendingSecondaryToken = null; // 一次性 token，timeout=0 模式下用
  let protectedItemRequestQueue = Promise.resolve();

  function withSecondaryToken(init) {
    const opts = Object.assign({}, init || {});
    const headers = new Headers(opts.headers || {});
    if (pendingSecondaryToken) {
      headers.set('X-ClipVault-Secondary-Token', pendingSecondaryToken);
      pendingSecondaryToken = null;
    }
    opts.headers = headers;
    return opts;
  }

  function secondaryFetch(url, init) {
    return fetch(url, withSecondaryToken(init));
  }

  function freshSecondaryFetch(url, init) {
    const opts = Object.assign({}, init || {});
    const headers = new Headers(opts.headers || {});
    if (pendingSecondaryToken) {
      headers.set('X-ClipVault-Fresh-Secondary', pendingSecondaryToken);
      pendingSecondaryToken = null;
    }
    opts.headers = headers;
    return fetch(url, opts);
  }

  function requestSecondaryAuth(forceFresh = false) {
    if (!authState.hasSecondaryPassword) return Promise.resolve(true);
    if (!forceFresh && pendingSecondaryToken) return Promise.resolve(true);
    if (secondaryAuthPromise) return secondaryAuthPromise;

    const run = (async () => {
      if (!forceFresh) {
        try {
          const st = await fetch('/api/v1/session').then(r => r.json());
          authState.secondaryAuthed = st.secondaryAuthed;
          appStore.update({ secondaryAuthenticated: st.secondaryAuthed });
          if (st.secondaryAuthed) return true;
        } catch (_) {}
      }
      return new Promise((resolve) => {
        secondaryResolve = resolve;
        SECONDARY_MODAL.classList.remove('hidden');
        SECONDARY_INPUT.value = '';
        SECONDARY_ERR.classList.add('hidden');
        setTimeout(() => SECONDARY_INPUT.focus(), 50);
      });
    })();
    secondaryAuthPromise = run.finally(() => { secondaryAuthPromise = null; });
    return secondaryAuthPromise;
  }

  closeSecondaryModal = function() {
    SECONDARY_MODAL.classList.add('hidden');
    if (secondaryResolve) { secondaryResolve(false); secondaryResolve = null; }
  };

  SECONDARY_FORM.addEventListener('submit', async (e) => {
    e.preventDefault();
    const pwd = SECONDARY_INPUT.value;
    SECONDARY_ERR.classList.add('hidden');
    try {
      const res = await fetch('/api/v1/session/secondary', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: pwd }),
      });
      const d = await res.json();
      if (d.ok) {
        SECONDARY_MODAL.classList.add('hidden');
        authState.secondaryAuthed = d.secondaryCached === true;
        appStore.update({ secondaryAuthenticated: authState.secondaryAuthed });
        pendingSecondaryToken = d.secondaryToken || null;
        toast(d.message || '二次验证成功');
        if (secondaryResolve) { secondaryResolve(true); secondaryResolve = null; }
      } else {
        SECONDARY_ERR.textContent = d.error || '二次密码错误';
        SECONDARY_ERR.classList.remove('hidden');
        SECONDARY_INPUT.value = '';
        SECONDARY_INPUT.focus();
      }
    } catch (_) {
      SECONDARY_ERR.textContent = '网络错误';
      SECONDARY_ERR.classList.remove('hidden');
    }
  });

  SECONDARY_MODAL.addEventListener('click', (e) => {
    if (e.target === SECONDARY_MODAL) closeSecondaryModal();
  });

  // ── Password set/change dialog ───────────────────────────
  showPwdDialog = function(type, action) {
	const trigger = document.activeElement;
    const isChange = action === 'change';
    const isRemove = action === 'remove';
    const passwordName = type === 'lock' ? '锁屏密码' : '二次密码';
    const title = passwordName + (isRemove ? '删除' : (isChange ? '修改' : '设置'));
    const oldLabel = (isChange || isRemove) ? '当前密码' : null;
    const removeWarning = type === 'lock'
      ? '删除后，任何能打开此服务的人都可以直接进入 ClipVault。'
      : '删除后，敏感数据、导出、备份和安全设置将不再受二次密码保护。';
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
              <button type="submit" class="lock-btn${isRemove ? ' danger' : ''}">${isRemove ? '确认删除' : (isChange ? '修改' : '设置')}${passwordName}</button>
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

    closePwdDialog = function() {
      overlay.remove();
	  if (trigger && trigger.focus) trigger.focus();
    };

	showInput.addEventListener('change', () => {
	  const visible = showInput.checked;
	  [oldInput, newInput, confirmInput].filter(Boolean).forEach(input => { input.type = visible ? 'text' : 'password'; });
	});

	overlay.addEventListener('keydown', (e) => {
	  if (e.key === 'Escape') { e.preventDefault(); closePwdDialog(); return; }
	  if (e.key !== 'Tab') return;
	  const focusable = Array.from(overlay.querySelectorAll('button:not([disabled]), input:not([disabled])')).filter(el => el.offsetParent !== null);
	  if (!focusable.length) return;
	  const first = focusable[0], last = focusable[focusable.length - 1];
	  if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
	  else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
	});

    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) closePwdDialog();
    });

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const oldPwd = oldInput ? oldInput.value : '';
      const newPwd = newInput ? newInput.value : '';
      const confirmPwd = confirmInput ? confirmInput.value : '';
      errEl.classList.add('hidden');

      if (!isRemove && newPwd !== confirmPwd) { errEl.textContent = '两次输入的密码不一致'; errEl.classList.remove('hidden'); return; }
      if (!isRemove && (Array.from(newPwd).length < 8 || newPwd.trim() !== newPwd)) { errEl.textContent = '密码至少需要 8 个字符，且首尾不能有空格'; errEl.classList.remove('hidden'); return; }

      try {
        const res = await fetch('/api/v1/session/password', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ type, action, oldPassword: oldPwd, newPassword: newPwd }),
        });
        const d = await res.json();
        if (d.ok) {
          closePwdDialog();
          const successMessage = passwordName + '已' + (isRemove ? '删除' : (isChange ? '修改' : '设置'));
          toast(d.runtimeError ? successMessage + '，但' + d.runtimeError : successMessage, d.runtimeError ? 'err' : undefined);
          await checkAuthStatus();
          updatePwdStatusUI();
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

  function updatePwdStatusUI() {
    const lockSet = authState.hasLockPassword;
    const secSet = authState.hasSecondaryPassword;
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
    updateRecoveryPwdUI();
  }

  // ── Recovery password management ──────────────────────────
  let recoveryState = { hasRecovery: false, isolatedDatabases: [], pendingSwitchId: '' };

  async function loadRecoveryStatus() {
    try {
      const ok = await requestSecondaryAuth();
      if (!ok) return;
      const res = await secondaryFetch('/api/v1/recovery');
      if (res.status === 403) return; // 二次验证失败，静默跳过
      if (!res.ok) return;
      const d = await res.json();
      recoveryState.hasRecovery = d.hasRecovery;
      recoveryState.isolatedDatabases = Array.isArray(d.isolatedDatabases) ? d.isolatedDatabases : [];
      recoveryState.pendingSwitchId = String(d.pendingSwitchId || '');
      updateRecoveryPwdUI();
      renderIsolatedDatabases();
    } catch (_) {}
  }

  function updateRecoveryPwdUI() {
    const status = document.getElementById('recoveryPwdStatus');
    const changeBtn = document.getElementById('recoveryPwdChangeBtn');
    const resetBtn = document.getElementById('recoveryPwdResetBtn');
    if (status) status.textContent = recoveryState.hasRecovery ? '已设置（必需）' : '状态异常';
    if (changeBtn) changeBtn.classList.toggle('hidden', !recoveryState.hasRecovery);
    if (resetBtn) resetBtn.classList.toggle('hidden', !recoveryState.hasRecovery);
  }

  function formatIsolatedSize(bytes) {
    const value = Number(bytes) || 0;
    if (value < 1024) return value + ' B';
    if (value < 1024 * 1024) return (value / 1024).toFixed(1) + ' KiB';
    if (value < 1024 * 1024 * 1024) return (value / 1024 / 1024).toFixed(1) + ' MiB';
    return (value / 1024 / 1024 / 1024).toFixed(2) + ' GiB';
  }

  function formatIsolatedTime(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '时间未知';
    return date.toLocaleString('zh-CN', { hour12: false });
  }

  function renderIsolatedDatabases() {
    const list = document.getElementById('isolatedDatabaseList');
    const count = document.getElementById('isolatedDatabaseCount');
    const pending = document.getElementById('isolatedDatabasePending');
    if (!list || !count || !pending) return;
    const databases = recoveryState.isolatedDatabases;
    count.textContent = databases.length + ' 份';