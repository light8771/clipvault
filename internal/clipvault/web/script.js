(function(){
	const nativeFetch = window.fetch.bind(window);
	const apiRoutes = new Map([
		['/api/csrf', '/api/v1/session/csrf'], ['/api/appearance', '/api/v1/appearance'],
		['/api/auth/login', '/api/v1/session/login'], ['/api/auth/logout', '/api/v1/session/logout'],
		['/api/auth/status', '/api/v1/session'], ['/api/auth/secondary', '/api/v1/session/secondary'],
		['/api/auth/password', '/api/v1/session/password'], ['/api/restart', '/api/v1/system/restart'],
		['/api/brand/logo', '/api/v1/appearance/logo'], ['/api/brand/favicon', '/api/v1/appearance/favicon'],
		['/api/background/media', '/api/v1/appearance/background'], ['/api/search', '/api/v1/items'],
		['/api/license/status', '/api/v1/license'], ['/api/license/machine', '/api/v1/license/machine'],
		['/api/license/activate', '/api/v1/license/activation'], ['/api/source-exes', '/api/v1/items/sources'],
		['/api/state', '/api/v1/runtime'], ['/api/pause', '/api/v1/runtime/pause'],
		['/api/clear', '/api/v1/items/clear'], ['/api/config', '/api/v1/config'],
		['/api/storage/status', '/api/v1/storage'], ['/api/storage/relocate', '/api/v1/storage/relocation'],
		['/api/storage/open', '/api/v1/storage/open'], ['/api/diagnostics/export', '/api/v1/diagnostics'],
		['/api/sensitive/rules', '/api/v1/config/sensitive-rules'], ['/api/search/sensitive', '/api/v1/items/sensitive'],
		['/api/items/sensitive', '/api/v1/items/sensitive/item'], ['/api/items/position', '/api/v1/items/position'],
		['/api/file/check', '/api/v1/files/check'], ['/api/file', '/api/v1/files/content'],
		['/api/file/opendir', '/api/v1/files/open-directory'], ['/api/file/hash', '/api/v1/files/hash'],
		['/api/groups', '/api/v1/groups'], ['/api/items/group', '/api/v1/items/group'],
		['/api/items/groups', '/api/v1/items/groups'], ['/api/export/data', '/api/v1/transfer/export'],
		['/api/import/data', '/api/v1/transfer/import'], ['/api/recovery/status', '/api/v1/recovery'],
		['/api/recovery/change', '/api/v1/recovery/password'], ['/api/recovery/reset', '/api/v1/recovery/reset'],
		['/api/recovery/isolated/switch', '/api/v1/recovery/isolated-switch'],
		['/api/backup/status', '/api/v1/backup'], ['/api/backup/email/history', '/api/v1/backup/email-history'],
		['/api/backup/dir/now', '/api/v1/backup/directory'], ['/api/backup/email/now', '/api/v1/backup/email'],
		['/api/backup/email/test', '/api/v1/backup/email-test']
	]);
	function v1URL(value) {
		if (typeof value !== 'string') return value;
		const q = value.indexOf('?');
		const path = q < 0 ? value : value.slice(0, q);
		const mapped = apiRoutes.get(path) || (path === '/api/items' ? '/api/v1/items' : path);
		return mapped + (q < 0 ? '' : value.slice(q));
	}
	function unwrapJSONResponse(response) {
		const original = response.json.bind(response);
		response.json = async () => {
			const envelope = await original();
			if (envelope && envelope.error) {
				return { ok: false, code: envelope.error.code, error: envelope.error.message };
			}
			return envelope && Object.prototype.hasOwnProperty.call(envelope, 'data') ? envelope.data : envelope;
		};
		return response;
	}
	let csrfTokenPromise = null;

	async function getCSRFToken(forceRefresh) {
		if (forceRefresh) csrfTokenPromise = null;
		if (!csrfTokenPromise) {
			csrfTokenPromise = nativeFetch('/api/v1/session/csrf', { cache: 'no-store' })
				.then(r => {
					if (!r.ok) throw new Error('csrf bootstrap failed');
					return r.json();
				})
				.then(d => (d.data && d.data.token) || '');
		}
		return csrfTokenPromise;
	}

	window.fetch = async function(input, init) {
		input = v1URL(input);
		const opts = Object.assign({}, init || {});
		const method = String(opts.method || 'GET').toUpperCase();
		const target = typeof input === 'string' ? input : (input && input.url) || '';
		const isLocal = target.startsWith('/') || target.startsWith(window.location.origin);
		if (isLocal && ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) {
			const headers = new Headers(opts.headers || (typeof input !== 'string' && input ? input.headers : undefined));
			headers.set('X-ClipVault-CSRF', await getCSRFToken(false));
			opts.headers = headers;
		}
		let response = await nativeFetch(input, opts);
		if (isLocal && response.status === 403 && response.headers.get('X-ClipVault-CSRF-Invalid') === '1') {
			const headers = new Headers(opts.headers || {});
			headers.set('X-ClipVault-CSRF', await getCSRFToken(true));
			opts.headers = headers;
			response = await nativeFetch(input, opts);
		}
		return unwrapJSONResponse(response);
	};

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
	// freeLimit 由后端 GET /api/license/status 下发，权威值是 Go 侧的 FreeTierVisibleLimit。
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
      const d = await fetch('/api/auth/status').then(r => r.json());
      authState = d;
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
      const res = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: pwd }),
      });
      const d = await res.json();
      if (d.ok) {
        hideLockScreen();
        authState.authenticated = true;
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
          const st = await fetch('/api/auth/status').then(r => r.json());
          authState.secondaryAuthed = st.secondaryAuthed;
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

  window.closeSecondaryModal = function() {
    SECONDARY_MODAL.classList.add('hidden');
    if (secondaryResolve) { secondaryResolve(false); secondaryResolve = null; }
  };

  SECONDARY_FORM.addEventListener('submit', async (e) => {
    e.preventDefault();
    const pwd = SECONDARY_INPUT.value;
    SECONDARY_ERR.classList.add('hidden');
    try {
      const res = await fetch('/api/auth/secondary', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: pwd }),
      });
      const d = await res.json();
      if (d.ok) {
        SECONDARY_MODAL.classList.add('hidden');
        authState.secondaryAuthed = d.secondaryCached === true;
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
  window.showPwdDialog = function(type, action) {
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
        <div class="modal-box secondary-box" role="dialog" aria-modal="true" aria-labelledby="pwdDialogTitle" onclick="event.stopPropagation()">
          <div class="modal-head">
            <span class="modal-title" id="pwdDialogTitle">${title}</span>
            <button class="modal-close" type="button" aria-label="关闭密码设置" onclick="closePwdDialog()">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body" style="height:auto;padding:24px">
            <form id="pwdDialogForm" class="lock-form">
              <input type="text" name="username" autocomplete="username" value="clipvault" style="display:none" aria-hidden="true">
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

    window.closePwdDialog = function() {
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
        const res = await fetch('/api/auth/password', {
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
      const res = await secondaryFetch('/api/recovery/status');
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
    list.replaceChildren();

    if (recoveryState.pendingSwitchId) {
      pending.replaceChildren();
      const pendingText = document.createElement('span');
      pendingText.textContent = '数据库切换已准备完成。请从托盘退出 ClipVault，再重新打开；不要直接关闭这个浏览器页面代替退出应用。';
      const cancelButton = document.createElement('button');
      cancelButton.type = 'button';
      cancelButton.className = 'btn-export';
      cancelButton.textContent = '取消切换';
      cancelButton.addEventListener('click', cancelIsolatedDatabaseSwitch);
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

    databases.forEach((database) => {
      const row = document.createElement('div');
      row.className = 'isolated-item' + (database.canSwitch ? '' : ' is-unavailable');

      const main = document.createElement('div');
      main.className = 'isolated-item-main';
      const title = document.createElement('div');
      title.className = 'isolated-item-title';
      title.textContent = formatIsolatedTime(database.isolatedAt);
      const meta = document.createElement('div');
      meta.className = 'isolated-item-meta';
      meta.textContent = formatIsolatedSize(database.sizeBytes) + (database.canSwitch ? ' · 可验证恢复' : ' · 缺少恢复文件，无法切换');
      main.append(title, meta);

      const actions = document.createElement('div');
      actions.className = 'isolated-item-actions';
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'btn-export';
      button.textContent = recoveryState.pendingSwitchId === database.id ? '等待重启' : '切换查看';
      button.disabled = !database.canSwitch || Boolean(recoveryState.pendingSwitchId);
      button.addEventListener('click', () => showIsolatedDatabaseDialog(database.id));
      actions.appendChild(button);

      row.append(main, actions);
      list.appendChild(row);
    });
  }

  async function cancelIsolatedDatabaseSwitch() {
    try {
      const verified = await requestSecondaryAuth(true);
      if (!verified) return;
      const res = await freshSecondaryFetch('/api/recovery/isolated/switch', { method: 'DELETE' });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data.ok) {
        toast(data.error || '无法取消数据库切换', 'err');
        return;
      }
      recoveryState.pendingSwitchId = '';
      renderIsolatedDatabases();
      toast('数据库切换已取消');
    } catch (_) {
      toast('无法取消数据库切换', 'err');
    }
  }

  window.showIsolatedDatabaseDialog = function(id) {
    const database = recoveryState.isolatedDatabases.find((item) => item.id === id);
    if (!database || !database.canSwitch || recoveryState.pendingSwitchId) return;
    const html = `
      <div class="modal-overlay" id="isolatedDatabaseOverlay">
        <div class="modal-box secondary-box isolated-database-modal" onclick="event.stopPropagation()">
          <div class="modal-head">
            <span class="modal-title">切换到旧数据库</span>
            <button class="modal-close" type="button" onclick="closeIsolatedDatabaseDialog()" aria-label="关闭">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body" style="height:auto;padding:24px">
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
    window.closeIsolatedDatabaseDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', (event) => { if (event.target === overlay) closeIsolatedDatabaseDialog(); });
    setTimeout(() => input.focus(), 50);

    form.addEventListener('submit', async (event) => {
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
        const verified = await requestSecondaryAuth(true);
        if (!verified) {
          error.textContent = '需要重新输入二次密码才能切换数据库';
          error.classList.remove('hidden');
          return;
        }
        const res = await freshSecondaryFetch('/api/recovery/isolated/switch', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: database.id, password: input.value }),
        });
        const data = await res.json().catch(() => ({}));
        input.value = '';
        if (!res.ok || !data.ok) {
          error.textContent = data.error || '无法准备数据库切换';
          error.classList.remove('hidden');
          return;
        }
        recoveryState.pendingSwitchId = database.id;
        closeIsolatedDatabaseDialog();
        renderIsolatedDatabases();
        toast('切换已准备，请退出 ClipVault 后重新打开');
      } catch (_) {
        error.textContent = '无法连接本机服务，请稍后重试';
        error.classList.remove('hidden');
      } finally {
        submit.disabled = false;
        submit.textContent = '验证并准备切换';
      }
    });
  };

  window.showRecoveryDialog = function(action) {
    const isChange = action === 'change';
    const isReset = action === 'reset';
    const title = '数据恢复密码' + (isReset ? '重置' : '修改');
    const oldLabel = isChange ? '当前恢复密码' : null;
    const html = `
      <div class="modal-overlay" id="recDialogOverlay">
        <div class="modal-box secondary-box" onclick="event.stopPropagation()">
          <div class="modal-head">
            <span class="modal-title">${title}</span>
            <button class="modal-close" onclick="closeRecDialog()">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body" style="height:auto;padding:24px">
            <form id="recDialogForm" class="lock-form">
              <input type="text" name="username" autocomplete="username" value="clipvault" style="display:none" aria-hidden="true">
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
    setTimeout(() => (oldInput || newInput).focus(), 50);

    window.closeRecDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', (e) => { if (e.target === overlay) closeRecDialog(); });

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const oldPwd = oldInput ? oldInput.value : '';
      const newPwd = newInput.value;
      const confirmPwd = confirmInput.value;
      errEl.classList.add('hidden');

      if (newPwd !== confirmPwd) { errEl.textContent = '两次输入的密码不一致'; errEl.classList.remove('hidden'); return; }

      try {
        const ok = await requestSecondaryAuth(isReset);
        if (!ok) { errEl.textContent = '需要二次验证才能操作'; errEl.classList.remove('hidden'); return; }
        const baseUrl = isReset ? '/api/recovery/reset' : '/api/recovery/change';
        const body = isChange
          ? { oldPassword: oldPwd, newPassword: newPwd }
          : { password: newPwd };

        const doFetch = async () => {
          const fetcher = isReset ? freshSecondaryFetch : secondaryFetch;
          return await fetcher(baseUrl, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
          });
        };

        let res = await doFetch();
        if (res.status === 403) {
          const ok2 = await requestSecondaryAuth(isReset);
          if (!ok2) { errEl.textContent = '需要二次验证才能操作'; errEl.classList.remove('hidden'); return; }
          res = await doFetch();
        }
        const d = await res.json();
        if (d.ok) {
          closeRecDialog();
          toast('数据恢复密码已' + (isReset ? '重置' : '修改'));
          recoveryState.hasRecovery = true;
          updateRecoveryPwdUI();
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
  let viewSwitchSequence = 0;

  window.switchView = async function(view) {
    const request = ++viewSwitchSequence;
    const isHistory = view === 'history';
    if (!isHistory) {
      setupAutoSave();
      const ok = await requestSecondaryAuth();
      if (request !== viewSwitchSequence) return false;
      if (!ok) {
        document.getElementById('viewHistory').classList.remove('hidden');
        document.getElementById('viewSettings').classList.add('hidden');
        document.getElementById('tabHistory').classList.add('active');
        document.getElementById('tabSettings').classList.remove('active');
        toast('已取消进入设置，需要二次密码验证', 'err');
        return false;
      }
      await loadSettings();
      if (request !== viewSwitchSequence) return false;
    }
    document.getElementById('viewHistory').classList.toggle('hidden', !isHistory);
    document.getElementById('viewSettings').classList.toggle('hidden', isHistory);
    document.getElementById('tabHistory').classList.toggle('active', isHistory);
    document.getElementById('tabSettings').classList.toggle('active', !isHistory);
    return true;
  };

  // ── Filter chips ──────────────────────────────────────────
  const GROUP_CHIP = document.getElementById('groupChip');

  document.getElementById('kindFilters').addEventListener('click', async e => {
    const chip = e.target.closest('.filter-chip');
    if (!chip) return;
    const kind = chip.dataset.kind;
    // 分组 chip：打开分组选择弹窗
    if (kind === '__group__') {
      if (allGroups.length === 0) { await loadGroups(); }
      openGroupModal('filter');
      return;
    }
    // 敏感数据分组需要二次验证
    if (kind === '__sensitive__') {
      const ok = await requestSecondaryAuth();
      if (!ok) { toast('需要二次验证才能查看敏感数据', 'err'); return; }
    }
    document.querySelectorAll('.filter-chip').forEach(c => c.classList.remove('active'));
    chip.classList.add('active');
    currentKind = kind;
    currentGroupID = 0; // reset group filter when switching to other kinds
    resetSearch();
  });

  // ── Toast ─────────────────────────────────────────────────
  function toast(msg, type = 'ok') {
    const el = document.createElement('div');
    el.className = 'tk ' + type;
    el.textContent = msg;
	el.setAttribute('role', type === 'err' ? 'alert' : 'status');
	el.setAttribute('aria-live', type === 'err' ? 'assertive' : 'polite');
    TC.appendChild(el);
    requestAnimationFrame(() => el.classList.add('show'));
    const duration = type === 'err' ? 7000 : 3500;
    setTimeout(() => {
      el.classList.remove('show');
      setTimeout(() => el.remove(), 260);
    }, duration);
  }

  // 归一化后端授权状态。后端未下发 freeLimit、或下发了非正数/非数字时一律归零，
  // 避免把 undefined/0 直接拼进用户可见文案（例如"仅显示最近 undefined 条"）。
  function normalizeLicenseState(d) {
    const raw = Number(d && d.freeLimit);
    const freeLimit = Number.isFinite(raw) && raw > 0 ? Math.floor(raw) : 0;
    return Object.assign({}, d, { freeLimit });
  }

  async function loadLicenseStatus() {
    try {
      const d = await fetch('/api/license/status').then(r => r.json());
      licenseState = normalizeLicenseState(d);
      const banner = document.getElementById('licenseBanner');
      const badge = document.getElementById('licenseStatusBadge');
      const hint = document.getElementById('licenseStatusHint');
      const code = document.getElementById('licenseMachineCode');
      const activateBlock = document.getElementById('licenseActivateBlock');
      const details = document.getElementById('licenseDetailsRow');
      badge.classList.toggle('warn', !d.licensed);
      if (d.licensed) {
        badge.textContent = '已激活';
        hint.textContent = '当前设备永久有效';
      } else {
        badge.textContent = '未激活';
        hint.textContent = licenseStatusText(d.status);
      }
      banner.classList.toggle('hidden', d.licensed);
      activateBlock.classList.toggle('hidden', d.licensed);
      details.classList.toggle('hidden', !d.licensed);
      if (!d.licensed) {
        // 限额未知时退化为不带数字的说法，但保留横幅本身（它同时是激活入口）。
        const limit = licenseState.freeLimit;
        banner.querySelector('span').textContent = limit > 0
          ? '免费版运行中：剪贴板内容会照常完整保存，当前可浏览和检索最近 ' + limit + ' 条。激活后解锁完整历史记录，以及导出与备份功能。'
          : '免费版运行中：剪贴板内容会照常完整保存，当前可浏览和检索的历史记录条数有限。激活后解锁完整历史记录，以及导出与备份功能。';
      }
      if (d.licenseId) document.getElementById('licenseIdText').textContent = d.licenseId;
      if (d.machineCode) code.value = d.machineCode;
      await loadMachineID();
      await refreshState();
    } catch (_) {
      document.getElementById('licenseStatusBadge').textContent = '读取失败';
      document.getElementById('licenseStatusHint').textContent = '无法读取本机授权状态，请查看本地日志';
    }
  }

  function licenseStatusText(status) {
    if (status === 'machine_mismatch') return '授权与当前设备不匹配，请申请重新签发';
    if (status === 'corrupt') return '本地授权信息损坏，请重新输入激活码';
    if (status === 'machine_error') return '无法读取设备信息，请复制诊断信息联系支持';
    if (status === 'unconfigured') return '正式授权公钥尚未配置';
    return '输入对应当前设备的激活码后即可永久使用';
  }

  async function loadMachineID() {
    try {
      const d = await fetch('/api/license/machine').then(r => r.json());
      fullMachineId = d.machineId || '';
      if (d.machineCode) document.getElementById('licenseMachineCode').value = d.machineCode;
    } catch (_) {}
  }

  window.openLicenseSettings = async function() {
    if (await switchView('settings')) snavTo('s-license', 'snav-license');
  };

  window.copyPurchaseInfo = async function() {
    if (!fullMachineId) await loadMachineID();
    if (!fullMachineId) { toast('机器码读取失败', 'err'); return; }
    const displayCode = document.getElementById('licenseMachineCode').value || '';
    const info = '产品：ClipVault\n版本：' + (licenseState.version || '未知') +
      '\n机器码：' + fullMachineId +
      (displayCode && displayCode !== '读取中…' ? '\n识别码：' + displayCode : '');
    try {
      await navigator.clipboard.writeText(info);
      toast('购买信息已复制');
    } catch (_) { toast('复制失败', 'err'); }
  };

  window.activateLicense = async function() {
    const input = document.getElementById('licenseTokenInput');
    const button = document.getElementById('licenseActivateBtn');
    const message = document.getElementById('licenseMessage');
    const token = input.value.trim();
    if (!token) { message.textContent = '请粘贴完整激活码'; message.className = 'license-message err'; return; }
    button.disabled = true;
    button.textContent = '正在验证…';
    message.textContent = '';
    try {
      const res = await fetch('/api/license/activate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token }),
      });
      const d = await res.json();
      if (!d.ok) {
        message.textContent = licenseActivationError(d.error);
        message.className = 'license-message err';
        if (d.machineCode) document.getElementById('licenseMachineCode').value = d.machineCode;
        return;
      }
      input.value = '';
      message.textContent = '激活成功，已解锁全部历史记录与导出备份功能';
      message.className = 'license-message ok';
      toast('激活成功');
      await loadLicenseStatus();
    } catch (_) {
      message.textContent = '本地服务暂时无法完成激活，请重试';
      message.className = 'license-message err';
    } finally {
      button.disabled = false;
      button.textContent = '立即激活';
    }
  };

  function licenseActivationError(code) {
    if (code === 'license_bad_format') return '激活码不完整，请重新复制后粘贴';
    if (code === 'license_bad_signature') return '激活码验证未通过，请确认输入是否正确。';
    if (code === 'license_machine_mismatch') return '此激活码不适用于当前设备，请重新提供本机机器码。';
    if (code === 'license_unsupported') return '当前版本无法识别此激活码，请联系技术支持。';
    if (code === 'license_machine_unavailable') return '无法读取设备信息，尚未进行激活';
    return '激活码有效性或本地保存失败，请重试';
  }

  // ── Utils ─────────────────────────────────────────────────
  function esc(v) {
    return String(v).replace(/[&<>"']/g, c =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])
    );
  }

  function highlight(text, q) {
    const e = esc(text);
    if (!q.trim()) return e;
    const p = esc(q).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    try { return e.replace(new RegExp(p, 'gi'), m => '<mark>' + m + '</mark>'); }
    catch (_) { return e; }
  }

  function relTime(s) {
    const d = Math.floor((Date.now() - new Date(s).getTime()) / 1000);
    if (d < 5)      return '刚刚';
    if (d < 60)     return d + '秒前';
    if (d < 3600)   return Math.floor(d / 60) + '分钟前';
    if (d < 86400)  return Math.floor(d / 3600) + '小时前';
    if (d < 604800) return Math.floor(d / 86400) + '天前';
    return new Date(s).toLocaleString();
  }

  // ── State ─────────────────────────────────────────────────
  async function refreshState() {
    try {
      const d = await fetch('/api/state').then(r => r.json());
      DOT.className = 'dot' + (d.paused ? ' paused' : '');
      ST.textContent = d.paused ? '已暂停' : '记录中';
      TOG.className = 'btn-toggle' + (d.paused ? ' off' : '');
      TOG.disabled = false;
      TOGTX.textContent = d.paused ? '恢复' : '暂停';
      const togIcon = document.getElementById('togIcon');
      togIcon.innerHTML = d.paused
        ? '<polygon points="5 3 19 12 5 21 5 3"/>'
        : '<circle cx="12" cy="12" r="10"/><line x1="10" y1="15" x2="10" y2="9"/><line x1="14" y1="15" x2="14" y2="9"/>';
    } catch (_) {}
  }

  // ── Copy ──────────────────────────────────────────────────
  const cpHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg> 复制';

  async function fetchSensitiveText(id) {
    const url = '/api/items/sensitive?id=' + id;
    const res = await authorizedItemFetch(id, url);
    if (!res.ok) return null;
    const d = await res.json();
    return d.item ? d.item.text : null;
  }

  async function doCopy(id, btn) {
    const item = items.get(id);
    if (!item) return;
    let textToCopy = item.text;
    if (item.sensitive) {
      try {
        textToCopy = await fetchSensitiveText(id);
      } catch (_) {
        textToCopy = null;
      }
      if (textToCopy === null) { toast('需要二次验证才能复制敏感内容', 'err'); return; }
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg> 已复制';
      btn.classList.add('ok');
      toast('已复制到剪切板');
      setTimeout(() => { btn.innerHTML = cpHTML; btn.classList.remove('ok'); }, 1600);
    } catch (_) { toast('复制失败', 'err'); }
  }

  // ── Delete ────────────────────────────────────────────────
  async function doDelete(id, art) {
    try {
      const res = await authorizedItemFetch(id, '/api/items?id=' + id, { method: 'DELETE' });
      if (!res.ok) throw 0;
      art.style.transition = 'opacity .18s, transform .18s';
      art.style.opacity = '0';
      art.style.transform = 'translateX(12px)';
      setTimeout(() => art.remove(), 200);
      items.delete(id);
      BADGE.textContent = items.size;
      toast('已删除');
    } catch (_) { toast('删除失败', 'err'); }
  }

  // ── Star ──────────────────────────────────────────────────
  const starOnHTML  = '<svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="1.5"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>';
  const starOffHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>';

  async function doStar(id, btn) {
    const item = items.get(id);
    if (!item) return;
    const newStarred = !item.starred;
    try {
      const res = await authorizedItemFetch(id, '/api/items?id=' + id + '&starred=' + (newStarred ? '1' : '0'), { method: 'PATCH' });
      if (!res.ok) throw 0;
      item.starred = newStarred;
      const art = btn.closest('article');
      btn.innerHTML = newStarred ? starOnHTML : starOffHTML;
      btn.classList.toggle('on', newStarred);
      btn.title = newStarred ? '取消收藏' : '收藏';
      art.dataset.starred = newStarred ? '1' : '0';
      toast(newStarred ? '已收藏' : '已取消收藏');
    } catch (_) { toast('操作失败', 'err'); }
  }

  // ── Expand ────────────────────────────────────────────────
  function doExpand(btn) {} // 已在此版本废弃，功能由 doView 弹窗替代

  // ── View modal ────────────────────────────────────────
  let currentModalId = 0;
  const VIEW_MODAL  = document.getElementById('viewModal');
  const MODAL_PRE   = document.getElementById('modalPre');
  const MODAL_TITLE = document.getElementById('modalTitle');

  async function doView(id) {
    const item = items.get(id);
    if (!item) return;
    currentModalId = id;
    const kindKey = item.kind || 'text';

    let displayText = item.text;
    if (item.sensitive) {
      try {
        const url = '/api/items/sensitive?id=' + id;
        const res = await authorizedItemFetch(id, url);
        if (!res.ok) throw new Error('HTTP ' + res.status);
        const d = await res.json();
        if (d.item && d.item.text) {
          displayText = d.item.text;
        }
      } catch (_) { toast('获取敏感内容失败', 'err'); return; }
    }

    if (isNonTextKind(kindKey)) {
      const paths = displayText.split('\n').filter(Boolean);
      MODAL_TITLE.textContent = '文件列表 · #' + id;
      MODAL_PRE.style.display = 'none';
      let preview = document.getElementById('modalFilePreview');
      if (!preview) {
        preview = document.createElement('div');
        preview.id = 'modalFilePreview';
        preview.className = 'modal-file-preview';
        document.querySelector('.modal-body').appendChild(preview);
      }
      preview.style.display = 'flex';
      preview.innerHTML = paths.map((p, idx) => {
        const name = getBaseName(p);
        const ext = getExt(name);
        const isImg = IMAGE_EXTS.includes(ext);
        return '<div class="modal-file-item">' +
          (isImg && ext !== 'svg' ? '<img data-file-index="' + idx + '" loading="lazy" alt="' + esc(name) + '">' : '') +
          '<div class="modal-file-name">' + esc(name) + '</div>' +
          '<div class="modal-file-path">' + esc(p) + '</div>' +
        '</div>';
      }).join('');
      preview.querySelectorAll('img[data-file-index]').forEach(img => {
        loadFileImage(img, item.id, Number(img.dataset.fileIndex));
      });
    } else {
      MODAL_TITLE.textContent = '内容预览 · #' + id + (item.sensitive ? ' · 敏感' : '');
      MODAL_PRE.style.display = 'block';
      MODAL_PRE.textContent = displayText;
      const preview = document.getElementById('modalFilePreview');
      if (preview) preview.style.display = 'none';
    }
    VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  }

  window.closeModal = function() {
    VIEW_MODAL.classList.add('hidden');
    document.body.style.overflow = '';
    MODAL_PRE.style.display = 'block';
    const preview = document.getElementById('modalFilePreview');
    if (preview) { preview.innerHTML = ''; preview.style.display = 'none'; }
  };

  window.modalCopy = async function() {
    const item = items.get(currentModalId);
    if (!item) return;
    const btn = document.getElementById('modalCopyBtn');
    let textToCopy = item.text;
    if (item.sensitive) {
      try {
        textToCopy = await fetchSensitiveText(currentModalId);
      } catch (_) {
        textToCopy = null;
      }
      if (textToCopy === null) { toast('需要二次验证才能复制敏感内容', 'err'); return; }
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg> 已复制';
      btn.classList.add('ok');
      toast('已复制到剪切板');
      setTimeout(() => {
        btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg> 复制';
        btn.classList.remove('ok');
      }, 1600);
    } catch (_) { toast('复制失败', 'err'); }
  };

  // ── Kind label map ────────────────────────────────
  const KIND_LABEL = {
    text:    '文本',
    image:   '图片',
    video:   '视频',
    archive: '压缩包',
    office:  'Office',
    files:   '其他文件',
  };

  // ── File helpers ──────────────────────────────────────────
  function isNonTextKind(k) { return NON_TEXT_KINDS.includes(k); }
  function getBaseName(p) { const m = p.match(/[\\/]?([^\\/]+)$/); return m ? m[1] : p; }
  function getExt(name) { const m = name.match(/\.([^./\\]+)$/); return m ? m[1].toLowerCase() : ''; }
  function formatSize(b) {
    if (b < 1024) return b + ' B';
    if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
    if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
    return (b / 1073741824).toFixed(2) + ' GB';
  }
  function fileIconHTML() {
    return '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>';
  }

  const dlHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg> 下载';
  const dirHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg> 目录';
  const hashHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="4" y1="9" x2="20" y2="9"/><line x1="4" y1="15" x2="20" y2="15"/><line x1="10" y1="3" x2="8" y2="21"/><line x1="16" y1="3" x2="14" y2="21"/></svg> Hash';
  const fileObjectURLs = new Set();

  function revokeFileObjectURLs() {
    fileObjectURLs.forEach(url => URL.revokeObjectURL(url));
    fileObjectURLs.clear();
  }

  async function authorizedItemFetch(itemId, url, init) {
    const sensitive = !!items.get(itemId)?.sensitive;
    if (!sensitive) return fetch(url, init);

    const run = async () => {
      for (let attempt = 0; attempt < 2; attempt++) {
        const ok = await requestSecondaryAuth();
        if (!ok) throw new Error('需要二次验证');
        const res = await secondaryFetch(url, init);
        if (res.status !== 403) return res;
        const failure = await res.clone().json().catch(() => ({}));
        if (failure.code !== 'secondary_required') return res;
        authState.secondaryAuthed = false;
      }
      throw new Error('需要二次验证');
    };
    const queued = protectedItemRequestQueue.then(run, run);
    protectedItemRequestQueue = queued.then(() => undefined, () => undefined);
    return queued;
  }

  async function loadFileImage(img, itemId, idx) {
    if (!img) return;
    try {
      const res = await authorizedItemFetch(itemId, '/api/file?item_id=' + itemId + '&file_index=' + idx);
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const url = URL.createObjectURL(await res.blob());
      fileObjectURLs.add(url);
      img.src = url;
    } catch (_) {
      img.style.display = 'none';
    }
  }

  function renderFileList(it) {
    const paths = it.text.split('\n').filter(Boolean);
    filePaths.set(it.id, paths);
    return '<div class="file-list">' + paths.map((p, idx) => {
      const name = getBaseName(p);
      const ext = getExt(name);
      const isImg = IMAGE_EXTS.includes(ext);
      return '<div class="file-row" id="frow-' + it.id + '-' + idx + '">' +
        (isImg ? '<div class="file-thumb" id="fthumb-' + it.id + '-' + idx + '"></div>' : '<div class="file-icon">' + fileIconHTML() + '</div>') +
        '<div class="file-info">' +
          '<div class="file-name">' + esc(name) + '</div>' +
          '<div class="file-path">' + esc(p) + '</div>' +
          '<div class="file-status" id="fstat-' + it.id + '-' + idx + '"><span class="fcheck">检查中...</span></div>' +
          '<div class="file-hash" id="fhash-' + it.id + '-' + idx + '"></div>' +
        '</div>' +
        '<div class="file-actions">' +
          '<button class="ab dl" onclick="doDownload(' + it.id + ',' + idx + ')" id="fdl-' + it.id + '-' + idx + '" disabled>' + dlHTML + '</button>' +
          '<button class="ab dir" onclick="doOpenDir(' + it.id + ',' + idx + ')" id="fdir-' + it.id + '-' + idx + '" disabled>' + dirHTML + '</button>' +
          '<button class="ab hash" onclick="doFileHash(' + it.id + ',' + idx + ')" id="fhashbtn-' + it.id + '-' + idx + '" disabled>' + hashHTML + '</button>' +
        '</div>' +
      '</div>';
    }).join('') + '</div>';
  }

  async function checkFiles(itemId) {
    const paths = filePaths.get(itemId);
    if (!paths) return;
    for (let idx = 0; idx < paths.length; idx++) {
      const p = paths[idx];
      try {
        const fileRef = 'item_id=' + itemId + '&file_index=' + idx;
        const d = await authorizedItemFetch(itemId, '/api/file/check?' + fileRef).then(r => r.json());
        const stat = document.getElementById('fstat-' + itemId + '-' + idx);
        const dl = document.getElementById('fdl-' + itemId + '-' + idx);
        const dir = document.getElementById('fdir-' + itemId + '-' + idx);
        const hashBtn = document.getElementById('fhashbtn-' + itemId + '-' + idx);
        if (d.exists) {
          if (stat) stat.innerHTML = '<span class="fok">\u2713 ' + formatSize(d.size) + '</span>';
          if (dl) dl.disabled = false;
          if (dir) dir.disabled = false;
          if (hashBtn) hashBtn.disabled = false;
          const ext = getExt(d.name || p);
          if (IMAGE_EXTS.includes(ext)) {
            const thumb = document.getElementById('fthumb-' + itemId + '-' + idx);
            if (thumb && ext !== 'svg') {
              thumb.innerHTML = '<img loading="lazy" onclick="doFileView(' + itemId + ',' + idx + ')">';
              loadFileImage(thumb.querySelector('img'), itemId, idx);
            }
          }
        } else {
          if (stat) stat.innerHTML = '<span class="fmiss">\u26a0 \u6587\u4ef6\u4e0d\u5b58\u5728</span>';
        }
      } catch (_) {
        const stat = document.getElementById('fstat-' + itemId + '-' + idx);
        if (stat) stat.innerHTML = '<span class="fmiss">\u26a0 \u68c0\u67e5\u5931\u8d25</span>';
      }
    }
  }

  async function doDownload(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    try {
      const res = await authorizedItemFetch(itemId, '/api/file?item_id=' + itemId + '&file_index=' + idx + '&download=1');
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement('a');
      a.href = url;
      a.download = getBaseName(paths[idx]);
      a.style.display = 'none';
      document.body.appendChild(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      toast('开始下载');
    } catch (e) { toast('下载失败: ' + (e.message || e), 'err'); }
  }

  async function doOpenDir(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const btn = document.getElementById('fdir-' + itemId + '-' + idx);
    if (btn) { btn.disabled = true; }
    try {
      const res = await authorizedItemFetch(itemId, '/api/file/opendir?item_id=' + itemId + '&file_index=' + idx, { method: 'POST' });
      if (!res.ok) {
        const msg = await res.text().catch(() => '');
        throw new Error(msg || ('HTTP ' + res.status));
      }
      toast('已打开所在目录并选中文件');
    } catch (e) {
      toast('打开目录失败: ' + (e.message || e), 'err');
    } finally {
      if (btn) { btn.disabled = false; }
    }
  }

  async function doFileHash(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const btn = document.getElementById('fhashbtn-' + itemId + '-' + idx);
    const hashDiv = document.getElementById('fhash-' + itemId + '-' + idx);
    if (btn) { btn.disabled = true; }
    if (hashDiv) hashDiv.innerHTML = '<span class="fcheck">计算中...</span>';
    try {
      const d = await authorizedItemFetch(itemId, '/api/file/hash?item_id=' + itemId + '&file_index=' + idx).then(r => r.json());
      if (d.ok) {
        if (hashDiv) hashDiv.innerHTML =
          '<div class="fhash-line"><span class="fhash-label">MD5</span> <span class="fhash-val">' + esc(d.md5) + '</span></div>' +
          '<div class="fhash-line"><span class="fhash-label">SHA256</span> <span class="fhash-val">' + esc(d.sha256) + '</span></div>';
      } else {
        if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 ' + esc(d.error || '计算失败') + '</span>';
      }
    } catch (e) {
      if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 计算失败</span>';
    } finally {
      if (btn) { btn.disabled = false; }
    }
  }

  async function doFileView(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const p = paths[idx];
    const name = getBaseName(p);
    currentModalId = itemId;
    MODAL_TITLE.textContent = name;
    MODAL_PRE.style.display = 'none';
    let preview = document.getElementById('modalFilePreview');
    if (!preview) {
      preview = document.createElement('div');
      preview.id = 'modalFilePreview';
      preview.className = 'modal-file-preview';
      document.querySelector('.modal-body').appendChild(preview);
    }
    preview.style.display = 'flex';
    preview.innerHTML = getExt(name) === 'svg' ? '<div class="fmiss">SVG 文件仅支持下载，不提供内联预览</div>' : '<img alt="' + esc(name) + '">';
    if (getExt(name) !== 'svg') await loadFileImage(preview.querySelector('img'), itemId, idx);
    VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  }

  // ── Render ────────────────────────────────────────────────
  const viewHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg> 查看';
  const delHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';
  const groupBtnHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><path d="M9 12h6M9 16h4"/></svg>';
  const locateHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3"/></svg>';

  function render(arr, q) {
    revokeFileObjectURLs();
    items.clear();
    filePaths.clear();
    arr.forEach(i => items.set(i.id, i));
    // BADGE and RC updated by updatePagination()

    if (!arr.length) {
      LIST.innerHTML = `<div class="empty">
        <svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2">
          <path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/>
          <rect x="9" y="3" width="6" height="4" rx="1"/>
          <line x1="9" y1="12" x2="15" y2="12"/>
          <line x1="9" y1="16" x2="13" y2="16"/>
        </svg>
        <span>暂无剪切板记录</span>
      </div>`;
      return;
    }

    LIST.innerHTML = arr.map(it => {
      const kindKey   = it.kind || 'text';
      const isNonText = isNonTextKind(kindKey);
      const kindClass = 'mkind mkind-' + kindKey;
      const kindLabel = KIND_LABEL[kindKey] || kindKey;
      const fullTime  = esc(new Date(it.createdAt).toLocaleString());
      const starHTML  = it.starred ? starOnHTML : starOffHTML;
      const starTitle = it.starred ? '取消收藏' : '收藏';
      const sensHTML  = it.sensitive ? '<span class="msens">敏感</span>' : '';
      const sensAttr  = it.sensitive ? ' data-sensitive="1"' : '';
      // 来源应用（前台窗口法）：ShowExe 缺省视为 true；标签用别名（若有），悬停看窗口标题与完整路径
      let srcHTML = '';
      if (currentCfg?.source?.showExe !== false && it.sourceExeName) {
        const srcLabel = sourceAliases[it.sourceExeName] || it.sourceExeName;
        const srcTip = esc([it.sourceWindowTitle, it.sourceExePath].filter(Boolean).join('\n'));
        srcHTML = `<span class="msep">·</span><span class="msource" title="${srcTip}">${esc(srcLabel)}</span>`;
      }

      let bodyHTML, charText, showView;
      if (it.sensitive && !authState.secondaryAuthed) {
        // 敏感内容未通过二次认证时显示遮蔽占位
        bodyHTML = `<pre class="open sensitive-mask"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="vertical-align:-2px;margin-right:4px"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>敏感内容已隐藏，点击查看按钮查看明文</pre>`;
        charText = it.charLen >= 1000 ? (it.charLen / 1000).toFixed(1) + 'k 字' : it.charLen + ' 字';
        showView = true;
      } else if (isNonText) {
        const paths = it.text.split('\n').filter(Boolean);
        bodyHTML = renderFileList(it);
        charText = paths.length + ' 个文件';
        showView = true;
      } else {
        const long = it.text.length > 500;
        bodyHTML = `<pre${long ? '' : ' class="open"'}>${highlight(it.text, q)}</pre>`;
        charText = it.charLen >= 1000 ? (it.charLen / 1000).toFixed(1) + 'k 字' : it.charLen + ' 字';
        showView = long;
      }

      return `<article data-id="${it.id}" data-kind="${kindKey}" data-starred="${it.starred ? '1' : '0'}"${sensAttr}>
        <div class="card-body">${bodyHTML}</div>
        <div class="card-foot">
          <div class="card-meta">
            <span class="mid">#${it.id}</span>
            <span class="msep">·</span>
            <span class="${kindClass}">${kindLabel}</span>
            ${sensHTML ? '<span class="msep">·</span>' + sensHTML : ''}
            <span class="msep">·</span>
            <span class="mtime" title="${fullTime}">${relTime(it.createdAt)}</span>
            <span class="msep">·</span>
            <span class="mchars">${charText}</span>
            ${srcHTML}
          </div>
          <div class="card-acts">
            ${showView ? `<button class="ab exp" onclick="doView(${it.id})">${viewHTML}</button>` : ''}
            ${q ? `<button class="ab locate-btn" onclick="doLocate(${it.id})" title="定位到时间线">${locateHTML}</button>` : ''}
            <button class="ab cp" onclick="doCopy(${it.id},this)">${cpHTML}</button>
            <button class="ab star${it.starred ? ' on' : ''}" onclick="doStar(${it.id},this)" title="${starTitle}">${starHTML}</button>
            <button class="ab group-btn" onclick="openGroupModal('assign',{itemId:${it.id}})" title="加入分组">${groupBtnHTML}</button>
            <button class="ab del" onclick="doDelete(${it.id},this.closest('article'))" title="删除">${delHTML}</button>
          </div>
        </div>
      </article>`;
    }).join('');

    arr.forEach(it => {
      if (isNonTextKind(it.kind || 'text')) checkFiles(it.id);
    });

    if (pendingLocateId) {
      const el = LIST.querySelector(`article[data-id="${pendingLocateId}"]`);
      if (el) {
        el.classList.add('locate-highlight');
        el.scrollIntoView({ behavior: 'smooth', block: 'center' });
        setTimeout(() => el.classList.remove('locate-highlight'), 3500);
      }
      pendingLocateId = 0;
    }
  }

  // ── Locate (jump to item in full timeline) ───────────────
  async function doLocate(id) {
    try {
      const d = await fetch('/api/items/position?id=' + id).then(r => r.json());
      if (d.error) { toast(d.error, 'err'); return; }
      const pageSize = parseInt(PAGESIZE.value) || 50;
      pendingLocateId = id;
      locateActive = true;
      currentPage = Math.floor(d.position / pageSize);
      Q.value = '';
      clearTimeout(debounce);
      currentKind = '';
      currentGroupID = 0;
      filterDateFrom = '';
      filterDateTo = '';
      document.querySelectorAll('.filter-chip').forEach(c => c.classList.remove('active'));
      document.querySelector('.filter-chip[data-kind=""]').classList.add('active');
      const df = document.getElementById('dateFrom'); const dt = document.getElementById('dateTo');
      if (df) df.value = ''; if (dt) dt.value = '';
      const cdb = document.getElementById('clearDateBtn'); if (cdb) cdb.classList.add('hidden');
      await search();
      locateActive = false;
      clearTimeout(debounce);
    } catch (_) { toast('定位失败', 'err'); }
  }
  window.doLocate = doLocate;

  // ── Search ────────────────────────────────────────────────
  let searchRevision = 0;
  let searchController = null;
  function resetSearch() { if (locateActive) return; currentPage = 0; search(); }

  async function search() {
    const revision = ++searchRevision;
    if (searchController) searchController.abort();
    const controller = new AbortController();
    searchController = controller;
    const q = Q.value;
    const pageSize = parseInt(PAGESIZE.value) || 50;
    try {
      const offset = currentPage * pageSize;
      const p = new URLSearchParams({ q, limit: pageSize, offset });
      let url = '/api/search';
      if (currentKind === '__starred__') {
        p.set('starred', '1');
      } else if (currentKind === '__sensitive__') {
        url = '/api/search/sensitive';
      } else if (currentKind && currentKind !== '__group__') {
        p.set('kind', currentKind);
      }
      if (currentGroupID > 0) {
        p.set('group_id', currentGroupID);
      }
      if (filterDateFrom) p.set('date_from', filterDateFrom);
      if (filterDateTo)   p.set('date_to',   filterDateTo);
      if (url === '/api/search' || url === '/api/search/sensitive') {
        currentSourceExes.forEach(n => p.append('source_exe', n));
      }
      const request = url === '/api/search/sensitive' ? secondaryFetch : fetch;
      const response = await request(url + '?' + p, { signal: controller.signal });
      const d = await response.json();
      if (revision !== searchRevision) return;
      if (d.code === 'secondary_required') {
        const ok = await requestSecondaryAuth();
        if (revision !== searchRevision) return;
        if (ok) { await search(); return; }
        toast('需要二次验证才能查看敏感数据', 'err');
        return;
      }
      if (!response.ok) {
        toast(d.error || '搜索失败，请重试', 'err');
        return;
      }
      totalCount = d.total || 0;
      fullTotalCount = (d.fullTotal !== undefined) ? d.fullTotal : totalCount;
      render(d.items || [], q);
      updatePagination();
    } catch (e) {
      if (e && e.name === 'AbortError') return;
    }
  }

  // ── 来源应用（exe）下拉 ─────────────────────────────────
  async function loadSourceExes() {
    try {
      const data = await fetch('/api/source-exes').then(r => r.json());
      allSourceExes = Array.isArray(data) ? data : [];
    } catch (_) {
      allSourceExes = [];
    }
    renderSrcExeDropdown();
  }

  function renderSrcExeDropdown() {
    const wrap = document.getElementById('srcExeDropdown');
    const list = document.getElementById('srcExeList');
    const counter = document.getElementById('srcExeCount');
    const filterInput = document.getElementById('srcExeFilter');
    if (!wrap || !list) return;
    const cfg = currentCfg || {};
    const recordingOn = cfg.recordSourceInfo !== false;
    if (!recordingOn) {
      wrap.classList.add('disabled');
      list.innerHTML = '<p class="src-exe-empty">来源采集已关闭</p>';
      if (counter) counter.textContent = '';
      return;
    }
    wrap.classList.remove('disabled');
    const srcCfg = cfg.source || {};
    const whitelist = srcCfg.exeWhitelist || [];
    const showAll = !whitelist.length;
    let items = showAll ? allSourceExes.slice() : allSourceExes.filter(e => whitelist.includes(e.name));
    const q = (filterInput && filterInput.value || '').trim().toLowerCase();
    if (q) {
      items = items.filter(e => {
        const label = (sourceAliases[e.name] || e.name).toLowerCase();
        return label.includes(q) || e.name.toLowerCase().includes(q);
      });
    }
    if (!items.length) {
      list.innerHTML = '<p class="src-exe-empty">无可筛选来源应用</p>';
    } else {
      list.innerHTML = items.map(e => {
        const label = sourceAliases[e.name] || e.name;
        const checked = currentSourceExes.includes(e.name);
        return `<label class="src-exe-opt"><input type="checkbox" value="${esc(e.name)}"${checked ? ' checked' : ''}><span class="src-exe-name">${esc(label)}</span><span class="src-exe-cnt">${e.count}</span></label>`;
      }).join('');
    }
    if (counter) counter.textContent = currentSourceExes.length ? '(' + currentSourceExes.length + ')' : '';
  }

  function setupSrcExeUI() {
    const btn = document.getElementById('srcExeBtn');
    const pop = document.getElementById('srcExePop');
    const list = document.getElementById('srcExeList');
    const filterInput = document.getElementById('srcExeFilter');
    if (btn && pop) {
      btn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        pop.hidden = !pop.hidden;
      });
      document.addEventListener('click', (e) => {
        if (pop.hidden) return;
        if (!pop.contains(e.target) && e.target !== btn && !btn.contains(e.target)) {
          pop.hidden = true;
        }
      });
    }
    if (filterInput) {
      filterInput.addEventListener('input', () => renderSrcExeDropdown());
    }
    if (list) {
      list.addEventListener('change', (e) => {
        const cb = e.target.closest('input[type="checkbox"]');
        if (!cb) return;
        const name = cb.value;
        if (cb.checked) {
          if (!currentSourceExes.includes(name)) currentSourceExes.push(name);
        } else {
          currentSourceExes = currentSourceExes.filter(n => n !== name);
        }
        renderSrcExeDropdown();
        search();
      });
    }
    const rsi = document.getElementById('cfg-recordSourceInfo');
    if (rsi) rsi.addEventListener('change', () => renderSrcExeDropdown());
  }

  function updatePagination() {
    const pageSize = parseInt(PAGESIZE.value) || 50;
    const PAG  = document.getElementById('pagination');
    const PREV = document.getElementById('prevBtn');
    const NEXT = document.getElementById('nextBtn');
    const PI   = document.getElementById('pageInfo');
    const JI   = document.getElementById('pageJumpInput');
    const totalPages = Math.ceil(totalCount / pageSize) || 1;
    const start = totalCount === 0 ? 0 : currentPage * pageSize + 1;
    const end   = Math.min((currentPage + 1) * pageSize, totalCount);
    PI.textContent = `${start}–${end} / 共 ${totalCount} 条`;
    PREV.disabled = currentPage === 0;
    NEXT.disabled = end >= totalCount;
    PAG.classList.toggle('hidden', totalCount === 0);
    // 未激活时 badge 始终显示全量总数（转化钩子），已激活时跟随当前结果
    BADGE.textContent = (!licenseState.licensed && fullTotalCount > 0) ? fullTotalCount : totalCount;
    // 限额未知（freeLimit 为 0）时整条提示隐藏，绝不渲染"仅显示最近 0 条"。
    const freeNote = (!licenseState.licensed && licenseState.freeLimit > 0 && fullTotalCount > totalCount)
      ? ' · 免费版仅显示最近 ' + licenseState.freeLimit + ' 条，激活后查看全部'
      : '';
    RC.textContent = totalCount + ' 条结果' + freeNote;
    if (JI) { JI.value = currentPage + 1; JI.max = totalPages; }
  }

  // ── Auto-refresh ──────────────────────────────────────────
  function setupAR() {
    clearInterval(arTimer);
    if (AR.checked) arTimer = setInterval(() => { search(); refreshState(); }, 3000);
  }

  // ── Events ────────────────────────────────────────────────
  Q.addEventListener('input', () => { clearTimeout(debounce); debounce = setTimeout(resetSearch, 180); });
  PAGESIZE.addEventListener('change', () => {
    let v = parseInt(PAGESIZE.value);
    if (isNaN(v) || v < 10) v = 10;
    if (v > 500) v = 500;
    PAGESIZE.value = v;
    resetSearch();
  });
  PAGESIZE.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); PAGESIZE.blur(); }
  });
  AR.addEventListener('change', setupAR);

  // ── Pagesize custom dropdown ─────────────────────────────
  const psDropdown = document.getElementById('pagesizeDropdown');
  const psDropdownBtn = document.getElementById('pagesizeDropdownBtn');
  psDropdownBtn.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    psDropdown.classList.toggle('open');
  });
  psDropdown.addEventListener('click', (e) => {
    const opt = e.target.closest('.pagesize-opt');
    if (!opt) return;
    PAGESIZE.value = opt.dataset.val;
    psDropdown.classList.remove('open');
    resetSearch();
  });
  document.addEventListener('click', () => { psDropdown.classList.remove('open'); });

  // ── Pagination buttons ─────────────────────────────────
  document.getElementById('prevBtn').addEventListener('click', () => {
    if (currentPage > 0) { currentPage--; search(); }
  });
  document.getElementById('nextBtn').addEventListener('click', () => {
    const pageSize = parseInt(PAGESIZE.value) || 50;
    if ((currentPage + 1) * pageSize < totalCount) { currentPage++; search(); }
  });

  // ── Page jump ────────────────────────────────────────────
  function doPageJump() {
    const pageSize = parseInt(PAGESIZE.value) || 50;
    const totalPages = Math.ceil(totalCount / pageSize) || 1;
    const ji = document.getElementById('pageJumpInput');
    let p = parseInt(ji.value);
    if (isNaN(p) || p < 1) p = 1;
    if (p > totalPages) p = totalPages;
    const newPage = p - 1;
    if (newPage === currentPage) return;
    currentPage = newPage;
    search();
  }
  document.getElementById('pageJumpBtn').addEventListener('click', doPageJump);
  document.getElementById('pageJumpInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); doPageJump(); }
  });

  // ── Date filter ───────────────────────────────────────
  const DATE_FROM  = document.getElementById('dateFrom');
  const DATE_TO    = document.getElementById('dateTo');
  const CLEAR_DATE = document.getElementById('clearDateBtn');

  function syncDateClear() {
    CLEAR_DATE.classList.toggle('hidden', !filterDateFrom && !filterDateTo);
  }
  DATE_FROM.addEventListener('change', () => {
    filterDateFrom = DATE_FROM.value;
    syncDateClear();
    resetSearch();
  });
  DATE_TO.addEventListener('change', () => {
    filterDateTo = DATE_TO.value;
    syncDateClear();
    resetSearch();
  });
  CLEAR_DATE.addEventListener('click', () => {
    DATE_FROM.value = '';
    DATE_TO.value   = '';
    filterDateFrom  = '';
    filterDateTo    = '';
    CLEAR_DATE.classList.add('hidden');
    resetSearch();
  });
  TOG.addEventListener('click', async () => {
    await fetch('/api/pause', { method: 'POST' });
    await refreshState();
  });

  // ── Clear all ─────────────────────────────────────────────
  let clearTimer = 0;
  const CLEAR_BTN = document.getElementById('clearBtn');
  const CLEAR_TX  = document.getElementById('clearTx');
  CLEAR_BTN.addEventListener('click', async () => {
    if (clearTimer) {
      clearTimeout(clearTimer);
      clearTimer = 0;
      CLEAR_BTN.classList.add('loading');
      CLEAR_TX.textContent = '清除中...';
      try {
        const ok = await requestSecondaryAuth();
        if (!ok) { throw new Error('secondary_required'); }
        const res = await secondaryFetch('/api/clear', { method: 'POST' });
        if (res.status === 403) { throw new Error('secondary_required'); }
        const d = await res.json();
        if (d.ok) {
          toast('已清除 ' + d.deleted + ' 条记录');
          items.clear();
          BADGE.textContent = '0';
          RC.textContent = '0 条结果';
          LIST.innerHTML = `<div class="empty">
            <svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2">
              <path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/>
              <rect x="9" y="3" width="6" height="4" rx="1"/>
              <line x1="9" y1="12" x2="15" y2="12"/><line x1="9" y1="16" x2="13" y2="16"/>
            </svg>
            <span>暂无剪切板记录</span>
          </div>`;
        } else { throw 0; }
      } catch (e) {
        if (e && e.message === 'secondary_required') {
          toast('需要二次验证才能清除记录', 'err');
        } else {
          toast('清除失败', 'err');
        }
      }
      CLEAR_BTN.classList.remove('loading');
      CLEAR_BTN.classList.remove('confirm');
      CLEAR_TX.textContent = '清除';
      return;
    }
    CLEAR_BTN.classList.add('confirm');
    CLEAR_TX.textContent = '确认清除？';
    clearTimer = setTimeout(() => {
      clearTimer = 0;
      CLEAR_BTN.classList.remove('confirm');
      CLEAR_TX.textContent = '清除';
    }, 3000);
  });

  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      if (!document.getElementById('sourceAppsModal')?.classList.contains('hidden')) {
        closeSourceAppsDialog();
        return;
      }
      if (document.getElementById('groupModalOverlay')) {
        closeGroupModal();
        return;
      }
      if (!VIEW_MODAL.classList.contains('hidden')) {
        window.closeModal();
        return;
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.key === 'k') { e.preventDefault(); Q.focus(); Q.select(); }
    if (e.key === 'Escape' && document.activeElement === Q) { Q.value = ''; search(); Q.blur(); }
  });

  // ── Settings sidebar nav ───────────────────────────────────
  window.snavTo = function(sectionId, navId) {
    document.querySelectorAll('.snav-item').forEach(el => el.classList.remove('active'));
    const navEl = document.getElementById(navId);
    if (navEl) navEl.classList.add('active');
    const sec = document.getElementById(sectionId);
    if (sec) sec.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  // ── Settings ──────────────────────────────────────────────
  let currentCfg = null;
  let configRevision = 0;
  let configRequestSequence = 0;
  const assetOperationTail = { logo: Promise.resolve(), favicon: Promise.resolve(), background: Promise.resolve() };

  function replaceCurrentConfig(config) {
    configRevision++;
    currentCfg = config;
  }

  function updateCurrentConfig(update) {
    replaceCurrentConfig({ ...currentCfg, ...update });
  }

  async function reserveAssetOperation(kind) {
    const previous = assetOperationTail[kind];
    let complete;
    assetOperationTail[kind] = new Promise(resolve => { complete = resolve; });
    await previous;
    return complete;
  }

  const THEME_PALETTES = {
    mint: {
      light: { p: '#14B8A6', p2: '#0D9488', accent: '#F97316', bg: '#F0FDFB', bg2: '#E6F9F5', card: '#FFFFFF', border: 'rgba(20,184,166,.18)', tx: '#0F2825', sub: '#3A6863', muted: '#5C8C87' },
      dark:  { p: '#2DD4BF', p2: '#14B8A6', accent: '#FB923C', bg: '#070C14', bg2: '#0C1520', card: '#101820', border: 'rgba(45,212,191,.14)', tx: '#E8F6F3', sub: '#9AD3CC', muted: '#70AAA4' },
    },
    ocean: {
      light: { p: '#0284C7', p2: '#0369A1', accent: '#2563EB', bg: '#F0F9FF', bg2: '#E0F2FE', card: '#FFFFFF', border: 'rgba(2,132,199,.18)', tx: '#102A43', sub: '#3D5A75', muted: '#6B849C' },
      dark:  { p: '#38BDF8', p2: '#0EA5E9', accent: '#60A5FA', bg: '#07111F', bg2: '#0B1B30', card: '#102038', border: 'rgba(56,189,248,.16)', tx: '#E8F4FF', sub: '#A7C7E7', muted: '#7FA5CB' },
    },
    sunrise: {
      light: { p: '#EA580C', p2: '#C2410C', accent: '#D97706', bg: '#FFF7ED', bg2: '#FFEDD5', card: '#FFFFFF', border: 'rgba(234,88,12,.18)', tx: '#342012', sub: '#72513B', muted: '#99735B' },
      dark:  { p: '#FB923C', p2: '#F97316', accent: '#FBBF24', bg: '#16100B', bg2: '#24170E', card: '#2B1C10', border: 'rgba(251,146,60,.16)', tx: '#FFF2E5', sub: '#E6B58A', muted: '#B98D6B' },
    },
    graphite: {
      light: { p: '#475569', p2: '#334155', accent: '#0F766E', bg: '#F8FAFC', bg2: '#EEF2F6', card: '#FFFFFF', border: 'rgba(71,85,105,.18)', tx: '#1E293B', sub: '#475569', muted: '#748196' },
      dark:  { p: '#94A3B8', p2: '#64748B', accent: '#2DD4BF', bg: '#0B1018', bg2: '#151D28', card: '#182230', border: 'rgba(148,163,184,.16)', tx: '#EDF2F7', sub: '#B5C0CF', muted: '#8A9AAD' },
    },
  };

  const { validHex, hexToRgba, blendHex, ensureContrastOnSurfaces, textColorFor } = window.ClipVaultTheme;

  function resolvedThemeMode(theme) {
    if (theme && theme.mode === 'light') return 'light';
    if (theme && theme.mode === 'dark') return 'dark';
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function readablePalette(palette, mode) {
    const isDark = mode === 'dark';
    const surfaces = [palette.card, palette.bg, palette.bg2];
    const primary = ensureContrastOnSurfaces(palette.p, surfaces, 4.5, [.12]);
    const primaryHover = ensureContrastOnSurfaces(palette.p2, surfaces, 4.5, [.12]);
    const accent = ensureContrastOnSurfaces(palette.accent, surfaces, 4.5, [.1, .16]);
    return {
      ...palette,
      p: primary,
      p2: primaryHover,
      accent,
      border: hexToRgba(primary, isDark ? .18 : .2),
    };
  }

  function customPalette(theme, mode) {
    const isDark = mode === 'dark';
    const neutral = THEME_PALETTES.graphite[mode];
    const surfaces = [neutral.card, neutral.bg, neutral.bg2];
    const primary = ensureContrastOnSurfaces(validHex(theme.primary, '#14B8A6'), surfaces, 4.5, [.12]);
    const accent = ensureContrastOnSurfaces(validHex(theme.accent, '#F97316'), surfaces, 4.5, [.1, .16]);
    // Keep neutral surfaces fixed for each mode. A user-provided background
    // cannot guarantee text contrast, so custom skins only alter brand colors.
    return {
      ...neutral,
      p: primary,
      p2: ensureContrastOnSurfaces(blendHex(primary, isDark ? '#FFFFFF' : '#000000', .12), surfaces, 4.5, [.12]),
      accent,
      border: hexToRgba(primary, isDark ? .18 : .2),
    };
  }

  function applyTheme(theme) {
    const normalized = theme || { preset: 'mint', mode: 'system' };
    const mode = resolvedThemeMode(normalized);
    const preset = THEME_PALETTES[normalized.preset];
    const palette = normalized.preset === 'custom'
      ? customPalette(normalized, mode)
      : (preset || THEME_PALETTES.mint)[mode];
    const colors = normalized.preset === 'custom' ? palette : readablePalette(palette, mode);
    const root = document.documentElement;
    root.dataset.themeMode = mode;
    root.style.setProperty('--p', colors.p);
    root.style.setProperty('--p2', colors.p2);
    root.style.setProperty('--on-p', textColorFor(colors.p));
    root.style.setProperty('--on-p2', textColorFor(colors.p2));
    root.style.setProperty('--accent', colors.accent);
    root.style.setProperty('--bg', colors.bg);
    root.style.setProperty('--bg2', colors.bg2);
    root.style.setProperty('--card', colors.card);
    root.style.setProperty('--card-border', colors.border);
    const surfaceOpacity = Number(normalized.surfaceOpacity);
    const surfaceAlpha = (Number.isFinite(surfaceOpacity) ? Math.max(0, Math.min(100, surfaceOpacity)) : 100) / 100;
    root.style.setProperty('--card-rgba', hexToRgba(colors.card, surfaceAlpha));
    root.style.setProperty('--nav-bg', hexToRgba(colors.bg, surfaceAlpha));
    root.style.setProperty('--tx', colors.tx);
    root.style.setProperty('--sub', colors.sub);
    root.style.setProperty('--muted', colors.muted);
    root.style.setProperty('--p-dim', hexToRgba(colors.p, .12));
    root.style.setProperty('--p-glow', hexToRgba(colors.p, .18));
    root.style.setProperty('--p-border', hexToRgba(colors.p, .22));
    root.style.setProperty('--accent-dim', hexToRgba(colors.accent, .1));
    root.style.setProperty('--accent-glow', hexToRgba(colors.accent, .16));
    root.style.setProperty('--accent-border', hexToRgba(colors.accent, .3));
	root.style.setProperty('--font-scale', `${Math.max(100, Math.min(150, Number(normalized.fontScale) || 100)) / 100}`);
    root.style.setProperty('--shadow-md', `0 4px 16px ${hexToRgba(colors.p, .1)}, 0 2px 6px rgba(0,0,0,.05)`);
    root.style.setProperty('--shadow-lg', `0 8px 32px ${hexToRgba(colors.p, .15)}, 0 3px 10px rgba(0,0,0,.08)`);
  }

  async function loadPublicAppearance() {
    try {
      const appearance = await fetch('/api/appearance', { cache: 'no-store' }).then(r => r.ok ? r.json() : null);
      if (!appearance) return;
      applyTheme(appearance.theme);
      applyBrand(appearance.brand);
      applyBackground(appearance.background);
    } catch (_) {}
  }

  function brandAssetURL(kind, version) {
    return Number(version) > 0 ? `/brand/${kind}?v=${encodeURIComponent(version)}` : '';
  }

  function applyBrand(brand) {
    const value = brand || {};
    const title = String(value.title || 'ClipVault').trim() || 'ClipVault';
    document.title = title;
    const name = document.getElementById('brandName');
    if (name) name.textContent = title;

    const logoURL = brandAssetURL('logo', value.logoVersion);
    const logo = document.getElementById('brandLogo');
    const defaultLogo = document.getElementById('brandDefaultLogo');
    if (logo) {
      if (logoURL) {
        logo.src = logoURL;
        logo.classList.remove('hidden');
        defaultLogo?.classList.add('hidden');
      } else {
        logo.removeAttribute('src');
        logo.classList.add('hidden');
        defaultLogo?.classList.remove('hidden');
      }
    }

    const lockTitle = document.getElementById('lockTitle');
    if (lockTitle) lockTitle.textContent = `${title} 锁屏`;
    const lockLogo = document.getElementById('lockBrandLogo');
    const lockDefaultLogo = document.getElementById('lockDefaultLogo');
    if (lockLogo) {
      if (logoURL) {
        lockLogo.src = logoURL;
        lockLogo.classList.remove('hidden');
        lockDefaultLogo?.classList.add('hidden');
      } else {
        lockLogo.removeAttribute('src');
        lockLogo.classList.add('hidden');
        lockDefaultLogo?.classList.remove('hidden');
      }
    }

    const favicon = document.getElementById('brandFavicon');
    if (favicon) favicon.href = Number(value.faviconVersion) > 0
      ? `/favicon.ico?v=${encodeURIComponent(value.faviconVersion)}`
      : '/favicon.ico';
    const logoPreview = document.getElementById('brandLogoPreview');
    const faviconPreview = document.getElementById('brandFaviconPreview');
    if (logoPreview) {
      if (logoURL) logoPreview.src = logoURL;
      else logoPreview.removeAttribute('src');
    }
    const faviconURL = Number(value.faviconVersion) > 0 ? `/favicon.ico?v=${encodeURIComponent(value.faviconVersion)}` : '';
    if (faviconPreview) {
      if (faviconURL) faviconPreview.src = faviconURL;
      else faviconPreview.removeAttribute('src');
    }
  }

  function applyBrandFormState(brand) {
    const title = document.getElementById('cfg-brandTitle');
    if (title) title.value = brand?.title || 'ClipVault';
    applyBrand(brand);
  }

  async function saveBrandAsset(kind, file) {
    if (!file) return;
    const limits = { logo: 10 * 1024 * 1024, favicon: 10 * 1024 * 1024 };
    if (file.size > limits[kind]) {
      toast('文件不能超过 10 MB', 'err');
      return;
    }
    const accepted = kind === 'logo' ? ['image/png', 'image/jpeg'] : ['image/png', 'image/x-icon', 'image/vnd.microsoft.icon'];
    if (!accepted.includes(file.type)) {
      toast(kind === 'logo' ? 'Logo 仅支持 PNG、JPEG' : 'favicon 仅支持 PNG、ICO', 'err');
      return;
    }
    const complete = await reserveAssetOperation(kind);
    try {
      let res = await secondaryFetch(`/api/brand/${kind}`, {
        method: 'PUT',
        headers: { 'Content-Type': file.type },
        body: file,
      });
      if (res.status === 403) {
        if (!await requestSecondaryAuth()) return;
        res = await secondaryFetch(`/api/brand/${kind}`, {
          method: 'PUT',
          headers: { 'Content-Type': file.type },
          body: file,
        });
      }
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      updateCurrentConfig({ brand: data.brand });
      applyBrandFormState(data.brand);
      if (data.runtimeError || data.hotkeyError) {
        toast('素材已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      toast(kind === 'logo' ? 'Logo 已更新' : 'favicon 已更新');
    } catch (error) {
      toast('上传失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  }

  async function resetBrandAsset(kind) {
    const complete = await reserveAssetOperation(kind);
    try {
      let res = await secondaryFetch(`/api/brand/${kind}`, { method: 'DELETE' });
      if (res.status === 403) {
        if (!await requestSecondaryAuth()) return;
        res = await secondaryFetch(`/api/brand/${kind}`, { method: 'DELETE' });
      }
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      updateCurrentConfig({ brand: data.brand });
      applyBrandFormState(data.brand);
      if (data.runtimeError || data.hotkeyError) {
        toast('设置已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      toast(kind === 'logo' ? '已还原默认 Logo' : '已还原默认 favicon');
    } catch (error) {
      toast('还原失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  }

  const BACKGROUND_DEFAULTS = {
    opacity: 58, overlay: 64, blur: 0, brightness: 100, contrast: 100,
    saturation: 100, scale: 100, positionX: 50, positionY: 50, fit: 'cover',
  };

  function backgroundValue(background) {
    return { ...BACKGROUND_DEFAULTS, ...(background || {}) };
  }

  function backgroundMediaURL(version) {
    return Number(version) > 0 ? `/background/media?v=${encodeURIComponent(version)}` : '';
  }

  function updateBackgroundOutputs(value) {
    const units = {
      'cfg-backgroundOpacity': '%', 'cfg-backgroundOverlay': '%', 'cfg-backgroundBlur': 'px',
      'cfg-backgroundBrightness': '%', 'cfg-backgroundContrast': '%', 'cfg-backgroundSaturation': '%',
      'cfg-backgroundScale': '%', 'cfg-backgroundPositionX': '%', 'cfg-backgroundPositionY': '%',
    };
    Object.entries(units).forEach(([id, unit]) => {
      const output = document.querySelector(`[data-background-output="${id}"]`);
      const input = document.getElementById(id);
      if (output && input) output.value = `${input.value}${unit}`;
    });
  }

  function applyBackground(background) {
    const value = backgroundValue(background);
    const contentType = String(value.contentType || '');
    const url = backgroundMediaURL(value.version);
    const isImage = contentType.startsWith('image/');
    const isVideo = contentType.startsWith('video/');
    const active = !!url && (isImage || isVideo);
    const root = document.documentElement;
    root.style.setProperty('--background-opacity', `${Math.max(0, Math.min(100, Number(value.opacity))) / 100}`);
    root.style.setProperty('--background-overlay', `${Math.max(0, Math.min(90, Number(value.overlay))) / 100}`);
    root.style.setProperty('--background-filter', `blur(${Math.max(0, Number(value.blur))}px) brightness(${Math.max(40, Number(value.brightness))}%) contrast(${Math.max(40, Number(value.contrast))}%) saturate(${Math.max(0, Number(value.saturation))}%)`);
    root.style.setProperty('--background-scale', `${Math.max(25, Math.min(200, Number(value.scale))) / 100}`);
    root.style.setProperty('--background-position', `${Math.max(0, Math.min(100, Number(value.positionX)))}% ${Math.max(0, Math.min(100, Number(value.positionY)))}%`);
	root.style.setProperty('--background-fit', ['cover', 'contain', 'fill'].includes(value.fit) ? value.fit : 'cover');

    const layer = document.getElementById('backgroundMediaLayer');
    const shield = document.getElementById('backgroundShield');
    const image = document.getElementById('backgroundImage');
    const video = document.getElementById('backgroundVideo');
    document.body.classList.toggle('has-background', active);
    layer?.classList.toggle('hidden', !active);
    shield?.classList.toggle('hidden', !active);
    if (isImage && image) {
      image.src = url;
      video?.pause();
      video?.removeAttribute('src');
      video?.load();
    } else if (isVideo && video) {
      image?.removeAttribute('src');
      video.src = url;
      video.play().catch(() => {});
    } else {
      image?.removeAttribute('src');
      video?.pause();
      video?.removeAttribute('src');
      video?.load();
    }
  }

  function backgroundFromForm() {
    const value = backgroundValue(currentCfg?.background);
    const controls = {
      opacity: 'cfg-backgroundOpacity', overlay: 'cfg-backgroundOverlay', blur: 'cfg-backgroundBlur',
      brightness: 'cfg-backgroundBrightness', contrast: 'cfg-backgroundContrast', saturation: 'cfg-backgroundSaturation',
      scale: 'cfg-backgroundScale', positionX: 'cfg-backgroundPositionX', positionY: 'cfg-backgroundPositionY',
    };
    Object.entries(controls).forEach(([key, id]) => {
      const input = document.getElementById(id);
      if (input) value[key] = Number(input.value);
    });
	const fit = document.getElementById('cfg-backgroundFit');
	if (fit) value.fit = fit.value;
    return value;
  }

  function applyBackgroundFormState(background) {
    const value = backgroundValue(background);
    const controls = {
      opacity: 'cfg-backgroundOpacity', overlay: 'cfg-backgroundOverlay', blur: 'cfg-backgroundBlur',
      brightness: 'cfg-backgroundBrightness', contrast: 'cfg-backgroundContrast', saturation: 'cfg-backgroundSaturation',
      scale: 'cfg-backgroundScale', positionX: 'cfg-backgroundPositionX', positionY: 'cfg-backgroundPositionY',
    };
    Object.entries(controls).forEach(([key, id]) => {
      const input = document.getElementById(id);
      if (input) input.value = value[key];
    });
	const fit = document.getElementById('cfg-backgroundFit');
	if (fit) fit.value = value.fit;
    updateBackgroundOutputs(value);
    applyBackground(value);
  }

  async function saveBackgroundMedia(file) {
    if (!file) return;
    if (file.size > 10 * 1024 * 1024) {
      toast('背景文件不能超过 10 MB', 'err');
      return;
    }
    const complete = await reserveAssetOperation('background');
    try {
	  let res = await secondaryFetch('/api/background/media', { method: 'PUT', headers: { 'Content-Type': file.type || 'application/octet-stream' }, body: file });
      if (res.status === 403) {
        if (!await requestSecondaryAuth()) return;
		res = await secondaryFetch('/api/background/media', { method: 'PUT', headers: { 'Content-Type': file.type || 'application/octet-stream' }, body: file });
      }
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      updateCurrentConfig({ background: data.background });
      applyBackgroundFormState(data.background);
      if (data.runtimeError || data.hotkeyError) {
        toast('背景已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      toast('背景已更新');
    } catch (error) {
      toast('背景上传失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  }

  async function resetBackgroundMedia() {
    const complete = await reserveAssetOperation('background');
    try {
      let res = await secondaryFetch('/api/background/media', { method: 'DELETE' });
      if (res.status === 403) {
        if (!await requestSecondaryAuth()) return;
        res = await secondaryFetch('/api/background/media', { method: 'DELETE' });
      }
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      updateCurrentConfig({ background: data.background });
      applyBackgroundFormState(data.background);
      if (data.runtimeError || data.hotkeyError) {
        toast('设置已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      toast('背景已移除');
    } catch (error) {
      toast('移除背景失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  }

  function themeFromForm() {
    const selected = document.querySelector('.theme-preset.active');
    return {
      preset: selected ? selected.dataset.themePreset : (currentCfg?.theme?.preset || 'mint'),
      mode: document.getElementById('cfg-themeMode')?.value || 'system',
      primary: document.getElementById('cfg-themePrimary')?.value || '#14B8A6',
      accent: document.getElementById('cfg-themeAccent')?.value || '#F97316',
	  fontScale: Number(document.getElementById('cfg-themeFontScale')?.value) || 100,
	  surfaceOpacity: Number(document.getElementById('cfg-themeSurfaceOpacity')?.value),
    };
  }

  function applyThemeFormState(theme) {
    const value = theme || { preset: 'mint', mode: 'system' };
    document.querySelectorAll('.theme-preset').forEach(button => {
      const selected = button.dataset.themePreset === value.preset;
      button.classList.toggle('active', selected);
      button.setAttribute('aria-checked', selected ? 'true' : 'false');
    });
    const mode = document.getElementById('cfg-themeMode');
    if (mode) mode.value = value.mode || 'system';
    const primary = document.getElementById('cfg-themePrimary');
    const accent = document.getElementById('cfg-themeAccent');
    if (primary) primary.value = validHex(value.primary, '#14B8A6');
    if (accent) accent.value = validHex(value.accent, '#F97316');
	const fontScale = document.getElementById('cfg-themeFontScale');
	const fontScaleOutput = document.getElementById('cfg-themeFontScaleOutput');
	if (fontScale) fontScale.value = Math.max(100, Math.min(150, Number(value.fontScale) || 100));
	if (fontScaleOutput) fontScaleOutput.value = `${fontScale?.value || 100}%`;
	const surfaceOpacityEl = document.getElementById('cfg-themeSurfaceOpacity');
	const surfaceOpacityOutput = document.getElementById('cfg-themeSurfaceOpacityOutput');
	const surfaceOpacityRaw = Number(value.surfaceOpacity);
	const surfaceOpacityValue = Number.isFinite(surfaceOpacityRaw) ? Math.max(0, Math.min(100, surfaceOpacityRaw)) : 100;
	if (surfaceOpacityEl) surfaceOpacityEl.value = surfaceOpacityValue;
	if (surfaceOpacityOutput) surfaceOpacityOutput.value = `${surfaceOpacityValue}%`;
    document.getElementById('customThemeColors')?.classList.toggle('hidden', value.preset !== 'custom');
    applyTheme(value.preset === 'custom'
      ? themeFromForm()
      : { preset: value.preset || 'mint', mode: value.mode || 'system', fontScale: Number(value.fontScale) || 100, surfaceOpacity: surfaceOpacityValue });
  }

  async function loadTheme() {
    const request = ++configRequestSequence;
    const revision = configRevision;
    try {
      const cfg = await fetch('/api/config').then(r => r.json());
      if (request !== configRequestSequence || revision !== configRevision) return;
      replaceCurrentConfig(cfg);
      applyTheme(cfg.theme);
	  applyBrand(cfg.brand);
	  applyBackground(cfg.background);
    } catch (_) {
      if (request !== configRequestSequence || revision !== configRevision) return;
      applyTheme();
	  applyBrand();
	  applyBackground();
    }
  }

  if (window.matchMedia) {
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
      if (currentCfg?.theme?.mode === 'system') applyTheme(currentCfg.theme);
    });
  }

  async function loadSettings() {
    const request = ++configRequestSequence;
    const revision = configRevision;
    try {
      const cfg = await fetch('/api/config').then(r => r.json());
      if (request === configRequestSequence && revision === configRevision && !hasPendingConfigSave()) {
        replaceCurrentConfig(cfg);
        try { await loadSourceExes(); } catch (_) {}
        applyConfigToForm(cfg);
      }
    } catch (_) {
      if (request === configRequestSequence && revision === configRevision && !hasPendingConfigSave()) {
        toast('加载配置失败', 'err');
      }
    }
    if (request !== configRequestSequence) return;
    loadRecoveryStatus();
    loadStorageStatus();
  }

  async function loadStorageStatus() {
    const request = ++storageStatusRequestSequence;
    const currentDir = document.getElementById('storageCurrentDir');
    const hint = document.getElementById('storageCurrentHint');
    const openBtn = document.getElementById('storageOpenDirBtn');
    const button = document.getElementById('storageRelocateBtn');
    if (!currentDir || !hint || !button) return;
    try {
      const res = await fetch('/api/storage/status');
      if (!res.ok) throw new Error('status');
      const status = await res.json();
      if (request !== storageStatusRequestSequence || storageRelocationInFlight) return;
      currentDir.textContent = status.dataDir || '-';
      currentDir.title = status.dataDir || '';
      if (openBtn) openBtn.disabled = !status.dataDir;
      if (status.movable && !status.pending && !storageRelocationScheduled) {
        hint.textContent = '数据目录迁移会保留数据库、密钥和应用数据；运行日志保留在原位置。目标目录必须预先创建、为空且可读写。';
        button.disabled = false;
      } else if (status.movable) {
        hint.textContent = '数据目录迁移已准备完成，请退出应用后重新启动。';
        button.disabled = true;
      } else {
        hint.textContent = status.reason || '当前运行模式不支持修改数据目录。';
        button.disabled = true;
      }
    } catch (_) {
      if (request !== storageStatusRequestSequence || storageRelocationInFlight) return;
      hint.textContent = '无法读取存储状态。';
      button.disabled = true;
      if (openBtn) openBtn.disabled = true;
    }
  }

  async function openDataDirectory() {
    const btn = document.getElementById('storageOpenDirBtn');
    if (!btn || btn.disabled) return;
    btn.disabled = true;
    try {
      const res = await fetch('/api/storage/open', { method: 'POST' });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) { toast(data.error || '无法打开数据目录', 'err'); return; }
      toast('已在文件资源管理器中打开');
    } catch (_) {
      toast('无法打开数据目录', 'err');
    } finally {
      btn.disabled = false;
    }
  }

  window.openMigrateDialog = function() {
    if (storageRelocationInFlight || storageRelocationScheduled) return;
    const currentDirText = document.getElementById('storageCurrentDir')?.textContent || '-';
    const html = `
      <div class="modal-overlay" id="migrateOverlay">
        <div class="modal-box secondary-box migrate-modal" onclick="event.stopPropagation()">
          <div class="modal-head">
            <span class="modal-title">迁移数据目录</span>
            <button class="modal-close" onclick="closeMigrateDialog()">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body" style="height:auto;padding:24px">
            <div class="migrate-notes">
              <p>迁移将复制数据库、密钥和应用数据到新目录；运行日志保留在原位置。目标目录必须由你提前创建、为空且具备读写权限。迁移计划提交后，请从托盘退出应用并重新启动，程序才会在启动时迁移现有数据。</p>
            </div>
            <div class="migrate-field">
              <label class="migrate-label">当前数据目录</label>
              <code class="migrate-dir" id="migrateOldDir"></code>
            </div>
            <div class="migrate-field">
              <label class="migrate-label" for="migrateNewDir">新数据目录</label>
              <input type="text" id="migrateNewDir" class="field-input" autocomplete="off" placeholder="例如 D:\\ClipVaultData">
            </div>
            <div class="migrate-progress hidden" id="migrateProgress"></div>
            <p class="lock-err hidden" id="migrateErr"></p>
            <div class="migrate-actions">
              <button type="button" class="btn-export" id="migrateCancelBtn">取消</button>
              <button type="button" class="lock-btn" id="migrateExecBtn">执行迁移</button>
            </div>
          </div>
        </div>
      </div>
    `;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('migrateOverlay');
    const execBtn = document.getElementById('migrateExecBtn');
    const cancelBtn = document.getElementById('migrateCancelBtn');
    const newDirInput = document.getElementById('migrateNewDir');
    const errEl = document.getElementById('migrateErr');
    const progressEl = document.getElementById('migrateProgress');
    const oldDirEl = document.getElementById('migrateOldDir');
    oldDirEl.textContent = currentDirText;

    window.closeMigrateDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', (e) => { if (e.target === overlay) closeMigrateDialog(); });
    cancelBtn.addEventListener('click', closeMigrateDialog);
    setTimeout(() => newDirInput.focus(), 50);

    const steps = [
      { label: '验证目标目录' },
      { label: '提交迁移计划' },
      { label: '完成' },
    ];

    function renderProgress(states) {
      progressEl.classList.remove('hidden');
      progressEl.innerHTML = steps.map((s, i) => {
        const st = states[i] || 'pending';
        const icon = st === 'done' ? '\u2713' : st === 'active' ? '\u25CF' : st === 'error' ? '\u2715' : '\u25CB';
        return '<div class="migrate-step ms-' + st + '"><span class="ms-icon">' + icon + '</span><span class="ms-label">' + s.label + '</span></div>';
      }).join('');
    }

    execBtn.addEventListener('click', async () => {
      const target = newDirInput.value.trim();
      errEl.classList.add('hidden');
      if (!target) { errEl.textContent = '请输入已创建的新数据目录'; errEl.classList.remove('hidden'); return; }

      execBtn.disabled = true;
      newDirInput.disabled = true;
      cancelBtn.disabled = true;
      const states = ['active', 'pending', 'pending'];
      renderProgress(states);

      // Step 0: validate target
      states[0] = 'done'; states[1] = 'active';
      renderProgress(states);

      // Step 1: submit (includes secondary auth + POST, retry on 403)
      storageRelocationInFlight = true;
      let scheduled = false;
      try {
        for (let attempt = 0; attempt < 2; attempt++) {
          const ok = await requestSecondaryAuth(attempt > 0);
          if (!ok) break;
          const res = await secondaryFetch('/api/storage/relocate', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ dataDir: target }),
          });
          if (res.status === 403) continue;
          const data = await res.json();
          if (!res.ok) throw new Error(data.error || '数据目录迁移准备失败');
          scheduled = true;
          break;
        }
        if (!scheduled) throw new Error('需要二次验证才能迁移数据目录');

        states[1] = 'done'; states[2] = 'active';
        renderProgress(states);
        states[2] = 'done';
        renderProgress(states);

        progressEl.insertAdjacentHTML('beforeend',
          '<div class="migrate-success">\u2713 迁移计划已提交。请从托盘退出应用并重新启动，程序将在启动时迁移数据。</div>');
        execBtn.textContent = '关闭';
        execBtn.disabled = false;
        execBtn.onclick = closeMigrateDialog;
        cancelBtn.style.display = 'none';
        storageRelocationScheduled = true;
        newDirInput.value = '';
      } catch (e) {
        states[1] = 'error';
        renderProgress(states);
        errEl.textContent = '迁移准备失败：' + (e.message || e);
        errEl.classList.remove('hidden');
        execBtn.disabled = false;
        newDirInput.disabled = false;
        cancelBtn.disabled = false;
      } finally {
        storageRelocationInFlight = false;
        void loadStorageStatus();
      }
    });
  };

  function isLoopbackListenAddress(addr) {
    const value = String(addr || '').trim();
    let host = '';
    if (value.startsWith('[')) {
      const end = value.indexOf(']');
      host = end > 0 ? value.slice(1, end) : '';
    } else {
      const colon = value.lastIndexOf(':');
      host = colon > 0 ? value.slice(0, colon) : '';
    }
    host = host.trim().toLowerCase();
    return host === 'localhost' || host === '::1' || /^127(?:\.\d{1,3}){3}$/.test(host);
  }

  function updateListenAddressWarning() {
    const input = document.getElementById('cfg-listenAddr');
    const warning = document.getElementById('listenAddrWarning');
    const proxyEnabled = document.getElementById('cfg-reverseProxyEnabled')?.checked || false;
    const title = document.getElementById('listenSecurityWarningTitle');
    const text = document.getElementById('listenSecurityWarningText');
    if (!input || !warning) return;
    const directLAN = !isLoopbackListenAddress(input.value);
    warning.classList.toggle('hidden', !directLAN && !proxyEnabled);
    if (proxyEnabled) {
      if (title) title.textContent = 'HTTPS 反向代理模式';
      if (text) text.textContent = 'ClipVault 只信任来自本机回环连接且与外部 HTTPS 地址完全匹配的转发头。后端监听地址必须保持为 127.0.0.1 或其他回环地址。';
    } else {
      if (title) title.textContent = '局域网访问风险';
      if (text) text.textContent = '非回环地址会通过明文 HTTP 暴露剪贴板服务。保存前必须设置锁屏密码；建议仅在可信网络中使用，并检查 Windows 防火墙规则。';
    }
  }

  function applyConfigToForm(cfg) {
    applyBrandFormState(cfg.brand);
	applyBackgroundFormState(cfg.background);
    applyThemeFormState(cfg.theme);
    document.getElementById('cfg-maxTextChars').value = cfg.maxTextChars ?? 0;
    document.getElementById('cfg-maxFileDropPaths').value = cfg.maxFileDropPaths ?? 0;
    const rsi = document.getElementById('cfg-recordSourceInfo');
    // 缺省视为开启：与后端 DefaultConfig().RecordSourceInfo = true 对齐
    if (rsi) rsi.checked = cfg.recordSourceInfo !== false;

    // 来源应用配置：ShowExe 缺省视为 true；别名/白名单编辑器依赖 allSourceExes（由 loadSourceExes 填充）
    sourceAliases = cfg.source?.exeAliases || {};
    sourceWhitelist = cfg.source?.exeWhitelist?.length ? cfg.source.exeWhitelist.slice() : null;
    const showExeEl = document.getElementById('cfg-sourceShowExe');
    if (showExeEl) showExeEl.checked = cfg.source?.showExe !== false;
    updateSourceAppsSummary();

    document.getElementById('cfg-listenAddr').value = (cfg.search && cfg.search.listenAddr) || '127.0.0.1:18765';
    const reverseProxy = cfg.search?.reverseProxy || {};
    const reverseProxyEnabled = document.getElementById('cfg-reverseProxyEnabled');
    const reverseProxyPublicUrl = document.getElementById('cfg-reverseProxyPublicUrl');
    if (reverseProxyEnabled) reverseProxyEnabled.checked = !!reverseProxy.enabled;
    if (reverseProxyPublicUrl) reverseProxyPublicUrl.value = reverseProxy.publicUrl || '';
    const timeZoneEl = document.getElementById('cfg-timeZone');
    if (timeZoneEl) timeZoneEl.value = (cfg.search && cfg.search.timeZone) || 'system';
    updateListenAddressWarning();

    // 全局快捷键
    const hk = cfg.hotkey || {};
    const hkEnabled = document.getElementById('cfg-hotkeyEnabled');
    const hkKey = document.getElementById('cfg-hotkeyKey');
    if (hkEnabled) hkEnabled.checked = !!hk.enabled;
    const mods = hk.modifiers || 0;
    document.querySelectorAll('#hotkeyMods .mod-btn input').forEach(cb => {
      cb.checked = (mods & parseInt(cb.value)) !== 0;
    });
    if (hkKey) hkKey.value = vkToDisplay(hk.key);

    // 搜索框快捷键
    const shk = cfg.searchHotkey || {};
    const shkEnabled = document.getElementById('cfg-searchHotkeyEnabled');
    const shkKey = document.getElementById('cfg-searchHotkeyKey');
    if (shkEnabled) shkEnabled.checked = !!shk.enabled;
    const smods = shk.modifiers || 0;
    document.querySelectorAll('#searchHotkeyMods .mod-btn input').forEach(cb => {
      cb.checked = (smods & parseInt(cb.value)) !== 0;
    });
    if (shkKey) shkKey.value = vkToDisplay(shk.key);

    const pauseResumeNotifications = document.getElementById('cfg-pauseResumeNotifications');
    if (pauseResumeNotifications) {
      pauseResumeNotifications.checked = cfg.notifications.pauseResume;
    }
    const notifications = cfg.notifications;
    const notificationFields = {
      'cfg-pauseNotificationTitle': notifications.pauseTitle || '',
      'cfg-pauseNotificationBody': notifications.pauseBody || '',
      'cfg-resumeNotificationTitle': notifications.resumeTitle || '',
      'cfg-resumeNotificationBody': notifications.resumeBody || '',
    };
    Object.entries(notificationFields).forEach(([id, value]) => {
      const field = document.getElementById(id);
      if (field) field.value = value;
    });

    // 安全超时配置
    const lockTimeoutEl = document.getElementById('cfg-lockSessionTimeout');
    const secTimeoutEl  = document.getElementById('cfg-secondaryAuthTimeout');
    if (lockTimeoutEl) lockTimeoutEl.value = (cfg.security && cfg.security.lockSessionTimeout) || 0;
    if (secTimeoutEl)  secTimeoutEl.value  = (cfg.security && cfg.security.secondaryAuthTimeout) || 0;

    // 检查过滤规则是否被遮蔽
    const patterns = (cfg.sensitive && cfg.sensitive.customPatterns) || [];
    const isMasked = patterns.length > 0 && patterns.every(p => p === '__masked__');
    const gate = document.getElementById('filterAuthGate');
    const content = document.getElementById('filterContent');
    if (isMasked) {
      // 规则被遮蔽，显示验证门
      if (gate) gate.classList.remove('hidden');
      if (content) content.classList.add('hidden');
      renderPatterns([]);
    } else {
      // 已通过二次认证或无规则，显示规则
      if (gate) gate.classList.add('hidden');
      if (content) content.classList.remove('hidden');
      renderPatterns(patterns);
    }

    // 更新密码状态 UI
    updatePwdStatusUI();

    // 备份配置
    const bk = cfg.backup || {};
    const db = bk.dirBackup || {};
    const eb = bk.emailBackup || {};
    const dirEnabled = document.getElementById('cfg-dirBackupEnabled');
    const dirRunOnStartup = document.getElementById('cfg-dirBackupRunOnStartup');
    const dirInterval = document.getElementById('cfg-dirBackupInterval');
    const dirDir = document.getElementById('cfg-dirBackupDir');
    const dirFormat = document.getElementById('cfg-dirBackupFormat');
    const dirPwd = document.getElementById('cfg-dirBackupPwd');
    const dirPwdRow = document.getElementById('cfg-dirBackupPwdRow');
    const dirPwdClearRow = document.getElementById('cfg-dirBackupPwdClearRow');
    const dirPwdClear = document.getElementById('cfg-dirBackupPwdClear');
    if (dirEnabled) dirEnabled.checked = !!db.enabled;
    if (dirRunOnStartup) dirRunOnStartup.checked = !!db.runOnStartup;
    if (dirInterval) dirInterval.value = db.interval || 24;
    if (dirDir) dirDir.value = db.dir || '';
    if (dirFormat) dirFormat.value = db.format || 'cvexport';
    const dirScope = document.getElementById('cfg-dirBackupScope');
    if (dirScope) dirScope.value = (db.scope && db.scope.mode) || 'full';
    const dirIncrFullInterval = document.getElementById('cfg-dirBackupIncrementalFullInterval');
    if (dirIncrFullInterval) dirIncrFullInterval.value = (db.incrementalFullInterval !== undefined && db.incrementalFullInterval !== null) ? db.incrementalFullInterval : 7;
    if (dirPwd) dirPwd.value = '';
    if (dirPwdClear) dirPwdClear.checked = false;
    const dirHasPwd = db.password === '__set__';
    if (dirPwdClearRow) dirPwdClearRow.style.display = dirHasPwd ? '' : 'none';
    if (dirPwdRow) dirPwdRow.style.display = (db.format === 'cvexport') ? '' : 'none';

    const emailEnabled = document.getElementById('cfg-emailBackupEnabled');
    const emailRunOnStartup = document.getElementById('cfg-emailBackupRunOnStartup');
    const emailInterval = document.getElementById('cfg-emailBackupInterval');
    const emailHost = document.getElementById('cfg-emailBackupSMTPHost');
    const emailPort = document.getElementById('cfg-emailBackupSMTPPort');
    const emailUser = document.getElementById('cfg-emailBackupSMTPUser');
    const emailPass = document.getElementById('cfg-emailBackupSMTPPass');
    const emailFrom = document.getElementById('cfg-emailBackupFrom');
    const emailTo = document.getElementById('cfg-emailBackupTo');
    const emailFormat = document.getElementById('cfg-emailBackupFormat');
    const emailPwd = document.getElementById('cfg-emailBackupPwd');
    const emailPwdRow = document.getElementById('cfg-emailBackupPwdRow');
    const emailPwdClearRow = document.getElementById('cfg-emailBackupPwdClearRow');
    const emailPwdClear = document.getElementById('cfg-emailBackupPwdClear');
    const emailSubject = document.getElementById('cfg-emailBackupSubject');
    const emailBody = document.getElementById('cfg-emailBackupBody');
    const emailStyle = document.getElementById('cfg-emailBackupStyle');
    const emailAccentColor = document.getElementById('cfg-emailBackupAccentColor');
    if (emailEnabled) emailEnabled.checked = !!eb.enabled;
    if (emailRunOnStartup) emailRunOnStartup.checked = !!eb.runOnStartup;
    if (emailInterval) emailInterval.value = eb.interval || 24;
    if (emailHost) emailHost.value = eb.smtpHost || '';
    if (emailPort) emailPort.value = eb.smtpPort || 587;
    if (emailUser) emailUser.value = eb.smtpUser || '';
    if (emailPass) emailPass.value = '';
    if (emailFrom) emailFrom.value = eb.from || '';
    if (emailTo) emailTo.value = eb.to || '';
    if (emailFormat) emailFormat.value = eb.format || 'cvexport';
    const emailScope = document.getElementById('cfg-emailBackupScope');
    if (emailScope) emailScope.value = (eb.scope && eb.scope.mode) || 'full';
    const emailMaxPartSize = document.getElementById('cfg-emailBackupMaxPartSize');
    if (emailMaxPartSize) {
      emailMaxPartSize.value = Math.round(eb.maxPartSize / (1024 * 1024));
    }
    const emailIncrFullInterval = document.getElementById('cfg-emailBackupIncrementalFullInterval');
    if (emailIncrFullInterval) emailIncrFullInterval.value = (eb.incrementalFullInterval !== undefined && eb.incrementalFullInterval !== null) ? eb.incrementalFullInterval : 7;
    if (emailSubject) emailSubject.value = eb.subject || '';
    if (emailBody) emailBody.value = eb.body || '';
    if (emailStyle) emailStyle.value = eb.style || 'card';
    if (emailAccentColor) emailAccentColor.value = eb.accentColor || '#16A34A';
    if (emailPwd) emailPwd.value = '';
    if (emailPwdClear) emailPwdClear.checked = false;
    const emailHasPwd = eb.password === '__set__';
    if (emailPwdClearRow) emailPwdClearRow.style.display = emailHasPwd ? '' : 'none';
    if (emailPwdRow) emailPwdRow.style.display = (eb.format === 'cvexport') ? '' : 'none';

    // 加载备份状态
    loadBackupStatus();
  }

  // ── 来源应用管理弹窗 ─────────────────────────────────────
  function updateSourceAppsSummary() {
    const summary = document.getElementById('sourceAppsSummary');
    if (!summary) return;
    const aliasCount = Object.keys(sourceAliases || {}).length;
    const scope = sourceWhitelist?.length ? `下拉显示 ${sourceWhitelist.length} 个应用` : '下拉显示全部应用';
    summary.textContent = `${scope}；已设置 ${aliasCount} 个别名`;
  }

  function renderSourceAppsDialog() {
    const wrap = document.getElementById('srcAliases');
    if (!wrap || !sourceAppsDraft) return;
    if (!allSourceExes.length) {
      wrap.innerHTML = '<p class="src-empty-hint">暂无已记录的来源应用</p>';
      updateSourceAppsSelectionHint();
      return;
    }
    const query = document.getElementById('sourceAppsFilter')?.value.trim().toLowerCase() || '';
    const visible = allSourceExes.filter(e => {
      const alias = sourceAppsDraft.aliases[e.name] || '';
      return !query || e.name.toLowerCase().includes(query) || alias.toLowerCase().includes(query);
    });
    const selectedMode = sourceAppsDraft.mode === 'selected';
    const selected = new Set(selectedMode
      ? (sourceAppsDraft.whitelist || [])
      : allSourceExes.map(e => e.name));
    wrap.innerHTML = visible.map(e => {
      const custom = sourceAppsDraft.aliases[e.name] || '';
      return `<div class="src-alias-row" data-raw="${esc(e.name)}">
        <label class="src-app-check" title="控制是否出现在搜索下拉"><input type="checkbox"${selected.has(e.name) ? ' checked' : ''}${selectedMode ? '' : ' disabled'} aria-label="${esc(e.name)} 出现在搜索下拉"></label>
        <span class="src-alias-raw" title="${esc(e.name)}">${esc(e.name)}</span>
        <input type="text" class="src-alias-input" maxlength="40" placeholder="别名（可选）" value="${esc(custom)}">
      </div>`;
    }).join('');
    if (!visible.length) wrap.innerHTML = '<p class="src-empty-hint">没有匹配的来源应用</p>';
    updateSourceAppsSelectionHint();
  }

  function updateSourceAppsSelectionHint() {
    const hint = document.getElementById('sourceAppsSelectionHint');
    if (!hint || !sourceAppsDraft) return;
    if (sourceAppsDraft.mode === 'all') {
      hint.textContent = `共 ${allSourceExes.length} 个应用，全部显示在搜索下拉`;
      return;
    }
    const knownNames = new Set(allSourceExes.map(e => e.name));
    const configured = sourceAppsDraft.whitelist || [];
    const selectedKnown = configured.filter(name => knownNames.has(name)).length;
    const unavailable = configured.length - selectedKnown;
    hint.textContent = `已选择 ${selectedKnown} / ${allSourceExes.length} 个当前应用`
      + (unavailable ? `；另保留 ${unavailable} 个历史应用` : '');
  }

  function syncVisibleSourceAppsDraft() {
    if (!sourceAppsDraft) return;
    document.querySelectorAll('#srcAliases .src-alias-row').forEach(row => {
      const raw = row.dataset.raw;
      const input = row.querySelector('.src-alias-input');
      const checkbox = row.querySelector('.src-app-check input');
      if (!raw) return;
      const alias = input?.value.trim() || '';
      if (alias) sourceAppsDraft.aliases[raw] = alias;
      else delete sourceAppsDraft.aliases[raw];
      if (sourceAppsDraft.mode === 'selected' && checkbox) {
        const selected = new Set(sourceAppsDraft.whitelist || []);
        if (checkbox.checked) selected.add(raw); else selected.delete(raw);
        const knownNames = new Set(allSourceExes.map(e => e.name));
        const unavailableConfigured = (sourceAppsDraft.whitelist || []).filter(name => !knownNames.has(name));
        sourceAppsDraft.whitelist = allSourceExes.map(e => e.name).filter(name => selected.has(name)).concat(unavailableConfigured);
      }
    });
  }

  window.openSourceAppsDialog = function() {
    const existing = sourceWhitelist?.length ? sourceWhitelist.slice() : allSourceExes.map(e => e.name);
    sourceAppsDraft = {
      aliases: { ...sourceAliases },
      whitelist: existing,
      mode: sourceWhitelist?.length ? 'selected' : 'all',
    };
    const filter = document.getElementById('sourceAppsFilter');
    if (filter) filter.value = '';
    document.querySelectorAll('input[name="sourceAppsMode"]').forEach(radio => {
      radio.checked = radio.value === sourceAppsDraft.mode;
    });
    renderSourceAppsDialog();
    document.getElementById('sourceAppsModal')?.classList.remove('hidden');
    setTimeout(() => filter?.focus(), 50);
  };

  window.closeSourceAppsDialog = function() {
    document.getElementById('sourceAppsModal')?.classList.add('hidden');
    sourceAppsDraft = null;
  };

  window.saveSourceAppsDialog = async function() {
    if (!sourceAppsDraft) return;
    syncVisibleSourceAppsDraft();
    const knownNames = new Set(allSourceExes.map(e => e.name));
    const selectedKnown = (sourceAppsDraft.whitelist || []).filter(name => knownNames.has(name));
    if (sourceAppsDraft.mode === 'selected' && selectedKnown.length === 0) {
      toast('“仅显示选中”模式下请至少选择一个应用', 'err');
      return;
    }
    const previousAliases = sourceAliases;
    const previousWhitelist = sourceWhitelist;
    sourceAliases = { ...sourceAppsDraft.aliases };
    sourceWhitelist = sourceAppsDraft.mode === 'all' ? null : sourceAppsDraft.whitelist.slice();
    const saveButton = document.getElementById('sourceAppsSaveBtn');
    if (saveButton) saveButton.disabled = true;
    pendingConfigRevision++;
    clearTimeout(saveTimer);
    saveTimer = 0;
    const saved = await saveConfig();
    if (!saved) {
      sourceAliases = previousAliases;
      sourceWhitelist = previousWhitelist;
      if (saveButton) saveButton.disabled = false;
      return;
    }
    updateSourceAppsSummary();
    renderSrcExeDropdown();
    closeSourceAppsDialog();
  };

  document.getElementById('sourceAppsFilter')?.addEventListener('input', () => {
    syncVisibleSourceAppsDraft();
    renderSourceAppsDialog();
  });
  document.querySelectorAll('input[name="sourceAppsMode"]').forEach(radio => {
    radio.addEventListener('change', () => {
      if (!sourceAppsDraft || !radio.checked) return;
      syncVisibleSourceAppsDraft();
      sourceAppsDraft.mode = radio.value;
      if (radio.value === 'selected' && !sourceAppsDraft.whitelist?.length) {
        sourceAppsDraft.whitelist = allSourceExes.map(e => e.name);
      }
      renderSourceAppsDialog();
    });
  });
  document.getElementById('srcAliases')?.addEventListener('change', () => {
    syncVisibleSourceAppsDraft();
    updateSourceAppsSelectionHint();
  });

  // ── Unlock filter rules (secondary auth) ────────────────
  const unlockFilterBtn = document.getElementById('unlockFilterBtn');
  if (unlockFilterBtn) {
    unlockFilterBtn.addEventListener('click', async () => {
      const ok = await requestSecondaryAuth();
      if (!ok) { toast('需要二次验证才能查看过滤规则', 'err'); return; }
      try {
        const d = await secondaryFetch('/api/sensitive/rules').then(r => r.json());
        if (d.code === 'secondary_required') {
          toast('二次验证失败', 'err');
          return;
        }
        const patterns = d.customPatterns || [];
        document.getElementById('filterAuthGate').classList.add('hidden');
        document.getElementById('filterContent').classList.remove('hidden');
        renderPatterns(patterns);
        toast('已解锁敏感过滤规则');
      } catch (_) { toast('加载过滤规则失败', 'err'); }
    });
  }
  // ── Pattern list ──────────────────────────────────────────
  function renderPatterns(patterns) {
    const list = document.getElementById('patternList');
    if (!patterns.length) {
      list.innerHTML = '<p class="no-patterns">暂无过滤规则，所有内容均会被记录</p>';
      return;
    }
    list.innerHTML = patterns.map((p, i) => `
      <div class="pattern-row" data-idx="${i}">
        <input type="text" class="pattern-input" value="${esc(p)}" placeholder="正则表达式">
        <button class="pattern-del" onclick="removePattern(${i})" title="删除">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
    `).join('');
  }

  window.removePattern = function(idx) {
    const patterns = getPatterns();
    patterns.splice(idx, 1);
    renderPatterns(patterns);
    scheduleConfigSave(300);
  };

  function getPatterns() {
    return Array.from(document.querySelectorAll('.pattern-input'))
      .map(i => i.value.trim())
      .filter(Boolean);
  }

  document.getElementById('addPatternBtn').addEventListener('click', () => {
    const patterns = getPatterns();
    patterns.push('');
    renderPatterns(patterns);
    // 聚焦最后一个输入框
    const inputs = document.querySelectorAll('.pattern-input');
    if (inputs.length) inputs[inputs.length - 1].focus();
  });

  // ── Auto-save settings ───────────────────────────────────
  const RESTART_OVERLAY  = document.getElementById('restartOverlay');
  const RESTART_CONFIRM  = document.getElementById('restartConfirm');
  const RESTART_CANCEL   = document.getElementById('restartCancelBtn');
  const RESTART_CONFIRM_BTN = document.getElementById('restartConfirmBtn');
  let saveTimer = 0;
  let saveInFlight = false;
  let activeConfigSave = null;
  let pendingConfigRevision = 0;
  let savedConfigRevision = 0;
  let storageRelocationInFlight = false;
  let storageRelocationScheduled = false;
  let storageStatusRequestSequence = 0;
  let diagnosticsExportInFlight = false;
  let autoSaveBound = false;

  function hasPendingConfigSave() {
    return pendingConfigRevision !== savedConfigRevision || saveInFlight;
  }

	const { vkToDisplay, parseVkInput } = window.ClipVaultHotkey;

  let pendingRestart = false;

  function collectConfig() {
    if (!currentCfg) return null;
    // 当过滤规则被遮蔽（auth gate 可见）时，保留原配置中的 patterns，避免用空数组覆盖
    const gate = document.getElementById('filterAuthGate');
    const patternsMasked = gate && !gate.classList.contains('hidden');
    const patterns = patternsMasked
      ? (currentCfg.sensitive && currentCfg.sensitive.customPatterns) || []
      : getPatterns();
    const lockTimeout = parseInt(document.getElementById('cfg-lockSessionTimeout')?.value) || 0;
    const secTimeout  = parseInt(document.getElementById('cfg-secondaryAuthTimeout')?.value) || 0;
    const hkEnabled = document.getElementById('cfg-hotkeyEnabled')?.checked || false;
    let hkMods = 0;
    document.querySelectorAll('#hotkeyMods .mod-btn input').forEach(cb => {
      if (cb.checked) hkMods |= parseInt(cb.value);
    });
	const hkKeyChar = parseVkInput(document.getElementById('cfg-hotkeyKey')?.value);
	if (hkKeyChar === null) {
	  toast('全局快捷键主键仅支持 A–Z、0–9、F1–F24 或 Space', 'err');
	  return null;
	}
    if (hkEnabled && (hkMods === 0 || hkKeyChar === 0)) {
      toast('启用快捷键时需选择至少一个修饰键并输入一个按键', 'err');
      return null;
    }
    const shkEnabled = document.getElementById('cfg-searchHotkeyEnabled')?.checked || false;
    let shkMods = 0;
    document.querySelectorAll('#searchHotkeyMods .mod-btn input').forEach(cb => {
      if (cb.checked) shkMods |= parseInt(cb.value);
    });
	const shkKeyChar = parseVkInput(document.getElementById('cfg-searchHotkeyKey')?.value);
	if (shkKeyChar === null) {
	  toast('搜索框快捷键主键仅支持 A–Z、0–9、F1–F24 或 Space', 'err');
	  return null;
	}
    if (shkEnabled && (shkMods === 0 || shkKeyChar === 0)) {
      toast('启用搜索框快捷键时需选择至少一个修饰键并输入一个按键', 'err');
      return null;
    }
    // 防止两个快捷键完全相同
    if (hkEnabled && shkEnabled && hkMods === shkMods && hkKeyChar === shkKeyChar) {
      toast('搜索框快捷键不能与全局快捷键相同', 'err');
      return null;
    }
    return {
      ...currentCfg,
      brand: {
        ...currentCfg.brand,
        title: document.getElementById('cfg-brandTitle')?.value.trim() || 'ClipVault',
      },
	  background: backgroundFromForm(),
      theme: themeFromForm(),
      maxTextChars:     parseInt(document.getElementById('cfg-maxTextChars').value) || 0,
      maxFileDropPaths: parseInt(document.getElementById('cfg-maxFileDropPaths').value) || 0,
      recordSourceInfo: !!(document.getElementById('cfg-recordSourceInfo')?.checked),
      source: {
        // 展开 currentCfg.source：未来新增字段在前端未渲染时也能保留，不会被覆盖成 undefined
        ...currentCfg.source,
        showExe: document.getElementById('cfg-sourceShowExe')?.checked !== false ? true : false,
        exeAliases: sourceAliases,
        exeWhitelist: sourceWhitelist,
      },
      sensitive: { customPatterns: patterns },
      search: {
        ...currentCfg.search,
        listenAddr: document.getElementById('cfg-listenAddr').value.trim() || '127.0.0.1:18765',
        timeZone: document.getElementById('cfg-timeZone')?.value.trim() || 'system',
        reverseProxy: {
          enabled: document.getElementById('cfg-reverseProxyEnabled')?.checked || false,
          publicUrl: document.getElementById('cfg-reverseProxyPublicUrl')?.value.trim() || '',
        },
      },
      security: {
        ...currentCfg.security,
        lockSessionTimeout:   lockTimeout,
        secondaryAuthTimeout: secTimeout,
      },
      hotkey: {
        enabled:   hkEnabled,
        modifiers: hkMods,
        key:       hkKeyChar,
      },
      searchHotkey: {
        enabled:   shkEnabled,
        modifiers: shkMods,
        key:       shkKeyChar,
      },
      notifications: {
        ...currentCfg.notifications,
        pauseResume: document.getElementById('cfg-pauseResumeNotifications')?.checked || false,
        pauseTitle: document.getElementById('cfg-pauseNotificationTitle')?.value.trim() || '',
        pauseBody: document.getElementById('cfg-pauseNotificationBody')?.value.trim() || '',
        resumeTitle: document.getElementById('cfg-resumeNotificationTitle')?.value.trim() || '',
        resumeBody: document.getElementById('cfg-resumeNotificationBody')?.value.trim() || '',
      },
      backup: {
        ...currentCfg.backup,
        dirBackup: {
          ...currentCfg.backup?.dirBackup,
          enabled:      document.getElementById('cfg-dirBackupEnabled')?.checked || false,
          runOnStartup: document.getElementById('cfg-dirBackupRunOnStartup')?.checked || false,
          interval:     parseInt(document.getElementById('cfg-dirBackupInterval')?.value) || 24,
          dir:          document.getElementById('cfg-dirBackupDir')?.value.trim() || '',
          format:       document.getElementById('cfg-dirBackupFormat')?.value || 'cvexport',
          password:     backupPwdValue('cfg-dirBackupPwd', 'cfg-dirBackupPwdClear'),
          scope:        { mode: document.getElementById('cfg-dirBackupScope')?.value || 'full', from: '', to: '' },
          incrementalFullInterval: (v => isNaN(v) ? 7 : v)(parseInt(document.getElementById('cfg-dirBackupIncrementalFullInterval')?.value)),
        },
        emailBackup: {
          ...currentCfg.backup?.emailBackup,
          enabled:      document.getElementById('cfg-emailBackupEnabled')?.checked || false,
          runOnStartup: document.getElementById('cfg-emailBackupRunOnStartup')?.checked || false,
          interval:     parseInt(document.getElementById('cfg-emailBackupInterval')?.value) || 24,
          smtpHost:     document.getElementById('cfg-emailBackupSMTPHost')?.value.trim() || '',
          smtpPort:     parseInt(document.getElementById('cfg-emailBackupSMTPPort')?.value) || 587,
          smtpUser:     document.getElementById('cfg-emailBackupSMTPUser')?.value.trim() || '',
          smtpPass:     smtpPasswordValue(),
          from:         document.getElementById('cfg-emailBackupFrom')?.value.trim() || '',
          to:           document.getElementById('cfg-emailBackupTo')?.value.trim() || '',
          format:       document.getElementById('cfg-emailBackupFormat')?.value || 'cvexport',
          password:     backupPwdValue('cfg-emailBackupPwd', 'cfg-emailBackupPwdClear'),
          subject:      document.getElementById('cfg-emailBackupSubject')?.value || '',
          body:         document.getElementById('cfg-emailBackupBody')?.value || '',
          style:        document.getElementById('cfg-emailBackupStyle')?.value || '',
          accentColor:  document.getElementById('cfg-emailBackupAccentColor')?.value || '',
          scope:        { mode: document.getElementById('cfg-emailBackupScope')?.value || 'full', from: '', to: '' },
          maxPartSize:  Number(document.getElementById('cfg-emailBackupMaxPartSize')?.value) * 1024 * 1024,
          incrementalFullInterval: (v => isNaN(v) ? 7 : v)(parseInt(document.getElementById('cfg-emailBackupIncrementalFullInterval')?.value)),
        },
      },
    };
  }

  function scheduleConfigSave(delay) {
    pendingConfigRevision++;
    clearTimeout(saveTimer);
    if (delay <= 0) {
      saveTimer = 0;
      void saveConfig();
      return;
    }
    saveTimer = setTimeout(() => {
      saveTimer = 0;
      void saveConfig();
    }, delay);
  }

  async function flushPendingConfigSave() {
    clearTimeout(saveTimer);
    saveTimer = 0;
    while (hasPendingConfigSave()) {
      if (!await saveConfig()) return false;
    }
    return true;
  }

  async function saveConfig() {
    if (activeConfigSave) return activeConfigSave;
    saveInFlight = true;
    activeConfigSave = (async () => {
      for (;;) {
        const revision = pendingConfigRevision;
        if (!await saveConfigNow()) return false;
        if (revision === pendingConfigRevision) {
          savedConfigRevision = revision;
          return true;
        }
        clearTimeout(saveTimer);
        saveTimer = 0;
      }
    })();
    try {
      return await activeConfigSave;
    } finally {
      activeConfigSave = null;
      saveInFlight = false;
    }
  }

  async function saveConfigNow(secondaryRetried = false) {
    const newCfg = collectConfig();
    if (!newCfg) return false;
    const emailMaxPartSizeMB = newCfg.backup.emailBackup.maxPartSize / (1024 * 1024);
    if (!Number.isInteger(emailMaxPartSizeMB) || emailMaxPartSizeMB < 1 || emailMaxPartSizeMB > 50) {
      toast('单封邮件上限必须是 1–50MB 的整数', 'err');
      return false;
    }

    // 敏感规则以服务端 Go RE2 为唯一语法标准。不要在浏览器中用
    // JavaScript RegExp 预校验；两种引擎的语法不同（例如 Go 支持 (?i)）。

    // 启用定时备份时，先在前端校验配置完整性，避免发送必然失败请求造成"切格式不生效"的错觉。
    if (newCfg.backup?.dirBackup?.enabled) {
      const missing = validateDirBackupConfig();
      if (missing.length) {
        toast('目录备份配置不完整：' + missing.join('、'), 'err');
        return false;
      }
    }
    if (newCfg.backup?.emailBackup?.enabled) {
      const missing = validateEmailBackupConfig();
      if (missing.length) {
        toast('邮件备份配置不完整：' + missing.join('、'), 'err');
        return false;
      }
    }

    const previousSearch = currentCfg.search || {};
    const previousProxy = previousSearch.reverseProxy || {};
    const nextProxy = newCfg.search.reverseProxy;
    const addrChanged = previousSearch.listenAddr !== newCfg.search.listenAddr;
    const proxyChanged = !!previousProxy.enabled !== nextProxy.enabled
      || (previousProxy.publicUrl || '') !== nextProxy.publicUrl;
    const openingLAN = addrChanged
      && isLoopbackListenAddress(previousSearch.listenAddr)
      && !isLoopbackListenAddress(newCfg.search.listenAddr);
    const enablingProxy = !previousProxy.enabled && nextProxy.enabled;

    if (nextProxy.enabled && !isLoopbackListenAddress(newCfg.search.listenAddr)) {
      toast('启用 HTTPS 反向代理时，Web 监听地址必须是回环地址', 'err');
      return false;
    }
    if (nextProxy.enabled || nextProxy.publicUrl) {
      try {
        const publicURL = new URL(nextProxy.publicUrl);
        const rootPath = publicURL.pathname === '' || publicURL.pathname === '/';
        if (publicURL.protocol !== 'https:' || publicURL.username || publicURL.password
          || !publicURL.hostname || !rootPath || publicURL.search || publicURL.hash) {
          throw new Error('invalid public URL');
        }
      } catch (_) {
        toast('外部 HTTPS 地址必须是完整的 https:// 主机地址，不能包含路径、账号、查询参数或片段', 'err');
        return false;
      }
    }

    if (openingLAN || enablingProxy) {
      if (!authState.hasLockPassword) {
        toast('开放远程访问前，请先设置锁屏密码', 'err');
        return false;
      }
      const message = enablingProxy
        ? '安全确认：反向代理必须运行在本机，并将请求转发到当前回环监听地址。ClipVault 只接受与配置地址完全匹配的 HTTPS 转发头。是否继续？'
        : '安全警告：非回环地址会通过明文 HTTP 向同一网络开放 ClipVault。请确认网络可信、防火墙规则正确，并只向可信用户提供锁屏密码。是否继续？';
      if (!window.confirm(message)) {
        document.getElementById('cfg-listenAddr').value = previousSearch.listenAddr;
        document.getElementById('cfg-reverseProxyEnabled').checked = !!previousProxy.enabled;
        document.getElementById('cfg-reverseProxyPublicUrl').value = previousProxy.publicUrl || '';
        updateListenAddressWarning();
        toast('已取消开放远程访问');
        return false;
      }
    }

    try {
      const res = await secondaryFetch('/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newCfg),
      });
      if (!res.ok) {
        const txt = await res.text();
        let failure = {};
        try { failure = JSON.parse(txt); } catch (_) {}
        if (!secondaryRetried && res.status === 403 && failure.code === 'secondary_required') {
          authState.secondaryAuthed = false;
          const ok = await requestSecondaryAuth();
          if (ok) return saveConfigNow(true);
          toast('需要二次验证才能保存配置', 'err');
          return false;
        }
        throw new Error(failure.error || txt || `HTTP ${res.status}`);
      }
      const d = await res.json();
      replaceCurrentConfig(d.config);
      const runtimeWarning = d.runtimeError || d.hotkeyError;
      if (runtimeWarning) {
        toast('设置已保存，但' + runtimeWarning, 'err');
      } else {
        toast('设置已保存');
      }

      if (addrChanged || proxyChanged) {
        showRestartConfirm();
      }
      return true;
    } catch (e) {
			toast('保存失败：' + (e.message || e), 'err');
      return false;
    }
  }

  function showRestartConfirm() {
    RESTART_CONFIRM.classList.remove('hidden');
  }

  function hideRestartConfirm() {
    RESTART_CONFIRM.classList.add('hidden');
  }

  RESTART_CANCEL.addEventListener('click', () => {
    hideRestartConfirm();
    toast('监听地址将在下次重启后生效');
  });

  RESTART_CONFIRM_BTN.addEventListener('click', async () => {
    hideRestartConfirm();
    await doRestart();
  });

  async function doRestart() {
    RESTART_OVERLAY.classList.remove('hidden');
    try {
      const res = await fetch('/api/restart', { method: 'POST' });
      if (!res.ok) {
        RESTART_OVERLAY.classList.add('hidden');
        toast('重启请求失败，请手动重启应用', 'err');
        return;
      }
    } catch (_) {
      // 服务已开始关闭，fetch 可能失败，继续轮询
    }
    // 开始轮询等待服务恢复
    pollRestart();
  }

  function pollRestart() {
    let attempts = 0;
    const maxAttempts = 15;
    const interval = setInterval(async () => {
      attempts++;
      if (attempts > maxAttempts) {
        clearInterval(interval);
        RESTART_OVERLAY.classList.add('hidden');
        toast('重启超时，新页面可能已在新标签页打开', 'err');
        return;
      }
      try {
        const res = await fetch('/api/auth/status');
        if (res.ok) {
          clearInterval(interval);
          // 新页面已由后端 shellExecute 在新标签页打开，仅隐藏遮罩
          RESTART_OVERLAY.classList.add('hidden');
        }
      } catch (_) {
        // 服务尚未恢复，继续轮询
      }
    }, 1000);
  }

  // 自动保存：监听设置区所有输入变化（debounce 800ms）
  function setupAutoSave() {
    const settingsEl = document.getElementById('viewSettings');
    if (!settingsEl) return;
    if (autoSaveBound) return;
    autoSaveBound = true;
    const listenAddrEl = document.getElementById('cfg-listenAddr');
    const reverseProxyEnabledEl = document.getElementById('cfg-reverseProxyEnabled');
    const reverseProxyPublicUrlEl = document.getElementById('cfg-reverseProxyPublicUrl');
    const timeZoneEl = document.getElementById('cfg-timeZone');
    const brandTitleEl = document.getElementById('cfg-brandTitle');
    brandTitleEl?.addEventListener('input', () => applyBrand({ ...currentCfg?.brand, title: brandTitleEl.value }));
    ['logo', 'favicon'].forEach(kind => {
      const fileInput = document.getElementById(`cfg-brand${kind === 'logo' ? 'Logo' : 'Favicon'}File`);
      document.getElementById(`brand${kind === 'logo' ? 'Logo' : 'Favicon'}UploadBtn`)?.addEventListener('click', () => fileInput?.click());
      document.getElementById(`brand${kind === 'logo' ? 'Logo' : 'Favicon'}ResetBtn`)?.addEventListener('click', () => resetBrandAsset(kind));
      fileInput?.addEventListener('change', () => {
        saveBrandAsset(kind, fileInput.files?.[0]);
        fileInput.value = '';
      });
    });
	const backgroundFileInput = document.getElementById('cfg-backgroundMediaFile');
	document.getElementById('backgroundMediaUploadBtn')?.addEventListener('click', () => backgroundFileInput?.click());
	document.getElementById('backgroundMediaResetBtn')?.addEventListener('click', resetBackgroundMedia);
	backgroundFileInput?.addEventListener('change', () => {
	  saveBackgroundMedia(backgroundFileInput.files?.[0]);
	  backgroundFileInput.value = '';
	});
	document.querySelectorAll('[id^="cfg-background"][type="range"]').forEach(control => {
	  control.addEventListener('input', () => {
		updateBackgroundOutputs(backgroundFromForm());
		applyBackground(backgroundFromForm());
	  });
	});
	document.getElementById('cfg-backgroundFit')?.addEventListener('change', () => {
	  applyBackground(backgroundFromForm());
	});
    document.querySelectorAll('.theme-preset').forEach(button => {
      button.addEventListener('click', () => {
        const preset = button.dataset.themePreset;
        document.querySelectorAll('.theme-preset').forEach(item => {
          const selected = item === button;
          item.classList.toggle('active', selected);
          item.setAttribute('aria-checked', selected ? 'true' : 'false');
        });
        document.getElementById('customThemeColors')?.classList.toggle('hidden', preset !== 'custom');
        applyTheme(themeFromForm());
        scheduleConfigSave(150);
      });
    });
    ['cfg-themeMode', 'cfg-themePrimary', 'cfg-themeAccent'].forEach(id => {
      const control = document.getElementById(id);
      if (!control) return;
      control.addEventListener('input', () => applyTheme(themeFromForm()));
      control.addEventListener('change', () => applyTheme(themeFromForm()));
    });
	const fontScale = document.getElementById('cfg-themeFontScale');
	fontScale?.addEventListener('input', () => {
	  const output = document.getElementById('cfg-themeFontScaleOutput');
	  if (output) output.value = `${fontScale.value}%`;
	  applyTheme(themeFromForm());
	});
	const surfaceOpacityControl = document.getElementById('cfg-themeSurfaceOpacity');
	surfaceOpacityControl?.addEventListener('input', () => {
	  const output = document.getElementById('cfg-themeSurfaceOpacityOutput');
	  if (output) output.value = `${surfaceOpacityControl.value}%`;
	  applyTheme(themeFromForm());
	});
    settingsEl.addEventListener('input', (e) => {
      if (e.target === listenAddrEl || e.target === reverseProxyPublicUrlEl || e.target === timeZoneEl || e.target?.type === 'file') return;
      scheduleConfigSave(800);
    });
    settingsEl.addEventListener('change', (e) => {
      if (e.target === listenAddrEl || e.target === reverseProxyPublicUrlEl || e.target === timeZoneEl || e.target?.type === 'file') return;
      scheduleConfigSave(300);
    });
    // 监听地址失焦时立即保存（触发重启确认）
    if (listenAddrEl) {
      listenAddrEl.addEventListener('input', updateListenAddressWarning);
      listenAddrEl.addEventListener('blur', () => {
        scheduleConfigSave(0);
      });
    }
    if (reverseProxyEnabledEl) {
      reverseProxyEnabledEl.addEventListener('change', updateListenAddressWarning);
    }
    if (reverseProxyPublicUrlEl) {
      reverseProxyPublicUrlEl.addEventListener('blur', () => scheduleConfigSave(0));
    }
    if (timeZoneEl) {
      timeZoneEl.addEventListener('blur', () => scheduleConfigSave(0));
    }
    // 过滤规则增删也触发保存
    const addBtn = document.getElementById('addPatternBtn');
    if (addBtn) {
      addBtn.addEventListener('click', () => {
        scheduleConfigSave(300);
      });
    }
    // 备份格式切换时显示/隐藏加密密码行
    const dirFmt = document.getElementById('cfg-dirBackupFormat');
    if (dirFmt) {
      dirFmt.addEventListener('change', () => {
        const row = document.getElementById('cfg-dirBackupPwdRow');
        if (row) row.style.display = (dirFmt.value === 'cvexport') ? '' : 'none';
      });
    }
    const emailFmt = document.getElementById('cfg-emailBackupFormat');
    if (emailFmt) {
      emailFmt.addEventListener('change', () => {
        const row = document.getElementById('cfg-emailBackupPwdRow');
        if (row) row.style.display = (emailFmt.value === 'cvexport') ? '' : 'none';
        if (emailFmt.value === 'cvexport') {
          delete emailFmt.dataset.plainConfirmed;
          return;
        }
        if ((currentCfg?.backup?.emailBackup?.format || 'cvexport') !== 'cvexport') {
          emailFmt.dataset.plainConfirmed = 'true';
          return;
        }
        if (emailFmt.dataset.plainConfirmed === 'true') return;
        const confirmed = window.confirm(
          '明文邮件附件会以可读形式保存在收件箱、邮件服务器和可能的转发副本中。确认使用未加密的备份格式吗？'
        );
        if (confirmed) {
          emailFmt.dataset.plainConfirmed = 'true';
          return;
        }
        emailFmt.value = currentCfg?.backup?.emailBackup?.format || 'cvexport';
        if (row) row.style.display = (emailFmt.value === 'cvexport') ? '' : 'none';
      });
    }
    document.getElementById('storageOpenDirBtn')?.addEventListener('click', openDataDirectory);
    document.getElementById('storageRelocateBtn')?.addEventListener('click', openMigrateDialog);
    const diagnosticsButton = document.getElementById('diagnosticsExportBtn');
    diagnosticsButton?.addEventListener('click', async () => {
      if (diagnosticsExportInFlight) return;
      diagnosticsExportInFlight = true;
      diagnosticsButton.disabled = true;
      try {
        const ok = await authedDownload('/api/diagnostics/export', 'clipvault-diagnostics.zip');
        if (ok) toast('运行日志已导出');
      } finally {
        diagnosticsExportInFlight = false;
        diagnosticsButton.disabled = false;
      }
    });

    const dirEnabledEl = document.getElementById('cfg-dirBackupEnabled');
    if (dirEnabledEl) {
      dirEnabledEl.addEventListener('click', (e) => {
        if (!dirEnabledEl.checked) return;
        const missing = validateDirBackupConfig();
        if (missing.length) {
          e.preventDefault();
          toast('启用目录备份前请先完善配置：' + missing.join('、'), 'err');
        }
      });
    }
    const emailEnabledEl = document.getElementById('cfg-emailBackupEnabled');
    if (emailEnabledEl) {
      emailEnabledEl.addEventListener('click', (e) => {
        if (!emailEnabledEl.checked) return;
        const missing = validateEmailBackupConfig();
        if (missing.length) {
          e.preventDefault();
          toast('启用邮件备份前请先完善配置：' + missing.join('、'), 'err');
        }
      });
    }
  }

  // 根据密码输入框和"清除密码"复选框状态返回要提交的密码值。
  // - 输入框有值：返回该值（设置/修改密码，优先级最高）。
  // - 输入框为空 + 复选框勾选：返回 "__clear__" 告知服务端清除已保存的密码。
  // - 输入框为空且未勾选清除：返回 ""，服务端保留原密码。
  function backupPwdValue(pwdId, clearId) {
    const pwd = document.getElementById(pwdId)?.value || '';
    if (pwd) return pwd;
    const clearEl = document.getElementById(clearId);
    if (clearEl && clearEl.checked) return '__clear__';
    return '';
  }

  // SMTP 用户名和密码同时留空时，明确清除已保存的密码并使用无认证 relay。
  // 填写用户名但留空密码时保留已保存的密码，随后由校验判断是否完整。
  function smtpPasswordValue() {
    const user = (document.getElementById('cfg-emailBackupSMTPUser')?.value || '').trim();
    const password = document.getElementById('cfg-emailBackupSMTPPass')?.value || '';
    if (password) return password;
    if (!user) return '__clear__';
    return '';
  }

  // 校验目录备份配置是否完整，返回缺失项列表（空数组表示通过）。
  // 仅 cvexport 格式需要加密密码；json/xlsx 为明文导出，无需密码。
  function validateDirBackupConfig() {
    const missing = [];
    const interval = parseInt(document.getElementById('cfg-dirBackupInterval')?.value) || 0;
    const dir = (document.getElementById('cfg-dirBackupDir')?.value || '').trim();
    const format = document.getElementById('cfg-dirBackupFormat')?.value || 'cvexport';
    const pwd = document.getElementById('cfg-dirBackupPwd')?.value || '';
    const clearChecked = document.getElementById('cfg-dirBackupPwdClear')?.checked || false;
    const hasExistingPwd = currentCfg?.backup?.dirBackup?.password === '__set__';
    if (interval < 1 || interval > 720) missing.push('备份间隔');
    if (!dir) missing.push('目标目录');
    if (format === 'cvexport') {
      // 有新输入用新密码；无新输入且勾选清除则为空；无新输入未勾选则依赖已存密码
      const effectivePwd = pwd || (clearChecked ? '' : (hasExistingPwd ? '__set__' : ''));
      if (!effectivePwd) missing.push('加密密码');
    }
    return missing;
  }

  // 校验邮件备份配置是否完整，返回缺失项列表（空数组表示通过）。
  // 仅 cvexport 格式需要加密密码；json/xlsx 为明文导出，无需密码。
  function validateEmailBackupConfig() {
    const missing = [];
    const interval = parseInt(document.getElementById('cfg-emailBackupInterval')?.value) || 0;
    const host = (document.getElementById('cfg-emailBackupSMTPHost')?.value || '').trim();
    const port = parseInt(document.getElementById('cfg-emailBackupSMTPPort')?.value) || 0;
    const user = (document.getElementById('cfg-emailBackupSMTPUser')?.value || '').trim();
    const pass = document.getElementById('cfg-emailBackupSMTPPass')?.value || '';
    const from = (document.getElementById('cfg-emailBackupFrom')?.value || '').trim();
    const to = (document.getElementById('cfg-emailBackupTo')?.value || '').trim();
    const format = document.getElementById('cfg-emailBackupFormat')?.value || 'cvexport';
    const pwd = document.getElementById('cfg-emailBackupPwd')?.value || '';
    const clearChecked = document.getElementById('cfg-emailBackupPwdClear')?.checked || false;
    const subject = document.getElementById('cfg-emailBackupSubject')?.value || '';
    const body = document.getElementById('cfg-emailBackupBody')?.value || '';
    const style = document.getElementById('cfg-emailBackupStyle')?.value || '';
    const accentColor = document.getElementById('cfg-emailBackupAccentColor')?.value || '';
    const eb = currentCfg?.backup?.emailBackup || {};
    const hasExistingSmtpPass = eb.smtpPass === '__set__';
    const hasExistingPwd = eb.password === '__set__';
    if (interval < 1 || interval > 720) missing.push('备份间隔');
    if (!host) missing.push('SMTP 服务器');
    if (port <= 0 || port > 65535) missing.push('SMTP 端口');
    const effectiveSmtpPass = pass || (user && hasExistingSmtpPass ? '__set__' : '');
    if (Boolean(user) !== Boolean(effectiveSmtpPass)) {
      missing.push('SMTP 用户名和密码需同时填写');
    }
    if (user && !document.getElementById('cfg-emailBackupSMTPUser')?.validity.valid) {
      missing.push('SMTP 用户名必须为邮箱');
    }
    if (!from || !document.getElementById('cfg-emailBackupFrom')?.validity.valid) missing.push('发件人邮箱');
    if (!to || !document.getElementById('cfg-emailBackupTo')?.validity.valid) missing.push('收件人邮箱');
    if (!subject.trim() || /[\r\n]/.test(subject) || Array.from(subject).length > 200) missing.push('邮件标题');
    if (Array.from(body).length > 5000) missing.push('邮件正文');
    if (!['plain', 'card', 'accent'].includes(style)) missing.push('邮件样式');
    if (!/^#[0-9A-Fa-f]{6}$/.test(accentColor)) missing.push('邮件主色');
    if (format === 'cvexport') {
      const effectivePwd = pwd || (clearChecked ? '' : (hasExistingPwd ? '__set__' : ''));
      if (!effectivePwd) missing.push('加密密码');
    }
    return missing;
  }

  // expose for inline handlers
  window.doCopy = doCopy;
  window.doDelete = doDelete;
  window.doView = doView;
  window.doStar = doStar;
  window.doDownload = doDownload;
  window.doOpenDir = doOpenDir;
  window.doFileView = doFileView;
  window.doFileHash = doFileHash;
  window.switchView = window.switchView;
  window.removePattern = window.removePattern;

  // ── Export ─────────────────────────────────────────────────────────
  // authedDownload 处理二次认证后通过 fetch 下载文件（blob），避免 <a> 标签无法捕获 403。
  async function authedDownload(baseUrl, fallbackFilename, requestInit) {
    for (let attempt = 0; attempt < 2; attempt++) {
      const ok = await requestSecondaryAuth();
      if (!ok) { toast('需要二次验证才能导出', 'err'); return false; }
      try {
        const res = await secondaryFetch(baseUrl, requestInit);
        if (res.status === 403) continue; // token 失效，重试
        if (res.status === 402) {
          // 付费墙：未激活
          try {
            const d = await res.json();
            toast(d.message || '导出为付费功能，请先激活', 'err');
          } catch (_) { toast('导出为付费功能，请先激活', 'err'); }
          openLicenseSettings();
          return false;
        }
        if (!res.ok) { toast('导出失败: ' + res.status, 'err'); return false; }
        const blob = await res.blob();
        const cd = res.headers.get('Content-Disposition') || '';
        const m = cd.match(/filename="([^"]+)"/);
        const filename = m ? m[1] : fallbackFilename;
        const a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(a.href);
        return true;
      } catch (_) { toast('导出失败: 网络错误', 'err'); return false; }
    }
    toast('需要二次验证才能导出', 'err');
    return false;
  }

  // ── Export scope dialog (full / date range) ─────────────────
  window.showExportScopeDialog = function(performExport) {
    const html = `
      <div class="modal-overlay" id="exportScopeOverlay">
        <div class="modal-box secondary-box" onclick="event.stopPropagation()">
          <div class="modal-head">
            <span class="modal-title">选择导出范围</span>
            <button class="modal-close" onclick="closeExportScopeDialog()">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body" style="height:auto;padding:24px">
            <div style="margin-bottom:14px;font-size:13px;color:var(--sub)">
              <label style="margin-right:18px;cursor:pointer"><input type="radio" name="exportScopeMode" value="full" checked> 全部记录</label>
              <label style="cursor:pointer"><input type="radio" name="exportScopeMode" value="range"> 按日期范围</label>
            </div>
            <div id="exportRangeFields" class="hidden" style="margin-bottom:14px;font-size:13px;color:var(--sub)">
              <input type="date" id="exportFrom" class="lock-input" style="display:inline-block;width:auto;margin-right:6px">
              <span style="color:var(--sub)">至</span>
              <input type="date" id="exportTo" class="lock-input" style="display:inline-block;width:auto;margin-left:6px">
            </div>
            <button class="lock-btn" onclick="confirmExportScope()">开始导出</button>
          </div>
        </div>
      </div>
    `;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('exportScopeOverlay');
    const rangeFields = document.getElementById('exportRangeFields');
    document.querySelectorAll('input[name=exportScopeMode]').forEach(r => {
      r.addEventListener('change', () => {
        if (r.checked) rangeFields.classList.toggle('hidden', r.value !== 'range');
      });
    });
    window.closeExportScopeDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', (e) => { if (e.target === overlay) closeExportScopeDialog(); });
    window.confirmExportScope = function() {
      const mode = document.querySelector('input[name=exportScopeMode]:checked').value;
      let params = '';
      if (mode === 'range') {
        const from = document.getElementById('exportFrom').value;
        const to = document.getElementById('exportTo').value;
        if (!from || !to) { toast('请选择起止日期', 'err'); return; }
        params = '&mode=range&from=' + encodeURIComponent(from) + '&to=' + encodeURIComponent(to);
      }
      closeExportScopeDialog();
      performExport(params);
    };
  };

  window.doExport = async function(type) {
    if (!licenseState.licensed) { toast('导出为付费功能，激活后可导出全部数据', 'err'); openLicenseSettings(); return; }
    const fmt = type === 'data-xlsx' ? 'xlsx' : 'json';
    const fallback = 'clipvault-data.' + fmt;
    const label = '剪切板记录（' + (type === 'data-xlsx' ? 'Excel' : 'JSON') + '）';
    if (fmt === 'xlsx') {
	  if (!window.confirm('即将导出明文 Excel 数据。任何能访问导出文件的人都可以查看其中内容。是否继续？')) return;
	  showExportScopeDialog(function(scopeParams) {
		const baseUrl = '/api/export/data?format=xlsx' + scopeParams;
		authedDownload(baseUrl, fallback).then(ok => { if (ok) toast('已导出 ' + label); });
	  });
    } else {
      if (!window.confirm('即将导出明文剪贴板数据。任何能访问导出文件的人都可以查看其中内容。是否继续？')) return;
      showExportScopeDialog(function(scopeParams) {
        const baseUrl = '/api/export/data?format=json' + scopeParams;
        authedDownload(baseUrl, fallback).then(ok => { if (ok) toast('已导出 ' + label); });
      });
    }
  };

  // ── Encrypted export (.cvexport) ─────────────────────────────
  window.doExportCvexport = function() {
    if (!licenseState.licensed) { toast('导出为付费功能，激活后可导出全部数据', 'err'); openLicenseSettings(); return; }
    const html = `
      <div class="modal-overlay" id="cvexportOverlay">
        <div class="modal-box secondary-box" onclick="event.stopPropagation()">
          <div class="modal-head">
            <span class="modal-title">加密导出</span>
            <button class="modal-close" onclick="closeCvexportDialog()">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body" style="height:auto;padding:24px">
            <p style="margin-bottom:16px;color:var(--sub);font-size:13px;line-height:1.6">
              输入密码加密导出文件。导入时需要相同的密码才能解密。
            </p>
            <div style="margin-bottom:14px;font-size:13px;color:var(--sub)">
              <label style="margin-right:18px;cursor:pointer"><input type="radio" name="cvexportScopeMode" value="full" checked> 全部记录</label>
              <label style="cursor:pointer"><input type="radio" name="cvexportScopeMode" value="range"> 按日期范围</label>
            </div>
            <div id="cvexportRangeFields" class="hidden" style="margin-bottom:14px;font-size:13px;color:var(--sub)">
              <input type="date" id="cvexportFrom" class="lock-input" style="display:inline-block;width:auto;margin-right:6px">
              <span>至</span>
              <input type="date" id="cvexportTo" class="lock-input" style="display:inline-block;width:auto;margin-left:6px">
            </div>
            <form id="cvexportForm" class="lock-form">
              <input type="password" id="cvexportPwd" class="lock-input" placeholder="加密密码" autocomplete="new-password">
              <input type="password" id="cvexportConfirm" class="lock-input" placeholder="确认密码" autocomplete="new-password">
              <button type="submit" class="lock-btn">加密导出</button>
            </form>
            <p class="lock-err hidden" id="cvexportErr"></p>
          </div>
        </div>
      </div>
    `;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('cvexportOverlay');
    const form = document.getElementById('cvexportForm');
    const errEl = document.getElementById('cvexportErr');
    const pwdInput = document.getElementById('cvexportPwd');
    const confirmInput = document.getElementById('cvexportConfirm');
    setTimeout(() => pwdInput.focus(), 50);

    window.closeCvexportDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', (e) => { if (e.target === overlay) closeCvexportDialog(); });

    const rangeFields = document.getElementById('cvexportRangeFields');
    document.querySelectorAll('input[name=cvexportScopeMode]').forEach(r => {
      r.addEventListener('change', () => {
        rangeFields.classList.toggle('hidden', r.value !== 'range');
      });
    });

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const pwd = pwdInput.value;
      const confirm = confirmInput.value;
      errEl.classList.add('hidden');
      if (!pwd) { errEl.textContent = '密码不能为空'; errEl.classList.remove('hidden'); return; }
      if (pwd !== confirm) { errEl.textContent = '两次输入的密码不一致'; errEl.classList.remove('hidden'); return; }

      const mode = document.querySelector('input[name=cvexportScopeMode]:checked').value;
      let scopeParams = '';
      if (mode === 'range') {
        const from = document.getElementById('cvexportFrom').value;
        const to = document.getElementById('cvexportTo').value;
        if (!from || !to) { errEl.textContent = '请选择起止日期'; errEl.classList.remove('hidden'); return; }
        scopeParams = '&mode=range&from=' + encodeURIComponent(from) + '&to=' + encodeURIComponent(to);
      }

      const ok = await authedDownload('/api/export/data?format=cvexport' + scopeParams, 'clipvault-data.cvexport', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: pwd }),
      });
      pwdInput.value = '';
      confirmInput.value = '';
      closeCvexportDialog();
      if (ok) toast('已加密导出');
    });
  };

  // ── Import dialog ────────────────────────────────────────────
  window.showImportDialog = function() {
    const html = `
      <div class="modal-overlay" id="importOverlay">
        <div class="modal-box secondary-box" onclick="event.stopPropagation()">
          <div class="modal-head">
            <span class="modal-title">导入数据</span>
            <button class="modal-close" onclick="closeImportDialog()">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body" style="height:auto;padding:24px">
            <p style="margin-bottom:12px;color:var(--sub);font-size:13px;line-height:1.6">
              选择 JSON、Excel 或加密 .cvexport 文件导入。导入为追加模式：不会清空现有数据，也不做去重，重复导入同一文件会产生重复记录。加密文件需输入密码。提示：Excel 文件较大时解析慢且占内存，大数据量建议用 JSON 或加密 .cvexport 格式。
            </p>
            <input type="file" id="importFile" accept=".json,.xlsx,.cvexport" style="margin-bottom:12px;width:100%;font-size:13px;color:var(--sub)">
            <input type="password" id="importPwd" class="lock-input" placeholder="解密密码（仅 .cvexport 需要）" autocomplete="off" style="margin-bottom:12px">
            <button class="lock-btn" onclick="doImport()">开始导入</button>
            <p class="lock-err hidden" id="importErr" style="margin-top:12px"></p>
          </div>
        </div>
      </div>
    `;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('importOverlay');
    window.closeImportDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', (e) => { if (e.target === overlay) closeImportDialog(); });
  };

  async function doImport() {
    const fileInput = document.getElementById('importFile');
    const pwdInput = document.getElementById('importPwd');
    const errEl = document.getElementById('importErr');
    errEl.classList.add('hidden');

    if (!fileInput.files || !fileInput.files[0]) {
      errEl.textContent = '请选择文件';
      errEl.classList.remove('hidden');
      return;
    }

    const file = fileInput.files[0];
	const extension = file.name.toLowerCase().split('.').pop();
	const formats = { json: 'json', xlsx: 'xlsx', cvexport: 'cvexport' };
	const format = formats[extension];
	if (!format) {
	  errEl.textContent = '仅支持 JSON、Excel 或 .cvexport 文件';
	  errEl.classList.remove('hidden');
	  return;
	}
	const isCvexport = format === 'cvexport';
    const pwd = pwdInput.value;

    if (isCvexport && !pwd) {
      errEl.textContent = '加密文件需要输入密码';
      errEl.classList.remove('hidden');
      return;
    }

    try {
      const ok = await requestSecondaryAuth();
      if (!ok) { errEl.textContent = '需要二次验证才能导入'; errEl.classList.remove('hidden'); return; }
		const headers = { 'X-ClipVault-Import-Format': format };
		if (pwd) headers['X-ClipVault-Import-Password'] = pwd;
		const res = await secondaryFetch('/api/import/data', {
        method: 'POST',
		headers,
        body: file,
      });
      if (res.status === 403) {
        const ok2 = await requestSecondaryAuth();
        if (!ok2) { errEl.textContent = '需要二次验证才能导入'; errEl.classList.remove('hidden'); return; }
        const res2 = await secondaryFetch('/api/import/data', {
          method: 'POST',
			headers,
          body: file,
        });
        const d2 = await readImportResponse(res2);
        if (d2.ok) { closeImportDialog(); notifyImportSuccess(d2); }
        else { errEl.textContent = d2.error || '导入失败'; errEl.classList.remove('hidden'); }
        return;
      }
      const d = await readImportResponse(res);
      if (d.ok) {
        closeImportDialog();
        notifyImportSuccess(d);
      } else {
        errEl.textContent = d.error || '导入失败';
        errEl.classList.remove('hidden');
      }
    } catch (e) {
      errEl.textContent = '网络错误: ' + e.message;
      errEl.classList.remove('hidden');
    }
  }

  function notifyImportSuccess(data) {
    if (data.runtimeError) {
      toast('导入成功，但部分运行时设置需重启后生效：' + data.runtimeError, 'err');
      return;
    }
    if (data.hotkeyError) {
      toast('导入成功，' + data.hotkeyError, 'err');
      return;
    }
    toast('导入成功');
  }

  async function readImportResponse(res) {
    const body = await res.text();
    try {
      return JSON.parse(body);
    } catch (_) {
      return { ok: false, error: body.trim() || `\u5bfc\u5165\u8bf7\u6c42\u5931\u8d25 (${res.status})` };
    }
  }
  window.doImport = doImport;

  // ── Backup actions ─────────────────────────────────────────
  async function backupAction(url, successMsg) {
    if (!await flushPendingConfigSave()) return;
    const ok = await requestSecondaryAuth();
    if (!ok) { toast('需要二次验证', 'err'); return; }
    try {
      const res = await secondaryFetch(url, { method: 'POST' });
      const d = await res.json();
      if (d.ok) {
        toast(successMsg);
        loadBackupStatus();
      } else {
        const message = d.error === 'license required'
          ? '需要先激活 ClipVault 才能使用备份功能'
          : (d.error || '操作失败');
        toast(message, 'err');
      }
    } catch (e) {
      toast('网络错误: ' + e.message, 'err');
    }
  }

  window.doBackupDirNow = function() {
    backupAction('/api/backup/dir/now', '目录备份成功');
  };
  window.doBackupDirFullNow = function() {
    if (!window.confirm('将执行一次全量备份并重置增量链。是否继续？')) return;
    backupAction('/api/backup/dir/now?forceFull=1', '目录全量备份成功（增量链已重置）');
  };
  window.doBackupEmailNow = function() {
    backupAction('/api/backup/email/now', '邮件备份已发送');
  };
  window.doBackupEmailFullNow = function() {
    if (!window.confirm('将执行一次全量备份并重置增量链。是否继续？')) return;
    backupAction('/api/backup/email/now?forceFull=1', '邮件全量备份已发送（增量链已重置）');
  };
  window.doBackupEmailTest = function() {
    backupAction('/api/backup/email/test', '测试邮件已发送');
  };

  function formatBackupStatus(s) {
    if (!s || !s.lastTime || s.lastTime === '0001-01-01T00:00:00Z') return '';
    const time = new Date(s.lastTime).toLocaleString('zh-CN');
    if (!s.lastOK) {
      return '❌ 失败 · ' + time + ' · ' + (s.lastError || '未知错误');
    }
    let modeLabel;
    if (s.actualMode === 'incremental') {
      const interval = s.fullInterval > 0 ? '/' + s.fullInterval : '';
      modeLabel = '增量(' + s.incrementalCount + interval + ')';
    } else {
      modeLabel = s.autoFallback ? '全量(自动兜底)' : '全量';
    }
    let detail = '';
    if (s.lastFullAt) {
      const fullTime = new Date(s.lastFullAt).toLocaleString('zh-CN');
      if (s.actualMode === 'incremental' && s.fullInterval > 0) {
        detail = ' · 上次全量：' + fullTime + ' · 下次自动全量：第' + s.fullInterval + '次后';
      } else if (s.actualMode === 'incremental' && s.fullInterval === 0) {
        detail = ' · 上次全量：' + fullTime + ' · 未启用自动兜底，建议定期手动全量';
      }
    }
    const icon = s.autoFallback ? '⚠️' : '✅';
    return icon + ' ' + modeLabel + ' · ' + time + ' · ' + (s.backupFile || '') + detail;
  }

  async function loadBackupStatus() {
    try {
      const d = await fetch('/api/backup/status').then(r => r.json());
      const ds = d.dirStatus;
      const es = d.emailStatus;
      if (ds) {
        const row = document.getElementById('cfg-dirBackupStatusRow');
        const text = document.getElementById('cfg-dirBackupStatusText');
        const msg = formatBackupStatus(ds);
        if (row) row.style.display = msg ? '' : 'none';
        if (text) text.textContent = msg;
      }
      if (es) {
        const row = document.getElementById('cfg-emailBackupStatusRow');
        const text = document.getElementById('cfg-emailBackupStatusText');
        const msg = formatBackupStatus(es);
        if (row) row.style.display = msg ? '' : 'none';
        if (text) text.textContent = msg;
      }
    } catch (_) {}
  }

  window.showEmailBackupHistory = async function() {
    const ok = await requestSecondaryAuth();
    if (!ok) { toast('需要二次验证才能查看发送历史', 'err'); return; }
    try {
      const res = await secondaryFetch('/api/backup/email/history');
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || '读取发送历史失败');
      renderEmailBackupHistory(data.history || []);
    } catch (e) {
      toast('读取发送历史失败：' + (e.message || e), 'err');
    }
  };

  function renderEmailBackupHistory(history) {
    document.getElementById('emailBackupHistoryOverlay')?.remove();
    const rows = history.map(record => {
      const mode = record.mode === 'manual' ? '手动' : '自动';
      const format = { cvexport: '加密 cvexport', json: 'JSON', xlsx: 'Excel' }[record.format] || record.format;
      const time = new Date(record.sentAt).toLocaleString('zh-CN');
      return `<tr><td>${esc(mode)}</td><td>${esc(record.from)}</td><td>${esc(record.to)}</td><td>${esc(time)}</td><td>${esc(format)}</td></tr>`;
    }).join('');
    const content = rows || '<tr><td colspan="5" class="backup-history-empty">暂无成功发送的邮件备份记录</td></tr>';
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.id = 'emailBackupHistoryOverlay';
    overlay.innerHTML = `
      <div class="modal-box backup-history-modal" role="dialog" aria-modal="true" aria-labelledby="emailBackupHistoryTitle" onclick="event.stopPropagation()">
        <div class="modal-head">
          <span class="modal-title" id="emailBackupHistoryTitle">邮件备份发送历史</span>
          <button class="modal-close" type="button" aria-label="关闭">&times;</button>
        </div>
        <div class="modal-body backup-history-body">
          <table class="backup-history-table">
            <thead><tr><th>方式</th><th>发件账号</th><th>收件账号</th><th>发送时间</th><th>文件类型</th></tr></thead>
            <tbody>${content}</tbody>
          </table>
        </div>
      </div>`;
    const close = () => overlay.remove();
    overlay.addEventListener('click', e => { if (e.target === overlay) close(); });
    overlay.querySelector('.modal-close').addEventListener('click', close);
    document.body.appendChild(overlay);
  }

  // ── Group functions ─────────────────────────────────────────────────────

  async function loadGroups() {
    try {
      const d = await fetch('/api/groups').then(r => r.json());
      allGroups = d.groups || [];
    } catch (_) { allGroups = []; }
  }

  function buildGroupTree(parentID) {
    return allGroups
      .filter(g => g.parentId === parentID)
      .map(g => ({ ...g, children: buildGroupTree(g.id) }));
  }

  function flattenGroups(nodes, prefix) {
    let result = [];
    for (const n of nodes) {
      const label = prefix ? prefix + ' / ' + n.name : n.name;
      result.push({ id: n.id, label });
      if (n.children && n.children.length) {
        result = result.concat(flattenGroups(n.children, label));
      }
    }
    return result;
  }

  // ── Unified Group Modal ───────────────────────────────────
  const CHEVRON_SVG = '<svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="9 18 15 12 9 6"/></svg>';
  const FOLDER_SVG  = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>';
  const CHECK_SVG   = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="20 6 9 17 4 12"/></svg>';
  const EDIT_SVG    = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>';
  const DEL_SVG     = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>';
  const PLUS_SVG    = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>';

  function openGroupModal(mode, opts) {
    opts = opts || {};
    closeGroupModal();
    const titles = { filter: '选择分组筛选', assign: '加入分组', manage: '分组管理' };
    const boxClass = mode === 'manage' ? 'group-modal-box-wide' : 'group-modal-box';
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.id = 'groupModalOverlay';
    overlay.innerHTML =
      '<div class="modal-box ' + boxClass + '" onclick="event.stopPropagation()">' +
        '<div class="modal-head">' +
          '<span class="modal-title">' + titles[mode] + '</span>' +
          '<button class="modal-close" id="groupModalCloseBtn">' +
            '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>' +
          '</button>' +
        '</div>' +
        '<div class="modal-body group-modal-body" id="groupModalBody"></div>' +
      '</div>';
    document.body.appendChild(overlay);
    document.body.style.overflow = 'hidden';
    overlay.addEventListener('click', function(e) { if (e.target === overlay) closeGroupModal(); });
    document.getElementById('groupModalCloseBtn').addEventListener('click', closeGroupModal);
    groupModalMode = mode;
    groupModalOpts = opts;
    renderGroupModalContent();
  }

  window.openGroupModal = openGroupModal;

  function closeGroupModal() {
    const overlay = document.getElementById('groupModalOverlay');
    if (overlay) overlay.remove();
    document.body.style.overflow = '';
    groupModalMode = null;
    groupModalOpts = {};
  }

  async function renderGroupModalContent() {
    const body = document.getElementById('groupModalBody');
    if (!body) return;
    const mode = groupModalMode;
    const opts = groupModalOpts;
    if (allGroups.length === 0) { await loadGroups(); }

    // For assign mode, load item's current groups
    let itemGroupIds = [];
    if (mode === 'assign' && opts.itemId) {
      try {
        const res = await authorizedItemFetch(opts.itemId, '/api/items/groups?item_id=' + opts.itemId);
        if (!res.ok) throw new Error('HTTP ' + res.status);
        const d = await res.json();
        itemGroupIds = d.groupIds || [];
      } catch (_) {
        toast('无法读取条目分组', 'err');
        closeGroupModal();
        return;
      }
    }

    let html = '';
    // Toolbar for manage mode
    if (mode === 'manage') {
      html += '<div class="group-modal-toolbar">' +
        '<button class="btn-add-group" id="gmAddRootBtn">' + PLUS_SVG + ' 新建分组</button>' +
        '</div>';
    }

    if (allGroups.length === 0) {
      const msg = mode === 'manage' ? '暂无分组，点击上方按钮创建' : '暂无分组，请在设置中创建';
      html += '<div class="group-modal-empty">' + msg + '</div>';
    } else {
      const rootNodes = buildGroupTree(0);
      if (mode === 'filter') {
        html += '<div class="group-modal-tree">' + rootNodes.map(function(n) { return renderFilterNode(n); }).join('') + '</div>';
      } else if (mode === 'assign') {
        html += '<div class="group-modal-tree">' + rootNodes.map(function(n) { return renderAssignNode(n, itemGroupIds); }).join('') + '</div>';
      } else if (mode === 'manage') {
        html += '<div class="group-modal-tree">' + rootNodes.map(function(n) { return renderManageNode(n); }).join('') + '</div>';
      }
    }

    body.innerHTML = html;
    bindGroupModalEvents(mode, opts, itemGroupIds);
  }

  // ── Filter mode node (single select) ────────────────────────────
  function renderFilterNode(node) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = GROUP_EXPAND.has(node.id);
    const active = currentGroupID === node.id;
    let html = '<div class="gt-node">';
    html += '<div class="gt-row' + (active ? ' active' : '') + '" data-id="' + node.id + '">';
    html += '<span class="gt-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + CHEVRON_SVG + '</span>';
    html += '<span class="gt-icon">' + FOLDER_SVG + '</span>';
    html += '<span class="gt-label">' + esc(node.name) + '</span>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gt-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(renderFilterNode).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  }

  // ── Assign mode node (multi-select with checkboxes) ────────────────────
  function renderAssignNode(node, itemGroupIds) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = GROUP_EXPAND.has(node.id);
    const checked = itemGroupIds.includes(node.id);
    let html = '<div class="gt-node">';
    html += '<div class="gt-row" data-id="' + node.id + '">';
    html += '<span class="gt-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + CHEVRON_SVG + '</span>';
    html += '<span class="gt-check' + (checked ? ' checked' : '') + '" data-id="' + node.id + '">' + CHECK_SVG + '</span>';
    html += '<span class="gt-icon">' + FOLDER_SVG + '</span>';
    html += '<span class="gt-label">' + esc(node.name) + '</span>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gt-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(function(n) { return renderAssignNode(n, itemGroupIds); }).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  }

  // ── Manage mode node (CRUD actions) ─────────────────────────────
  function renderManageNode(node) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = GROUP_EXPAND.has(node.id);
    let html = '<div class="gm-node">';
    html += '<div class="gm-row">';
    html += '<span class="gm-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + CHEVRON_SVG + '</span>';
    html += '<span class="gm-icon">' + FOLDER_SVG + '</span>';
    html += '<span class="gm-name" data-id="' + node.id + '">' + esc(node.name) + '</span>';
    html += '<div class="gm-actions">';
    html += '<button class="gm-act" data-action="add-child" data-id="' + node.id + '" title="添加子分组">' + PLUS_SVG + '</button>';
    html += '<button class="gm-act" data-action="rename" data-id="' + node.id + '" title="重命名">' + EDIT_SVG + '</button>';
    html += '<button class="gm-act danger" data-action="delete" data-id="' + node.id + '" title="删除">' + DEL_SVG + '</button>';
    html += '</div>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gm-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(renderManageNode).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  }

  // ── Bind modal events ────────────────────────────────────────
  function bindGroupModalEvents(mode, opts, itemGroupIds) {
    const body = document.getElementById('groupModalBody');
    if (!body) return;

    // Toggle expand/collapse for all modes
    body.querySelectorAll('.gt-toggle, .gm-toggle').forEach(function(toggle) {
      toggle.addEventListener('click', function(e) {
        e.stopPropagation();
        const id = parseInt(toggle.dataset.id);
        if (GROUP_EXPAND.has(id)) { GROUP_EXPAND.delete(id); } else { GROUP_EXPAND.add(id); }
        renderGroupModalContent();
      });
    });

    if (mode === 'filter') {
      // Single-select: clicking a row selects the group
      body.querySelectorAll('.gt-row').forEach(function(row) {
        row.addEventListener('click', function(e) {
          if (e.target.closest('.gt-toggle')) return;
          const id = parseInt(row.dataset.id);
          if (currentGroupID === id) {
            currentGroupID = 0;
          } else {
            currentGroupID = id;
          }
          currentKind = '__group__';
          document.querySelectorAll('.filter-chip').forEach(function(c) { c.classList.remove('active'); });
          GROUP_CHIP.classList.add('active');
          closeGroupModal();
          resetSearch();
        });
      });
    } else if (mode === 'assign') {
      // Multi-select: clicking a row or checkbox toggles membership
      body.querySelectorAll('.gt-row').forEach(function(row) {
        row.addEventListener('click', async function(e) {
          if (e.target.closest('.gt-toggle')) return;
          const id = parseInt(row.dataset.id);
          const checkEl = row.querySelector('.gt-check');
          const isChecked = checkEl.classList.contains('checked');
          try {
            if (isChecked) {
              const res = await authorizedItemFetch(opts.itemId, '/api/items/group?item_id=' + opts.itemId + '&group_id=' + id, { method: 'DELETE' });
              if (!res.ok) throw new Error('HTTP ' + res.status);
              checkEl.classList.remove('checked');
              toast('已移出分组');
            } else {
              const res = await authorizedItemFetch(opts.itemId, '/api/items/group', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ itemId: opts.itemId, groupId: id }),
              });
              if (!res.ok) throw new Error('HTTP ' + res.status);
              checkEl.classList.add('checked');
              toast('已加入分组');
            }
          } catch (_) { toast('操作失败', 'err'); }
        });
      });
    } else if (mode === 'manage') {
      // Add root group
      const addRootBtn = document.getElementById('gmAddRootBtn');
      if (addRootBtn) {
        addRootBtn.addEventListener('click', function() {
          showGroupEditDialog('create', 0, '', async function(name) {
            try {
              const res = await fetch('/api/groups', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: name, parentId: 0 }),
              });
              const d = await res.json();
              if (d.ok) {
                toast('分组已创建');
                await loadGroups();
                renderGroupModalContent();
              } else { toast(d.error || '创建失败', 'err'); }
            } catch (_) { toast('创建失败', 'err'); }
          });
        });
      }
      // Action buttons
      body.querySelectorAll('.gm-act[data-action]').forEach(function(btn) {
        btn.addEventListener('click', function(e) {
          e.stopPropagation();
          const action = btn.dataset.action;
          const id = parseInt(btn.dataset.id);
          if (action === 'rename') startRenameGroup(id);
          else if (action === 'delete') confirmDeleteGroup(id);
          else if (action === 'add-child') quickAddChild(id);
        });
      });
    }
  }

  // ── Group CRUD helpers ───────────────────────────────────────
  function getParentId(id) {
    const g = allGroups.find(function(g) { return g.id === id; });
    return g ? g.parentId : 0;
  }

  function quickAddChild(parentId) {
    showGroupEditDialog('create', parentId, '', async function(name) {
      try {
        const res = await fetch('/api/groups', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: name, parentId: parentId }),
        });
        const d = await res.json();
        if (d.ok) {
          toast('子分组已创建');
          GROUP_EXPAND.add(parentId);
          await loadGroups();
          renderGroupModalContent();
        } else { toast(d.error || '创建失败', 'err'); }
      } catch (_) { toast('创建失败', 'err'); }
    });
  }

  function startRenameGroup(id) {
    const nameEl = document.querySelector('.gm-name[data-id="' + id + '"]');
    if (!nameEl) return;
    const oldName = nameEl.textContent;
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'gm-name-input';
    input.value = oldName;
    nameEl.replaceWith(input);
    input.focus();
    input.select();

    async function save() {
      const newName = input.value.trim();
      if (!newName || newName === oldName) {
        renderGroupModalContent();
        return;
      }
      try {
        const res = await fetch('/api/groups?id=' + id, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: newName, parentId: getParentId(id) }),
        });
        const d = await res.json();
        if (d.ok) {
          toast('已重命名');
          await loadGroups();
          renderGroupModalContent();
        } else { toast(d.error || '重命名失败', 'err'); renderGroupModalContent(); }
      } catch (_) { toast('重命名失败', 'err'); renderGroupModalContent(); }
    }

    input.addEventListener('blur', save);
    input.addEventListener('keydown', function(e) {
      if (e.key === 'Enter') { e.preventDefault(); input.blur(); }
      if (e.key === 'Escape') { renderGroupModalContent(); }
    });
  }

  function confirmDeleteGroup(id) {
    const g = allGroups.find(function(g) { return g.id === id; });
    if (!g) return;
    showConfirmDialog('删除分组', '确定删除分组「' + g.name + '」及其子分组吗？此操作不可撤销。', async function() {
      try {
        const res = await fetch('/api/groups?id=' + id, { method: 'DELETE' });
        const d = await res.json();
        if (d.ok) {
          toast('分组已删除');
          await loadGroups();
          renderGroupModalContent();
        } else { toast('删除失败', 'err'); }
      } catch (_) { toast('删除失败', 'err'); }
    });
  }

  // ── Generic group edit dialog (create/rename) ─────────────────────────────
  function showGroupEditDialog(mode, parentId, defaultName, onConfirm) {
    const title = mode === 'create' ? '新建分组' : '重命名分组';
    const html =
      '<div class="modal-overlay" id="groupEditOverlay">' +
        '<div class="modal-box secondary-box" onclick="event.stopPropagation()">' +
          '<div class="modal-head">' +
            '<span class="modal-title">' + title + '</span>' +
            '<button class="modal-close" onclick="closeGroupEditDialog()">' +
              '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>' +
            '</button>' +
          '</div>' +
          '<div class="modal-body" style="height:auto;padding:24px">' +
            '<form id="groupEditForm" class="lock-form">' +
              '<input type="text" id="groupEditInput" class="lock-input" placeholder="分组名称" value="' + esc(defaultName) + '" style="text-align:left" autofocus>' +
              '<button type="submit" class="lock-btn">确定</button>' +
            '</form>' +
          '</div>' +
        '</div>' +
      '</div>';
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('groupEditOverlay');
    const input = document.getElementById('groupEditInput');
    const form = document.getElementById('groupEditForm');
    setTimeout(function() { input.focus(); input.select(); }, 50);

    window.closeGroupEditDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', function(e) { if (e.target === overlay) overlay.remove(); });

    form.addEventListener('submit', function(e) {
      e.preventDefault();
      const name = input.value.trim();
      if (!name) return;
      overlay.remove();
      onConfirm(name);
    });
  }

  // ── Generic confirm dialog ───────────────────────────────────────
  function showConfirmDialog(title, message, onConfirm) {
    const html =
      '<div class="modal-overlay" id="confirmDialogOverlay">' +
        '<div class="modal-box secondary-box" onclick="event.stopPropagation()">' +
          '<div class="modal-head"><span class="modal-title">' + title + '</span></div>' +
          '<div class="modal-body" style="height:auto;padding:24px">' +
            '<p style="margin:0 0 16px;color:var(--sub);font-size:14px">' + esc(message) + '</p>' +
            '<div style="display:flex;gap:8px;justify-content:flex-end">' +
              '<button class="btn-cancel" onclick="closeConfirmDialog()">取消</button>' +
              '<button class="lock-btn" id="confirmDialogBtn" style="width:auto;padding:8px 20px;background:var(--danger)">删除</button>' +
            '</div>' +
          '</div>' +
        '</div>' +
      '</div>';
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('confirmDialogOverlay');
    window.closeConfirmDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', function(e) { if (e.target === overlay) overlay.remove(); });
    document.getElementById('confirmDialogBtn').addEventListener('click', function() {
      overlay.remove();
      onConfirm();
    });
  }


  // ── Init ─────────────────────────────────────────────────
  // -- Scroll-to-top ------------------------------------------
  const SCROLL_TOP_BTN = document.getElementById('scrollTopBtn');
  let scrollTicking = false;

  window.addEventListener('scroll', () => {
    if (scrollTicking) return;
    scrollTicking = true;
    requestAnimationFrame(() => {
      SCROLL_TOP_BTN.classList.toggle('visible', window.scrollY > 300);
      scrollTicking = false;
    });
  }, { passive: true });

  SCROLL_TOP_BTN.addEventListener('click', () => {
    window.scrollTo({ top: 0, behavior: 'smooth' });
  });

  async function init() {
    setupAutoSave();
    await loadTheme();
    await loadLicenseStatus();
    setupSrcExeUI();
    await loadSourceExes();
    // 支持 URL 参数快速跳转：
    //   ?q=<text>      直接以文本搜索
    //   ?locate=<id>   定位到时间线中的指定词条（复用 doLocate 逻辑）
    const params = new URLSearchParams(window.location.search);
    const qParam = params.get('q');
    const locateParam = params.get('locate');
    if (locateParam) {
      const id = parseInt(locateParam, 10);
      if (id > 0) {
        // 清掉 URL 上的 locate 参数，避免刷新时重复定位
        try { history.replaceState(null, '', window.location.pathname); } catch (_) {}
        doLocate(id);
        loadGroups();
        return;
      }
    }
    if (qParam !== null) {
      Q.value = qParam;
      try { history.replaceState(null, '', window.location.pathname); } catch (_) {}
    }
    search();
    loadGroups();
  }

  // ── Boot ─────────────────────────────────────────────────
  (async function boot() {
	await loadPublicAppearance();
    const ok = await checkAuthStatus();
    if (ok) {
      init();
    }

    // 标签页切回前台时，刷新认证状态和数据
    document.addEventListener('visibilitychange', async () => {
      if (document.visibilityState !== 'visible') return;
      const wasSecondaryAuthed = authState.secondaryAuthed;
      await checkAuthStatus();
      await loadLicenseStatus();
      // 如果二次认证缓存已过期，重新搜索以遮蔽敏感内容
      if (wasSecondaryAuthed && !authState.secondaryAuthed) {
        search();
      }
    });
  })();
}());
