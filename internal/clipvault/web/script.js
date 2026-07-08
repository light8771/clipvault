(function(){
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
  let currentPage = 0, totalCount = 0;
  let filterDateFrom = '', filterDateTo = '';
  let currentGroupID = 0; // 0 = no group filter
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
    if (!pwd) return;
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

  function requestSecondaryAuth() {
    return new Promise(async (resolve) => {
      if (!authState.hasSecondaryPassword) { resolve(true); return; }
      // 后端校验：查询当前会话的二次认证状态
      try {
        const st = await fetch('/api/auth/status').then(r => r.json());
        authState.secondaryAuthed = st.secondaryAuthed;
        if (st.secondaryAuthed) { resolve(true); return; }
      } catch (_) {}
      // 需要弹窗输入二次密码
      secondaryResolve = resolve;
      SECONDARY_MODAL.classList.remove('hidden');
      SECONDARY_INPUT.value = '';
      SECONDARY_ERR.classList.add('hidden');
      setTimeout(() => SECONDARY_INPUT.focus(), 50);
    });
  }

  window.closeSecondaryModal = function() {
    SECONDARY_MODAL.classList.add('hidden');
    if (secondaryResolve) { secondaryResolve(false); secondaryResolve = null; }
  };

  SECONDARY_FORM.addEventListener('submit', async (e) => {
    e.preventDefault();
    const pwd = SECONDARY_INPUT.value;
    if (!pwd) return;
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
        authState.secondaryAuthed = true;
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
    const isChange = action === 'change';
    const title = (type === 'lock' ? '锁屏密码' : '二次密码') + (isChange ? '修改' : '设置');
    const oldLabel = isChange ? '当前密码' : null;
    const html = `
      <div class="modal-overlay" id="pwdDialogOverlay">
        <div class="modal-box secondary-box" onclick="event.stopPropagation()">
          <div class="modal-head">
            <span class="modal-title">${title}</span>
            <button class="modal-close" onclick="closePwdDialog()">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body" style="height:auto;padding:24px">
            <form id="pwdDialogForm" class="lock-form">
              <input type="text" name="username" autocomplete="username" value="clipvault" style="display:none" aria-hidden="true">
              ${oldLabel ? `<input type="password" id="pwdDlgOld" class="lock-input" placeholder="${oldLabel}" autocomplete="current-password">` : ''}
              <input type="password" id="pwdDlgNew" class="lock-input" placeholder="新密码" autocomplete="new-password">
              <input type="password" id="pwdDlgConfirm" class="lock-input" placeholder="确认新密码" autocomplete="new-password">
              <button type="submit" class="lock-btn">确定</button>
            </form>
            <p class="lock-err hidden" id="pwdDlgErr"></p>
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
    setTimeout(() => (oldInput || newInput).focus(), 50);

    window.closePwdDialog = function() {
      overlay.remove();
    };

    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) closePwdDialog();
    });

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const oldPwd = oldInput ? oldInput.value : '';
      const newPwd = newInput.value;
      const confirmPwd = confirmInput.value;
      errEl.classList.add('hidden');

      if (!newPwd) { errEl.textContent = '新密码不能为空'; errEl.classList.remove('hidden'); return; }
      if (newPwd !== confirmPwd) { errEl.textContent = '两次输入的密码不一致'; errEl.classList.remove('hidden'); return; }
      if (isChange && !oldPwd) { errEl.textContent = '请输入当前密码'; errEl.classList.remove('hidden'); return; }

      try {
        const res = await fetch('/api/auth/password', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ type, action, oldPassword: oldPwd, newPassword: newPwd }),
        });
        const d = await res.json();
        if (d.ok) {
          closePwdDialog();
          toast((type === 'lock' ? '锁屏密码' : '二次密码') + '已' + (isChange ? '修改' : '设置'));
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
    const secStatus = document.getElementById('secondaryPwdStatus');
    const secSetBtn = document.getElementById('secondaryPwdSetBtn');
    const secChangeBtn = document.getElementById('secondaryPwdChangeBtn');
    if (lockStatus) lockStatus.textContent = lockSet ? '已设置' : '未设置';
    if (lockSetBtn) lockSetBtn.classList.toggle('hidden', lockSet);
    if (lockChangeBtn) lockChangeBtn.classList.toggle('hidden', !lockSet);
    if (secStatus) secStatus.textContent = secSet ? '已设置' : '未设置';
    if (secSetBtn) secSetBtn.classList.toggle('hidden', secSet);
    if (secChangeBtn) secChangeBtn.classList.toggle('hidden', !secSet);
  }

  // ── View switch ───────────────────────────────────────────
  window.switchView = function(view) {
    const isHistory = view === 'history';
    document.getElementById('viewHistory').classList.toggle('hidden', !isHistory);
    document.getElementById('viewSettings').classList.toggle('hidden', isHistory);
    document.getElementById('tabHistory').classList.toggle('active', isHistory);
    document.getElementById('tabSettings').classList.toggle('active', !isHistory);
    if (!isHistory) { loadSettings(); }
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
    TC.appendChild(el);
    requestAnimationFrame(() => el.classList.add('show'));
    const duration = type === 'err' ? 5000 : 2200;
    setTimeout(() => {
      el.classList.remove('show');
      setTimeout(() => el.remove(), 260);
    }, duration);
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
    let url = '/api/items/sensitive?id=' + id;
    if (pendingSecondaryToken) {
      url += '&secondary_token=' + encodeURIComponent(pendingSecondaryToken);
      pendingSecondaryToken = null;
    }
    const res = await fetch(url);
    if (res.status === 403) return null;
    const d = await res.json();
    return d.item ? d.item.text : null;
  }

  async function doCopy(id, btn) {
    const item = items.get(id);
    if (!item) return;
    let textToCopy = item.text;
    if (item.sensitive) {
      const ok = await requestSecondaryAuth();
      if (!ok) { toast('需要二次验证才能复制敏感内容', 'err'); return; }
      textToCopy = await fetchSensitiveText(id);
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
      const res = await fetch('/api/items?id=' + id, { method: 'DELETE' });
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
      const res = await fetch('/api/items?id=' + id + '&starred=' + (newStarred ? '1' : '0'), { method: 'PATCH' });
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

    // 敏感内容需要二次验证后获取明文
    if (item.sensitive && !authState.secondaryAuthed) {
      const ok = await requestSecondaryAuth();
      if (!ok) { toast('需要二次验证才能查看敏感内容', 'err'); return; }
    }

    let displayText = item.text;
    if (item.sensitive) {
      try {
        let url = '/api/items/sensitive?id=' + id;
        if (pendingSecondaryToken) {
          url += '&secondary_token=' + encodeURIComponent(pendingSecondaryToken);
          pendingSecondaryToken = null;
        }
        const res = await fetch(url);
        if (res.status === 403) {
          const ok = await requestSecondaryAuth();
          if (!ok) { toast('需要二次验证才能查看敏感内容', 'err'); return; }
          return doView(id);
        }
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
      preview.innerHTML = paths.map((p) => {
        const name = getBaseName(p);
        const ext = getExt(name);
        const isImg = IMAGE_EXTS.includes(ext);
        return '<div class="modal-file-item">' +
          (isImg ? '<img src="/api/file?path=' + encodeURIComponent(p) + '" loading="lazy" alt="' + esc(name) + '" onerror="this.style.display=\'none\'">' : '') +
          '<div class="modal-file-name">' + esc(name) + '</div>' +
          '<div class="modal-file-path">' + esc(p) + '</div>' +
        '</div>';
      }).join('');
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
      const ok = await requestSecondaryAuth();
      if (!ok) { toast('需要二次验证才能复制敏感内容', 'err'); return; }
      textToCopy = await fetchSensitiveText(currentModalId);
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
        const d = await fetch('/api/file/check?path=' + encodeURIComponent(p)).then(r => r.json());
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
            if (thumb) thumb.innerHTML = '<img src="/api/file?path=' + encodeURIComponent(p) + '" loading="lazy" onclick="doFileView(' + itemId + ',' + idx + ')">';
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

  function doDownload(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const p = paths[idx];
    const a = document.createElement('a');
    a.href = '/api/file?path=' + encodeURIComponent(p) + '&download=1';
    a.download = getBaseName(p);
    a.style.display = 'none';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    toast('开始下载');
  }

  async function doOpenDir(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const p = paths[idx];
    const btn = document.getElementById('fdir-' + itemId + '-' + idx);
    if (btn) { btn.disabled = true; }
    try {
      const res = await fetch('/api/file/opendir?path=' + encodeURIComponent(p), { method: 'POST' });
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
    const p = paths[idx];
    const btn = document.getElementById('fhashbtn-' + itemId + '-' + idx);
    const hashDiv = document.getElementById('fhash-' + itemId + '-' + idx);
    if (btn) { btn.disabled = true; }
    if (hashDiv) hashDiv.innerHTML = '<span class="fcheck">计算中...</span>';
    try {
      const d = await fetch('/api/file/hash?path=' + encodeURIComponent(p)).then(r => r.json());
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

  function doFileView(itemId, idx) {
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
    preview.innerHTML = '<img src="/api/file?path=' + encodeURIComponent(p) + '" alt="' + esc(name) + '">';
    VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  }

  // ── Render ────────────────────────────────────────────────
  const viewHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg> 查看';
  const delHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';
  const groupBtnHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><path d="M9 12h6M9 16h4"/></svg>';

  function render(arr, q) {
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
          </div>
          <div class="card-acts">
            ${showView ? `<button class="ab exp" onclick="doView(${it.id})">${viewHTML}</button>` : ''}
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
  }

  // ── Search ────────────────────────────────────────────────
    function resetSearch() { currentPage = 0; search(); }

  async function search() {
    const q = Q.value;
    const pageSize = parseInt(PAGESIZE.value) || 50;
    try {
      const p = new URLSearchParams({ q, limit: pageSize, offset: currentPage * pageSize });
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
      // 敏感搜索需要二次认证 token
      if (url === '/api/search/sensitive' && pendingSecondaryToken) {
        p.set('secondary_token', pendingSecondaryToken);
        pendingSecondaryToken = null;
      }
      const d = await fetch(url + '?' + p).then(r => r.json());
      if (d.error === 'secondary_required') {
        const ok = await requestSecondaryAuth();
        if (ok) { await search(); return; }
        toast('需要二次验证才能查看敏感数据', 'err');
        return;
      }
      totalCount = d.total || 0;
      render(d.items || [], q);
      updatePagination();
    } catch (_) {}
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
    BADGE.textContent = totalCount;
    RC.textContent = totalCount + ' 条结果';
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
        const res = await fetch('/api/clear', { method: 'POST' });
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
      } catch (_) { toast('清除失败', 'err'); }
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

  async function loadSettings() {
    try {
      const cfg = await fetch('/api/config').then(r => r.json());
      currentCfg = cfg;
      applyConfigToForm(cfg);
    } catch (_) { toast('加载配置失败', 'err'); }
  }

  function applyConfigToForm(cfg) {
    document.getElementById('cfg-maxTextChars').value = cfg.maxTextChars ?? 0;
    document.getElementById('cfg-maxFileDropPaths').value = cfg.maxFileDropPaths ?? 0;

    document.getElementById('cfg-listenAddr').value = (cfg.search && cfg.search.listenAddr) || '127.0.0.1:18765';

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
  }

  // ── Unlock filter rules (secondary auth) ────────────────
  const unlockFilterBtn = document.getElementById('unlockFilterBtn');
  if (unlockFilterBtn) {
    unlockFilterBtn.addEventListener('click', async () => {
      const ok = await requestSecondaryAuth();
      if (!ok) { toast('需要二次验证才能查看过滤规则', 'err'); return; }
      try {
        let url = '/api/sensitive/rules';
        if (pendingSecondaryToken) {
          url += '?secondary_token=' + encodeURIComponent(pendingSecondaryToken);
          pendingSecondaryToken = null;
        }
        const d = await fetch(url).then(r => r.json());
        if (d.error === 'secondary_required') {
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
  let pendingRestart = false;

  function collectConfig() {
    if (!currentCfg) return null;
    const patterns = getPatterns();
    const lockTimeout = parseInt(document.getElementById('cfg-lockSessionTimeout')?.value) || 0;
    const secTimeout  = parseInt(document.getElementById('cfg-secondaryAuthTimeout')?.value) || 0;
    return {
      ...currentCfg,
      maxTextChars:     parseInt(document.getElementById('cfg-maxTextChars').value) || 0,
      maxFileDropPaths: parseInt(document.getElementById('cfg-maxFileDropPaths').value) || 0,
      sensitive: { customPatterns: patterns },
      search: {
        ...currentCfg.search,
        listenAddr: document.getElementById('cfg-listenAddr').value.trim() || '127.0.0.1:18765',
      },
      security: {
        ...currentCfg.security,
        lockSessionTimeout:   lockTimeout,
        secondaryAuthTimeout: secTimeout,
      },
    };
  }

  async function saveConfig(opts) {
    opts = opts || {};
    const newCfg = collectConfig();
    if (!newCfg) return;

    // 验证正则合法性
    for (const p of newCfg.sensitive.customPatterns) {
      try { new RegExp(p); } catch (_) {
        toast(`正则无效：${p}`, 'err');
        return;
      }
    }

    // 检测 listenAddr 是否变更
    const addrChanged = currentCfg.search && currentCfg.search.listenAddr !== newCfg.search.listenAddr;

    try {
      const url = pendingSecondaryToken
        ? '/api/config?secondary_token=' + encodeURIComponent(pendingSecondaryToken)
        : '/api/config';
      pendingSecondaryToken = null;
      const res = await fetch(url, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newCfg),
      });
      if (!res.ok) {
        const txt = await res.text();
        throw new Error(txt);
      }
      const d = await res.json();
      currentCfg = d.config;
      if (opts.silent !== true) toast('设置已保存');

      if (addrChanged && !opts.skipRestart) {
        showRestartConfirm();
      }
    } catch (e) {
      if (e.message && e.message.includes('secondary_required')) {
        const ok = await requestSecondaryAuth();
        if (ok) { await saveConfig(opts); return; }
        toast('需要二次验证才能保存配置', 'err');
      } else {
        toast('保存失败：' + (e.message || e), 'err');
      }
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
    const maxAttempts = 60;
    const interval = setInterval(async () => {
      attempts++;
      if (attempts > maxAttempts) {
        clearInterval(interval);
        RESTART_OVERLAY.classList.add('hidden');
        toast('重启超时，请手动刷新页面', 'err');
        return;
      }
      try {
        const res = await fetch('/api/auth/status');
        if (res.ok) {
          clearInterval(interval);
          // 服务已恢复，刷新页面
          window.location.reload();
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
    settingsEl.addEventListener('input', () => {
      clearTimeout(saveTimer);
      saveTimer = setTimeout(() => saveConfig({ silent: true }), 800);
    });
    settingsEl.addEventListener('change', () => {
      clearTimeout(saveTimer);
      saveTimer = setTimeout(() => saveConfig({ silent: true }), 300);
    });
    // 过滤规则增删也触发保存
    const addBtn = document.getElementById('addPatternBtn');
    if (addBtn) {
      addBtn.addEventListener('click', () => {
        clearTimeout(saveTimer);
        saveTimer = setTimeout(() => saveConfig({ silent: true }), 300);
      });
    }
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
  window.doExport = async function(type) {
    let url = type === 'config'
      ? '/api/export/config'
      : '/api/export/data?format=' + (type === 'data-csv' ? 'csv' : 'json');
    // 使用临时链接触发浏览器下载
    const a = document.createElement('a');
    a.href = url;
    a.style.display = 'none';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    const label = type === 'config' ? '配置' : ('剪切板记录（' + (type === 'data-csv' ? 'CSV' : 'JSON') + '）');
    toast('开始导出 ' + label);
  };

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
        const d = await fetch('/api/items/groups?item_id=' + opts.itemId).then(r => r.json());
        itemGroupIds = d.groupIds || [];
      } catch (_) {}
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
              await fetch('/api/items/group?item_id=' + opts.itemId + '&group_id=' + id, { method: 'DELETE' });
              checkEl.classList.remove('checked');
              toast('已移出分组');
            } else {
              await fetch('/api/items/group', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ itemId: opts.itemId, groupId: id }),
              });
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
  function init() {
    refreshState();
    search();
    setupAutoSave();
    loadGroups();
  }

  // ── Boot ─────────────────────────────────────────────────
  (async function boot() {
    const ok = await checkAuthStatus();
    if (ok) {
      init();
    }

    // 标签页切回前台时，刷新认证状态和数据
    document.addEventListener('visibilitychange', async () => {
      if (document.visibilityState !== 'visible') return;
      const wasSecondaryAuthed = authState.secondaryAuthed;
      await checkAuthStatus();
      // 如果二次认证缓存已过期，重新搜索以遮蔽敏感内容
      if (wasSecondaryAuthed && !authState.secondaryAuthed) {
        search();
      }
    });
  })();
}());
