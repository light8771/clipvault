import { app } from "./app-context.js";
import { trapModalFocus } from './modal-controller.js';
export function registerConfigSave() {
  app.RESTART_OVERLAY = document.getElementById('restartOverlay');
  app.RESTART_CONFIRM = document.getElementById('restartConfirm');
  app.RESTART_CANCEL = document.getElementById('restartCancelBtn');
  app.RESTART_CONFIRM_BTN = document.getElementById('restartConfirmBtn');
  app.saveTimer = 0;
  app.saveInFlight = false;
  app.activeConfigSave = null;
  app.pendingConfigRevision = 0;
  app.savedConfigRevision = 0;
  app.storageRelocationInFlight = false;
  app.storageRelocationScheduled = false;
  app.storageStatusRequestSequence = 0;
  app.diagnosticsExportInFlight = false;
  app.autoSaveBound = false;
  app.hasPendingConfigSave = function hasPendingConfigSave() {
    return app.pendingConfigRevision !== app.savedConfigRevision || app.saveInFlight;
  };
  app.collectConfig = function collectConfig() {
    if (!app.currentCfg) return null;
    const cur = app.currentCfg;
    const has = id => !!document.getElementById(id);
    // 敏感过滤规则只在安全页收集；其他设置页保留服务端当前值，避免清空规则。
    let sensitive = cur.sensitive;
    if (has('filterAuthGate') || has('patternList')) {
      // 当过滤规则被遮蔽（auth gate 可见）时，保留原配置中的 patterns，避免用空数组覆盖
      const gate = document.getElementById('filterAuthGate');
      const patternsMasked = gate && !gate.classList.contains('hidden');
      sensitive = { customPatterns: patternsMasked ? cur.sensitive && cur.sensitive.customPatterns || [] : app.getPatterns() };
    }
    // 通知配置只在外观与通知页收集；其他页面保留当前值，避免清空文案。
    let notifications = cur.notifications;
    if (has('cfg-pauseResumeNotifications')) {
      notifications = {
        ...cur.notifications,
        pauseResume: !!document.getElementById('cfg-pauseResumeNotifications').checked,
        pauseTitle: document.getElementById('cfg-pauseNotificationTitle')?.value.trim() || '',
        pauseBody: document.getElementById('cfg-pauseNotificationBody')?.value.trim() || '',
        resumeTitle: document.getElementById('cfg-resumeNotificationTitle')?.value.trim() || '',
        resumeBody: document.getElementById('cfg-resumeNotificationBody')?.value.trim() || ''
      };
    }
    // 快捷键、图片捕获与弹窗配置只在录制与快捷键页收集与校验；其他页面保留当前值。
    let hotkey = cur.hotkey, searchHotkey = cur.searchHotkey, image = cur.image, popup = cur.popup;
    if (has('cfg-hotkeyEnabled')) {
      const hkEnabled = document.getElementById('cfg-hotkeyEnabled').checked;
      let hkMods = 0;
      document.querySelectorAll('#hotkeyMods .mod-btn input').forEach(cb => {
        if (cb.checked) hkMods |= parseInt(cb.value);
      });
      const hkKeyChar = app.parseVkInput(document.getElementById('cfg-hotkeyKey')?.value);
      if (hkKeyChar === null) {
        app.toast('全局快捷键主键仅支持 A–Z、0–9、F1–F24 或 Space', 'err');
        return null;
      }
      if (hkEnabled && (hkMods === 0 || hkKeyChar === 0)) {
        app.toast('启用快捷键时需选择至少一个修饰键并输入一个按键', 'err');
        return null;
      }
      const shkEnabled = document.getElementById('cfg-searchHotkeyEnabled')?.checked || false;
      let shkMods = 0;
      document.querySelectorAll('#searchHotkeyMods .mod-btn input').forEach(cb => {
        if (cb.checked) shkMods |= parseInt(cb.value);
      });
      const shkKeyChar = app.parseVkInput(document.getElementById('cfg-searchHotkeyKey')?.value);
      if (shkKeyChar === null) {
        app.toast('搜索框快捷键主键仅支持 A–Z、0–9、F1–F24 或 Space', 'err');
        return null;
      }
      if (shkEnabled && (shkMods === 0 || shkKeyChar === 0)) {
        app.toast('启用搜索框快捷键时需选择至少一个修饰键并输入一个按键', 'err');
        return null;
      }
      // 防止两个快捷键完全相同
      if (hkEnabled && shkEnabled && hkMods === shkMods && hkKeyChar === shkKeyChar) {
        app.toast('搜索框快捷键不能与全局快捷键相同', 'err');
        return null;
      }
      // 弹窗三动作快捷键：仅 [Ctrl+][Shift+][Alt+]Enter，三者互不相同
      const popupKeyPattern = /^(?:(ctrl|shift|alt)\+){0,3}enter$/i;
      const popupKeys = ['cfg-popupKeyOpen', 'cfg-popupKeyRestore', 'cfg-popupKeyPaste'].map(id => (document.getElementById(id)?.value || '').trim());
      const popupKeyNormalized = popupKeys.map(k => k.toLowerCase());
      if (popupKeyNormalized.some(k => !popupKeyPattern.test(k))) {
        app.toast('弹窗动作快捷键仅支持 [Ctrl+][Shift+][Alt+]Enter 组合', 'err');
        return null;
      }
      if (new Set(popupKeyNormalized).size !== popupKeyNormalized.length) {
        app.toast('弹窗三个动作的快捷键不能相同', 'err');
        return null;
      }
      const imageMaxMB = parseInt(document.getElementById('cfg-imageMaxMB')?.value) || 32;
      hotkey = { enabled: hkEnabled, modifiers: hkMods, key: hkKeyChar };
      searchHotkey = { enabled: shkEnabled, modifiers: shkMods, key: shkKeyChar };
      image = {
        enabled: !!document.getElementById('cfg-imageEnabled')?.checked,
        maxBytes: imageMaxMB * 1024 * 1024
      };
      popup = {
        hideDuplicates: !!document.getElementById('cfg-popupHideDuplicates')?.checked,
        hideUnavailable: !!document.getElementById('cfg-popupHideUnavailable')?.checked,
        keyOpen: popupKeys[0] || 'Enter',
        keyRestore: popupKeys[1] || 'Shift+Enter',
        keyPaste: popupKeys[2] || 'Ctrl+Enter'
      };
    }
    // 定时备份配置只在备份设置页收集；其他页面保留当前值。
    let backup = cur.backup;
    if (has('cfg-dirBackupEnabled') || has('cfg-emailBackupEnabled')) {
      backup = {
        ...cur.backup,
        dirBackup: {
          ...cur.backup?.dirBackup,
          enabled: document.getElementById('cfg-dirBackupEnabled')?.checked || false,
          runOnStartup: document.getElementById('cfg-dirBackupRunOnStartup')?.checked || false,
          interval: parseInt(document.getElementById('cfg-dirBackupInterval')?.value) || 24,
          dir: document.getElementById('cfg-dirBackupDir')?.value.trim() || '',
          format: document.getElementById('cfg-dirBackupFormat')?.value || 'cvexport',
          password: app.backupPwdValue('cfg-dirBackupPwd', 'cfg-dirBackupPwdClear'),
          scope: {
            mode: document.getElementById('cfg-dirBackupScope')?.value || 'full',
            from: '',
            to: ''
          },
          incrementalFullInterval: (v => isNaN(v) ? 7 : v)(parseInt(document.getElementById('cfg-dirBackupIncrementalFullInterval')?.value))
        },
        emailBackup: {
          ...cur.backup?.emailBackup,
          enabled: document.getElementById('cfg-emailBackupEnabled')?.checked || false,
          runOnStartup: document.getElementById('cfg-emailBackupRunOnStartup')?.checked || false,
          interval: parseInt(document.getElementById('cfg-emailBackupInterval')?.value) || 24,
          smtpHost: document.getElementById('cfg-emailBackupSMTPHost')?.value.trim() || '',
          smtpPort: parseInt(document.getElementById('cfg-emailBackupSMTPPort')?.value) || 587,
          smtpUser: document.getElementById('cfg-emailBackupSMTPUser')?.value.trim() || '',
          smtpPass: app.smtpPasswordValue(),
          from: document.getElementById('cfg-emailBackupFrom')?.value.trim() || '',
          to: document.getElementById('cfg-emailBackupTo')?.value.trim() || '',
          format: document.getElementById('cfg-emailBackupFormat')?.value || 'cvexport',
          password: app.backupPwdValue('cfg-emailBackupPwd', 'cfg-emailBackupPwdClear'),
          subject: document.getElementById('cfg-emailBackupSubject')?.value || '',
          body: document.getElementById('cfg-emailBackupBody')?.value || '',
          style: document.getElementById('cfg-emailBackupStyle')?.value || '',
          accentColor: document.getElementById('cfg-emailBackupAccentColor')?.value || '',
          scope: {
            mode: document.getElementById('cfg-emailBackupScope')?.value || 'full',
            from: '',
            to: ''
          },
          maxPartSize: Number(document.getElementById('cfg-emailBackupMaxPartSize')?.value) * 1024 * 1024,
          incrementalFullInterval: (v => isNaN(v) ? 7 : v)(parseInt(document.getElementById('cfg-emailBackupIncrementalFullInterval')?.value))
        }
      };
    }
    return {
      ...cur,
      brand: {
        ...cur.brand,
        title: document.getElementById('cfg-brandTitle')?.value.trim() || cur.brand?.title || 'ClipVault'
      },
      background: app.backgroundFromForm(),
      theme: app.themeFromForm(),
      maxTextChars: has('cfg-maxTextChars') ? parseInt(document.getElementById('cfg-maxTextChars').value) || 0 : cur.maxTextChars,
      maxFileDropPaths: has('cfg-maxFileDropPaths') ? parseInt(document.getElementById('cfg-maxFileDropPaths').value) || 0 : cur.maxFileDropPaths,
      recordSourceInfo: has('cfg-recordSourceInfo') ? !!document.getElementById('cfg-recordSourceInfo').checked : cur.recordSourceInfo,
      source: has('cfg-sourceShowExe') ? {
        // 展开 currentCfg.source：未来新增字段在前端未渲染时也能保留，不会被覆盖成 undefined
        ...cur.source,
        showExe: !!document.getElementById('cfg-sourceShowExe').checked,
        exeAliases: app.sourceAliases,
        exeWhitelist: app.sourceWhitelist
      } : cur.source,
      sensitive,
      search: has('cfg-listenAddr') ? {
        ...cur.search,
        listenAddr: document.getElementById('cfg-listenAddr').value.trim() || '127.0.0.1:18765',
        timeZone: document.getElementById('cfg-timeZone')?.value.trim() || 'system',
        reverseProxy: {
          enabled: document.getElementById('cfg-reverseProxyEnabled')?.checked || false,
          publicUrl: document.getElementById('cfg-reverseProxyPublicUrl')?.value.trim() || ''
        }
      } : cur.search,
      security: has('cfg-lockSessionTimeout') ? {
        ...cur.security,
        lockSessionTimeout: parseInt(document.getElementById('cfg-lockSessionTimeout').value) || 0,
        secondaryAuthTimeout: parseInt(document.getElementById('cfg-secondaryAuthTimeout')?.value) || 0
      } : cur.security,
      hotkey,
      searchHotkey,
      image,
      popup,
      notifications,
      backup
    };
  };
  app.scheduleConfigSave = function scheduleConfigSave(delay) {
    app.pendingConfigRevision++;
    clearTimeout(app.saveTimer);
    if (delay <= 0) {
      app.saveTimer = 0;
      void app.saveConfig();
      return;
    }
    app.saveTimer = setTimeout(() => {
      app.saveTimer = 0;
      void app.saveConfig();
    }, delay);
  };
  app.flushPendingConfigSave = async function flushPendingConfigSave() {
    clearTimeout(app.saveTimer);
    app.saveTimer = 0;
    while (app.hasPendingConfigSave()) {
      if (!(await app.saveConfig())) return false;
    }
    return true;
  };
  app.saveConfig = async function saveConfig() {
    if (app.activeConfigSave) return app.activeConfigSave;
    app.saveInFlight = true;
    app.activeConfigSave = (async () => {
      for (;;) {
        const revision = app.pendingConfigRevision;
        if (!(await app.saveConfigNow())) return false;
        if (revision === app.pendingConfigRevision) {
          app.savedConfigRevision = revision;
          return true;
        }
        clearTimeout(app.saveTimer);
        app.saveTimer = 0;
      }
    })();
    try {
      return await app.activeConfigSave;
    } finally {
      app.activeConfigSave = null;
      app.saveInFlight = false;
    }
  };
  app.saveConfigNow = async function saveConfigNow() {
    const newCfg = app.collectConfig();
    if (!newCfg) return false;
    // 备份相关校验只在备份设置页执行：校验函数读取备份页专有 DOM，
    // 其他页面保存时服务端已存的备份配置由后端自行校验。
    const onBackupPage = !!document.getElementById('cfg-dirBackupEnabled');
    if (onBackupPage) {
      const emailMaxPartSizeMB = newCfg.backup.emailBackup.maxPartSize / (1024 * 1024);
      if (!Number.isInteger(emailMaxPartSizeMB) || emailMaxPartSizeMB < 1 || emailMaxPartSizeMB > 50) {
        app.toast('单封邮件上限必须是 1–50MB 的整数', 'err');
        return false;
      }
    }

    // 敏感规则以服务端 Go RE2 为唯一语法标准。不要在浏览器中用
    // JavaScript RegExp 预校验；两种引擎的语法不同（例如 Go 支持 (?i)）。

    // 启用定时备份时，先在前端校验配置完整性，避免发送必然失败请求造成"切格式不生效"的错觉。
    if (onBackupPage && newCfg.backup?.dirBackup?.enabled) {
      const missing = app.validateDirBackupConfig();
      if (missing.length) {
        app.toast('目录备份配置不完整：' + missing.join('、'), 'err');
        return false;
      }
    }
    if (onBackupPage && newCfg.backup?.emailBackup?.enabled) {
      const missing = app.validateEmailBackupConfig();
      if (missing.length) {
        app.toast('邮件备份配置不完整：' + missing.join('、'), 'err');
        return false;
      }
    }
    const previousSearch = app.currentCfg.search || {};
    const previousProxy = previousSearch.reverseProxy || {};
    const nextProxy = newCfg.search.reverseProxy;
    const addrChanged = previousSearch.listenAddr !== newCfg.search.listenAddr;
    const proxyChanged = !!previousProxy.enabled !== nextProxy.enabled || (previousProxy.publicUrl || '') !== nextProxy.publicUrl;
    const openingLAN = addrChanged && app.isLoopbackListenAddress(previousSearch.listenAddr) && !app.isLoopbackListenAddress(newCfg.search.listenAddr);
    const enablingProxy = !previousProxy.enabled && nextProxy.enabled;
    if (nextProxy.enabled && !app.isLoopbackListenAddress(newCfg.search.listenAddr)) {
      app.toast('启用 HTTPS 反向代理时，Web 监听地址必须是回环地址', 'err');
      return false;
    }
    if (nextProxy.enabled || nextProxy.publicUrl) {
      try {
        const publicURL = new URL(nextProxy.publicUrl);
        const rootPath = publicURL.pathname === '' || publicURL.pathname === '/';
        if (publicURL.protocol !== 'https:' || publicURL.username || publicURL.password || !publicURL.hostname || !rootPath || publicURL.search || publicURL.hash) {
          throw new Error('invalid public URL');
        }
      } catch (_) {
        app.toast('外部 HTTPS 地址必须是完整的 https:// 主机地址，不能包含路径、账号、查询参数或片段', 'err');
        return false;
      }
    }
    if (openingLAN || enablingProxy) {
      if (!app.authState.hasLockPassword) {
        app.toast('开放远程访问前，请先设置锁屏密码', 'err');
        return false;
      }
      const message = enablingProxy ? '安全确认：反向代理必须运行在本机，并将请求转发到当前回环监听地址。ClipVault 只接受与配置地址完全匹配的 HTTPS 转发头。是否继续？' : '安全警告：非回环地址会通过明文 HTTP 向同一网络开放 ClipVault。请确认网络可信、防火墙规则正确，并只向可信用户提供锁屏密码。是否继续？';
      if (!window.confirm(message)) {
        document.getElementById('cfg-listenAddr').value = previousSearch.listenAddr;
        document.getElementById('cfg-reverseProxyEnabled').checked = !!previousProxy.enabled;
        document.getElementById('cfg-reverseProxyPublicUrl').value = previousProxy.publicUrl || '';
        app.updateListenAddressWarning();
        app.toast('已取消开放远程访问');
        return false;
      }
    }
    try {
      const res = await app.protectedFetch('/api/v1/config', {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify(newCfg)
      });
      await app.requireOK(res, '配置保存失败');
      const d = await res.json();
      app.replaceCurrentConfig(d.config);
      const runtimeWarning = d.runtimeError || d.hotkeyError;
      if (runtimeWarning) {
        app.toast('设置已保存，但' + runtimeWarning, 'err');
      } else {
        app.toast('设置已保存');
      }
      if (addrChanged || proxyChanged) {
        app.showRestartConfirm();
      }
      return true;
    } catch (e) {
      app.toast('保存失败：' + (e.message || e), 'err');
      return false;
    }
  };
  app.showRestartConfirm = function showRestartConfirm() {
    app.restartConfirmTrigger = document.activeElement;
    app.RESTART_CONFIRM.classList.remove('hidden');
    requestAnimationFrame(() => app.RESTART_CANCEL.focus());
  };
  app.hideRestartConfirm = function hideRestartConfirm() {
    app.RESTART_CONFIRM.classList.add('hidden');
    if (app.restartConfirmTrigger?.isConnected && typeof app.restartConfirmTrigger.focus === 'function') app.restartConfirmTrigger.focus();
    app.restartConfirmTrigger = null;
  };
  app.RESTART_CONFIRM.addEventListener('keydown', event => {
    if (event.key === 'Escape') {
      event.preventDefault();
      app.hideRestartConfirm();
      return;
    }
    trapModalFocus(app.RESTART_CONFIRM, event);
  });
  app.RESTART_CANCEL.addEventListener('click', () => {
    app.hideRestartConfirm();
    app.toast('监听地址将在下次重启后生效');
  });
  app.RESTART_CONFIRM_BTN.addEventListener('click', async () => {
    app.hideRestartConfirm();
    await app.doRestart();
  });
  app.doRestart = async function doRestart() {
    app.RESTART_OVERLAY.classList.remove('hidden');
    try {
      const res = await app.fetch('/api/v1/system/restart', {
        method: 'POST'
      });
      if (!res.ok) {
        app.RESTART_OVERLAY.classList.add('hidden');
        app.toast('重启请求失败，请手动重启应用', 'err');
        return;
      }
    } catch (_) {
      // 服务已开始关闭，fetch 可能失败，继续轮询
    }
    // 开始轮询等待服务恢复
    app.pollRestart();
  };
  app.pollRestart = function pollRestart() {
    let attempts = 0;
    const maxAttempts = 15;
    const interval = setInterval(async () => {
      attempts++;
      if (attempts > maxAttempts) {
        clearInterval(interval);
        app.RESTART_OVERLAY.classList.add('hidden');
        app.toast('重启超时，新页面可能已在新标签页打开', 'err');
        return;
      }
      try {
        const res = await app.fetch('/api/v1/session');
        if (res.ok) {
          clearInterval(interval);
          // 新页面已由后端 shellExecute 在新标签页打开，仅隐藏遮罩
          app.RESTART_OVERLAY.classList.add('hidden');
        }
      } catch (_) {
        // 服务尚未恢复，继续轮询
      }
    }, 1000);
  };
}
