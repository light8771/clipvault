
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

  openSourceAppsDialog = function() {
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

  closeSourceAppsDialog = function() {
    document.getElementById('sourceAppsModal')?.classList.add('hidden');
    sourceAppsDraft = null;
  };

  saveSourceAppsDialog = async function() {
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
      try {
        const d = await protectedFetch('/api/v1/config/sensitive-rules').then(r => r.json());
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
        <button class="pattern-del" data-action="remove-pattern" data-index="${i}" title="删除">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
    `).join('');
  }

  removePattern = function(idx) {
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

  async function saveConfigNow() {
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
      const res = await protectedFetch('/api/v1/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newCfg),
      });
      await requireOK(res, '配置保存失败');
      const d = await res.json();