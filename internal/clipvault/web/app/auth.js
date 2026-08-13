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
      const res = await protectedFetch('/api/v1/recovery/isolated-switch', { method: 'DELETE' }, { fresh: true });
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

  showIsolatedDatabaseDialog = function(id) {
    const database = recoveryState.isolatedDatabases.find((item) => item.id === id);
    if (!database || !database.canSwitch || recoveryState.pendingSwitchId) return;
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
    closeIsolatedDatabaseDialog = function() { overlay.remove(); };
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
        const res = await protectedFetch('/api/v1/recovery/isolated-switch', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: database.id, password: input.value }),
        }, { fresh: true });
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

  showRecoveryDialog = function(action) {
    const isChange = action === 'change';
    const isReset = action === 'reset';
    const title = '数据恢复密码' + (isReset ? '重置' : '修改');
    const oldLabel = isChange ? '当前恢复密码' : null;
    const html = `
      <div class="modal-overlay" id="recDialogOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${title}</span>
            <button class="modal-close" data-action="close-recovery">
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
    setTimeout(() => (oldInput || newInput).focus(), 50);

    closeRecDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', (e) => { if (e.target === overlay) closeRecDialog(); });

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const oldPwd = oldInput ? oldInput.value : '';
      const newPwd = newInput.value;
      const confirmPwd = confirmInput.value;
      errEl.classList.add('hidden');

      if (newPwd !== confirmPwd) { errEl.textContent = '两次输入的密码不一致'; errEl.classList.remove('hidden'); return; }

      try {
        const baseUrl = isReset ? '/api/v1/recovery/reset' : '/api/v1/recovery/password';
        const body = isChange
          ? { oldPassword: oldPwd, newPassword: newPwd }
          : { password: newPwd };

        const res = await protectedFetch(baseUrl, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        }, { fresh: isReset });
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

  switchView = async function(view) {
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
      const d = await fetch('/api/v1/license').then(r => r.json());
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
      const d = await fetch('/api/v1/license/machine').then(r => r.json());
      fullMachineId = d.machineId || '';
      if (d.machineCode) document.getElementById('licenseMachineCode').value = d.machineCode;
    } catch (_) {}
  }

  openLicenseSettings = async function() {
    if (await switchView('settings')) snavTo('s-license', 'snav-license');
  };

  copyPurchaseInfo = async function() {
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

  activateLicense = async function() {
    const input = document.getElementById('licenseTokenInput');
    const button = document.getElementById('licenseActivateBtn');
    const message = document.getElementById('licenseMessage');
    const token = input.value.trim();
    if (!token) { message.textContent = '请粘贴完整激活码'; message.className = 'license-message err'; return; }
    button.disabled = true;
    button.textContent = '正在验证…';
    message.textContent = '';
    try {
      const res = await fetch('/api/v1/license/activation', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token }),
      });
      const d = await res.json();
      if (!res.ok || !d.ok) {
        message.textContent = licenseActivationError(d.code, d.error);
        message.className = 'license-message err';
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

  function licenseActivationError(code, message) {
    if (code === 'license_bad_format') return '激活码不完整，请重新复制后粘贴';
    if (code === 'license_bad_signature') return '激活码验证未通过，请确认输入是否正确。';
    if (code === 'license_machine_mismatch') return '此激活码不适用于当前设备，请重新提供本机机器码。';
    if (code === 'license_unsupported') return '当前版本无法识别此激活码，请联系技术支持。';
    if (code === 'license_machine_unavailable') return '无法读取设备信息，尚未进行激活';
    return message || '激活码有效性或本地保存失败，请重试';
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