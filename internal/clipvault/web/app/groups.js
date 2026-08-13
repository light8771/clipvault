        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">加密导出</span>
            <button class="modal-close" data-action="close-encrypted-export">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <p class="u-inline-28">
              输入密码加密导出文件。导入时需要相同的密码才能解密。
            </p>
            <div class="u-inline-22">
              <label class="u-inline-23"><input type="radio" name="cvexportScopeMode" value="full" checked> 全部记录</label>
              <label class="u-inline-24"><input type="radio" name="cvexportScopeMode" value="range"> 按日期范围</label>
            </div>
            <div id="cvexportRangeFields" class="hidden u-inline-22">
              <input type="date" id="cvexportFrom" class="lock-input u-inline-25">
              <span>至</span>
              <input type="date" id="cvexportTo" class="lock-input u-inline-27">
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

    closeCvexportDialog = function() { overlay.remove(); };
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

      const ok = await authedDownload('/api/v1/transfer/export?format=cvexport' + scopeParams, 'clipvault-data.cvexport', {
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
  showImportDialog = function() {
    const html = `
      <div class="modal-overlay" id="importOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">导入数据</span>
            <button class="modal-close" data-action="close-import">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <p class="u-inline-29">
              选择 JSON、Excel 或加密 .cvexport 文件导入。导入为追加模式：不会清空现有数据，也不做去重，重复导入同一文件会产生重复记录。加密文件需输入密码。提示：Excel 文件较大时解析慢且占内存，大数据量建议用 JSON 或加密 .cvexport 格式。
            </p>
            <input type="file" id="importFile" accept=".json,.xlsx,.cvexport" class="u-inline-30">
            <input type="password" id="importPwd" class="lock-input u-inline-31" placeholder="解密密码（仅 .cvexport 需要）" autocomplete="off">
            <button class="lock-btn" data-action="import">开始导入</button>
            <p class="lock-err hidden u-inline-32" id="importErr"></p>
          </div>
        </div>
      </div>
    `;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('importOverlay');
    closeImportDialog = function() { overlay.remove(); };
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
		const headers = { 'X-ClipVault-Import-Format': format };
		if (pwd) headers['X-ClipVault-Import-Password'] = pwd;
		const res = await protectedFetch('/api/v1/transfer/import', {
        method: 'POST',
		headers,
        body: file,
      });
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
    try { return await res.json(); } catch (_) {}
    return { ok: false, error: `\u5bfc\u5165\u8bf7\u6c42\u5931\u8d25 (${res.status})` };
  }

  // ── Backup actions ─────────────────────────────────────────
  async function backupAction(url, successMsg) {
    if (!await flushPendingConfigSave()) return;
    try {
      const res = await protectedFetch(url, { method: 'POST' });
      const d = await res.json();
      if (d.ok) {
        toast(successMsg);
        loadBackupStatus();
      } else {
        const message = d.code === 'license_required'
          ? '需要先激活 ClipVault 才能使用备份功能'
          : (d.error || '操作失败');
        toast(message, 'err');
      }
    } catch (e) {
      toast('网络错误: ' + e.message, 'err');
    }
  }

  doBackupDirNow = function() {
    backupAction('/api/v1/backup/directory', '目录备份成功');
  };
  doBackupDirFullNow = function() {
    if (!window.confirm('将执行一次全量备份并重置增量链。是否继续？')) return;
    backupAction('/api/v1/backup/directory?forceFull=1', '目录全量备份成功（增量链已重置）');
  };
  doBackupEmailNow = function() {
    backupAction('/api/v1/backup/email', '邮件备份已发送');
  };
  doBackupEmailFullNow = function() {
    if (!window.confirm('将执行一次全量备份并重置增量链。是否继续？')) return;
    backupAction('/api/v1/backup/email?forceFull=1', '邮件全量备份已发送（增量链已重置）');
  };
  doBackupEmailTest = function() {
    backupAction('/api/v1/backup/email-test', '测试邮件已发送');
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
      const d = await fetch('/api/v1/backup').then(r => r.json());
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

  showEmailBackupHistory = async function() {
    try {
      const res = await protectedFetch('/api/v1/backup/email-history');
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
      <div class="modal-box backup-history-modal" role="dialog" aria-modal="true" aria-labelledby="emailBackupHistoryTitle" data-modal-panel>
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
      const d = await fetch('/api/v1/groups').then(r => r.json());
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
      '<div class="modal-box ' + boxClass + '" data-modal-panel>' +
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
        const res = await authorizedItemFetch(opts.itemId, '/api/v1/items/groups?item_id=' + opts.itemId);
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