    const request = ++configRequestSequence;
    const revision = configRevision;
    try {
      const cfg = await fetch('/api/v1/config').then(r => r.json());
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
      const res = await fetch('/api/v1/storage');
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
      const res = await fetch('/api/v1/storage/open', { method: 'POST' });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) { toast(data.error || '无法打开数据目录', 'err'); return; }
      toast('已在文件资源管理器中打开');
    } catch (_) {
      toast('无法打开数据目录', 'err');
    } finally {
      btn.disabled = false;
    }
  }

  openMigrateDialog = function() {
    if (storageRelocationInFlight || storageRelocationScheduled) return;
    const currentDirText = document.getElementById('storageCurrentDir')?.textContent || '-';
    const html = `
      <div class="modal-overlay" id="migrateOverlay">
        <div class="modal-box secondary-box migrate-modal" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">迁移数据目录</span>
            <button class="modal-close" data-action="close-migrate">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
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

    closeMigrateDialog = function() { overlay.remove(); };
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
        const res = await protectedFetch('/api/v1/storage/relocation', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ dataDir: target }),
        });
        await requireOK(res, '数据目录迁移准备失败');
        scheduled = true;

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