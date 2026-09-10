import { app } from "./app-context.js";
import { createRemovableModal } from './modal-controller.js';

const t = window.clipvaultI18n.t;

export function registerSettingsStorage() {
  const smtpUser = document.getElementById('cfg-emailBackupSMTPUser');
  const smtpCredentialUser = document.getElementById('cfg-emailBackupSMTPCredentialUser');
  smtpUser?.addEventListener('input', () => {
    if (smtpCredentialUser) smtpCredentialUser.value = smtpUser.value;
  });
  app.loadSettings = async function loadSettings() {
    const request = ++app.configRequestSequence;
    const revision = app.configRevision;
    try {
      const response = await app.fetch('/api/v1/config');
      await app.requireOK(response, t('config.load_failed'));
      const cfg = await response.json();
      if (request === app.configRequestSequence && revision === app.configRevision && !app.hasPendingConfigSave()) {
        app.replaceCurrentConfig(cfg, response.headers.get('X-ClipVault-Config-Version'));
        try {
          await app.loadSourceExes('all');
        } catch (_) {}
        app.applyConfigToForm(cfg);
      }
    } catch (_) {
      if (request === app.configRequestSequence && revision === app.configRevision && !app.hasPendingConfigSave()) {
        app.toast(t('config.load_failed'), 'err');
      }
    }
    if (request !== app.configRequestSequence) return;
    app.loadRecoveryStatus();
    app.loadStorageStatus();
  };
  app.loadStorageStatus = async function loadStorageStatus() {
    const request = ++app.storageStatusRequestSequence;
    const currentDir = document.getElementById('storageCurrentDir');
    const hint = document.getElementById('storageCurrentHint');
    const openBtn = document.getElementById('storageOpenDirBtn');
    const button = document.getElementById('storageRelocateBtn');
    const logDir = document.getElementById('storageCurrentLogDir');
    const logHint = document.getElementById('storageLogHint');
    const logOpenBtn = document.getElementById('storageOpenLogDirBtn');
    const logChangeBtn = document.getElementById('storageChangeLogDirBtn');
    if (!currentDir || !hint || !button) return;
    try {
      const res = await app.fetch('/api/v1/storage');
      if (!res.ok) throw new Error('status');
      const status = await res.json();
      if (request !== app.storageStatusRequestSequence || app.storageRelocationInFlight) return;
      currentDir.textContent = status.dataDir || '-';
      currentDir.title = status.dataDir || '';
      if (openBtn) openBtn.disabled = !status.dataDir;
      if (logDir) {
        logDir.textContent = status.logDir || '-';
        logDir.title = status.logDir || '';
      }
      if (logOpenBtn) logOpenBtn.disabled = !status.logDir;
      if (logHint) {
        if (status.logPending) {
          logHint.textContent = t('storage.log_change_pending', { dir: status.pendingLogDir || '-' });
        } else if (status.logFallback) {
          logHint.textContent = t('storage.log_fallback');
        } else if (status.logConfigurable) {
          logHint.textContent = t('storage.log_change_available');
        } else {
          logHint.textContent = status.logReason || t('storage.log_change_unsupported');
        }
      }
      if (logChangeBtn) logChangeBtn.disabled = !status.logConfigurable || app.logDirectoryChangeInFlight;
      if (status.movable && !status.pending && !app.storageRelocationScheduled) {
        hint.textContent = t('storage.relocate_available');
        button.disabled = false;
      } else if (status.movable) {
        hint.textContent = t('storage.relocate_pending');
        button.disabled = true;
      } else {
        hint.textContent = status.reason || t('storage.relocate_unsupported');
        button.disabled = true;
      }
    } catch (_) {
      if (request !== app.storageStatusRequestSequence || app.storageRelocationInFlight) return;
      hint.textContent = t('storage.status_unavailable');
      button.disabled = true;
      if (openBtn) openBtn.disabled = true;
      if (logOpenBtn) logOpenBtn.disabled = true;
      if (logChangeBtn) logChangeBtn.disabled = true;
      if (logHint) logHint.textContent = t('storage.status_unavailable');
    }
  };
  app.openDataDirectory = async function openDataDirectory() {
    const btn = document.getElementById('storageOpenDirBtn');
    if (!btn || btn.disabled) return;
    btn.disabled = true;
    try {
      const res = await app.fetch('/api/v1/storage/open', {
        method: 'POST'
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        app.toast(data.error || t('storage.open_failed'), 'err');
        return;
      }
      app.toast(t('storage.opened'));
    } catch (_) {
      app.toast(t('storage.open_failed'), 'err');
    } finally {
      btn.disabled = false;
    }
  };
  app.openLogDirectory = async function openLogDirectory() {
    const btn = document.getElementById('storageOpenLogDirBtn');
    if (!btn || btn.disabled) return;
    btn.disabled = true;
    try {
      const res = await app.fetch('/api/v1/storage/log-directory/open', { method: 'POST' });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        app.toast(data.error || t('storage.log_open_failed'), 'err');
        return;
      }
      app.toast(t('storage.log_opened'));
    } catch (_) {
      app.toast(t('storage.log_open_failed'), 'err');
    } finally {
      btn.disabled = false;
    }
  };
  app.openMigrateDialog = function () {
    if (app.storageRelocationInFlight || app.storageRelocationScheduled) return;
    const currentDirText = document.getElementById('storageCurrentDir')?.textContent || '-';
    const html = `
      <div class="modal-overlay" id="migrateOverlay">
        <div class="modal-box secondary-box migrate-modal" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${app.esc(t('storage.migrate_title'))}</span>
            <button class="modal-close" type="button" data-action="close-migrate" aria-label="${app.esc(t('storage.migrate_close_label'))}">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <div class="migrate-notes">
              <p>${app.esc(t('storage.migrate_note'))}</p>
            </div>
            <div class="migrate-field">
              <label class="migrate-label">${app.esc(t('storage.current_dir'))}</label>
              <code class="migrate-dir" id="migrateOldDir"></code>
            </div>
            <div class="migrate-field">
              <label class="migrate-label" for="migrateNewDir">${app.esc(t('storage.migrate_new_dir'))}</label>
              <input type="text" id="migrateNewDir" class="field-input" autocomplete="off" placeholder="${app.esc(t('storage.migrate_new_dir_placeholder'))}">
            </div>
            <div class="migrate-progress hidden" id="migrateProgress"></div>
            <p class="lock-err hidden" id="migrateErr"></p>
            <div class="migrate-actions">
              <button type="button" class="btn-export" id="migrateCancelBtn">${app.esc(t('common.cancel'))}</button>
              <button type="button" class="lock-btn" id="migrateExecBtn">${app.esc(t('storage.migrate_exec'))}</button>
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
    const modal = createRemovableModal(overlay, {
      initialFocus: newDirInput,
      canClose: () => !app.storageRelocationInFlight
    });
    app.closeMigrateDialog = modal.close;
    cancelBtn.addEventListener('click', app.closeMigrateDialog);
    const steps = [{
      label: t('storage.migrate_step_validate')
    }, {
      label: t('storage.migrate_step_submit')
    }, {
      label: t('storage.migrate_step_done')
    }];
    function renderProgress(states) {
      progressEl.classList.remove('hidden');
      progressEl.innerHTML = steps.map((s, i) => {
        const st = states[i] || 'pending';
        const icon = st === 'done' ? '\u2713' : st === 'active' ? '\u25CF' : st === 'error' ? '\u2715' : '\u25CB';
        return '<div class="migrate-step ms-' + st + '"><span class="ms-icon">' + icon + '</span><span class="ms-label">' + app.esc(s.label) + '</span></div>';
      }).join('');
    }
    let completed = false;
    execBtn.addEventListener('click', async () => {
      if (completed) {
        app.closeMigrateDialog();
        return;
      }
      const target = newDirInput.value.trim();
      errEl.classList.add('hidden');
      if (!target) {
        errEl.textContent = t('storage.migrate_need_dir');
        errEl.classList.remove('hidden');
        return;
      }
      execBtn.disabled = true;
      newDirInput.disabled = true;
      cancelBtn.disabled = true;
      const states = ['active', 'pending', 'pending'];
      renderProgress(states);

      // Step 0: validate target
      states[0] = 'done';
      states[1] = 'active';
      renderProgress(states);

      // Step 1: submit (includes secondary auth + POST, retry on 403)
      app.storageRelocationInFlight = true;
      try {
        const res = await app.protectedFetch('/api/v1/storage/relocation', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({
            dataDir: target
          })
        });
        await app.requireOK(res, t('storage.migrate_prepare_failed'));
        completed = true;
        states[1] = 'done';
        states[2] = 'active';
        renderProgress(states);
        states[2] = 'done';
        renderProgress(states);
        progressEl.insertAdjacentHTML('beforeend', '<div class="migrate-success">\u2713 ' + app.esc(t('storage.migrate_submitted')) + '</div>');
        execBtn.textContent = t('common.close');
        execBtn.disabled = false;
        cancelBtn.style.display = 'none';
        app.storageRelocationScheduled = true;
        newDirInput.value = '';
      } catch (e) {
        states[1] = 'error';
        renderProgress(states);
        errEl.textContent = t('storage.migrate_failed_with', { message: e.message || e });
        errEl.classList.remove('hidden');
        execBtn.disabled = false;
        newDirInput.disabled = false;
        cancelBtn.disabled = false;
      } finally {
        app.storageRelocationInFlight = false;
        void app.loadStorageStatus();
      }
    });
  };
  app.isLoopbackListenAddress = function isLoopbackListenAddress(addr) {
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
  };
  app.updateListenAddressWarning = function updateListenAddressWarning() {
    const input = document.getElementById('cfg-listenAddr');
    const warning = document.getElementById('listenAddrWarning');
    if (!input || !warning) return;
    warning.classList.toggle('hidden', app.isLoopbackListenAddress(input.value));
  };
  app.applyConfigToForm = function applyConfigToForm(cfg) {
    app.applyBrandFormState(cfg.brand);
    app.applyBackgroundFormState(cfg.background);
    app.applyAppearanceFormState(cfg);
    app.applyNavigationFormState(cfg);
    app.applyHistoryPageFormState(cfg);
    const setValue = (id, value) => {
      const el = document.getElementById(id);
      if (el) el.value = value;
    };
    setValue('cfg-maxTextChars', cfg.maxTextChars ?? 0);
    setValue('cfg-maxFileDropPaths', cfg.maxFileDropPaths ?? 0);
    const rsi = document.getElementById('cfg-recordSourceInfo');
    // 缺省视为开启：与后端 DefaultConfig().RecordSourceInfo = true 对齐
    if (rsi) rsi.checked = cfg.recordSourceInfo !== false;

    // 来源应用配置：ShowExe 缺省视为 true；别名/白名单编辑器依赖 allSourceExes（由 loadSourceExes 填充）
    app.sourceAliases = cfg.source?.exeAliases || {};
    app.sourceWhitelist = cfg.source?.exeWhitelist?.length ? cfg.source.exeWhitelist.slice() : null;
    const showExeEl = document.getElementById('cfg-sourceShowExe');
    if (showExeEl) showExeEl.checked = cfg.source?.showExe !== false;
    app.updateSourceAppsSummary();
    setValue('cfg-listenAddr', cfg.search && cfg.search.listenAddr || '127.0.0.1:15678');
    const timeZoneEl = document.getElementById('cfg-timeZone');
    if (timeZoneEl) timeZoneEl.value = cfg.search && cfg.search.timeZone || 'system';
    app.updateListenAddressWarning();

    // 全局快捷键
    const hk = cfg.hotkey || {};
    const hkEnabled = document.getElementById('cfg-hotkeyEnabled');
    const hkKey = document.getElementById('cfg-hotkeyKey');
    if (hkEnabled) hkEnabled.checked = !!hk.enabled;
    const mods = hk.modifiers || 0;
    document.querySelectorAll('#hotkeyMods .mod-btn input').forEach(cb => {
      cb.checked = (mods & parseInt(cb.value)) !== 0;
    });
    if (hkKey) hkKey.value = app.vkToDisplay(hk.key);

    // 搜索框快捷键
    const shk = cfg.searchHotkey || {};
    const shkEnabled = document.getElementById('cfg-searchHotkeyEnabled');
    const shkKey = document.getElementById('cfg-searchHotkeyKey');
    if (shkEnabled) shkEnabled.checked = !!shk.enabled;
    const smods = shk.modifiers || 0;
    document.querySelectorAll('#searchHotkeyMods .mod-btn input').forEach(cb => {
      cb.checked = (smods & parseInt(cb.value)) !== 0;
    });
    if (shkKey) shkKey.value = app.vkToDisplay(shk.key);

    // 图片捕获与弹窗行为
    const imageCfg = cfg.image || {};
    const imageEnabledEl = document.getElementById('cfg-imageEnabled');
    if (imageEnabledEl) imageEnabledEl.checked = imageCfg.enabled !== false;
    const imageMaxMBEl = document.getElementById('cfg-imageMaxMB');
    if (imageMaxMBEl) imageMaxMBEl.value = Math.round((imageCfg.maxBytes || 32 * 1024 * 1024) / (1024 * 1024));
    const popupCfg = cfg.popup || {};
    const popupHideConsecutiveDuplicates = document.getElementById('cfg-popupHideConsecutiveDuplicates');
    if (popupHideConsecutiveDuplicates) popupHideConsecutiveDuplicates.checked = !!popupCfg.hideConsecutiveDuplicates;
    const popupKeyOpen = document.getElementById('cfg-popupKeyOpen');
    if (popupKeyOpen) popupKeyOpen.value = popupCfg.keyOpen || 'Enter';
    const popupKeyRestore = document.getElementById('cfg-popupKeyRestore');
    if (popupKeyRestore) popupKeyRestore.value = popupCfg.keyRestore || 'Shift+Enter';
    const popupKeyPaste = document.getElementById('cfg-popupKeyPaste');
    if (popupKeyPaste) popupKeyPaste.value = popupCfg.keyPaste || 'Ctrl+Enter';
    const pauseResumeNotifications = document.getElementById('cfg-pauseResumeNotifications');
    if (pauseResumeNotifications) {
      pauseResumeNotifications.checked = cfg.notifications.pauseResume;
    }
    const notifications = cfg.notifications;
    const notificationFields = {
      'cfg-pauseNotificationTitle': notifications.pauseTitle || '',
      'cfg-pauseNotificationBody': notifications.pauseBody || '',
      'cfg-resumeNotificationTitle': notifications.resumeTitle || '',
      'cfg-resumeNotificationBody': notifications.resumeBody || ''
    };
    Object.entries(notificationFields).forEach(([id, value]) => {
      const field = document.getElementById(id);
      if (field) field.value = value;
    });

    // 安全超时配置
    const lockTimeoutEl = document.getElementById('cfg-lockSessionTimeout');
    const secTimeoutEl = document.getElementById('cfg-secondaryAuthTimeout');
    if (lockTimeoutEl) lockTimeoutEl.value = cfg.security && cfg.security.lockSessionTimeout || 0;
    if (secTimeoutEl) secTimeoutEl.value = cfg.security && cfg.security.secondaryAuthTimeout || 0;

    // 检查过滤规则是否被遮蔽
    const patterns = cfg.sensitive && cfg.sensitive.customPatterns || [];
    const isMasked = patterns.length > 0 && patterns.every(p => p === '__masked__');
    const gate = document.getElementById('filterAuthGate');
    const content = document.getElementById('filterContent');
    if (isMasked) {
      // 规则被遮蔽，显示验证门
      if (gate) gate.classList.remove('hidden');
      if (content) content.classList.add('hidden');
      app.renderPatterns([]);
    } else {
      // 已通过二次认证或无规则，显示规则
      if (gate) gate.classList.add('hidden');
      if (content) content.classList.remove('hidden');
      app.renderPatterns(patterns);
    }

    // 更新密码状态 UI
    app.updatePwdStatusUI();

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
    if (dirScope) dirScope.value = db.scope && db.scope.mode || 'full';
    const dirFullFallbackDays = document.getElementById('cfg-dirBackupFullFallbackDays');
    if (dirFullFallbackDays) dirFullFallbackDays.value = db.fullFallbackDays !== undefined && db.fullFallbackDays !== null ? db.fullFallbackDays : 7;
    if (dirPwd) dirPwd.value = '';
    if (dirPwdClear) dirPwdClear.checked = false;
    const dirHasPwd = db.password === '__set__';
    if (dirPwdClearRow) dirPwdClearRow.style.display = dirHasPwd ? 'flex' : 'none';
    // 目录备份仅支持加密导出：cvexport 直接加密；json/xlsx 打进 zip 后加密。
    // 密码行恒显示，任何格式都需要加密密码。
    if (dirPwdRow) dirPwdRow.style.display = 'flex';
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
    if (emailScope) emailScope.value = eb.scope && eb.scope.mode || 'full';
    const emailMaxPartSize = document.getElementById('cfg-emailBackupMaxPartSize');
    if (emailMaxPartSize) {
      emailMaxPartSize.value = Math.round(eb.maxPartSize / (1024 * 1024));
    }
    const emailFullFallbackDays = document.getElementById('cfg-emailBackupFullFallbackDays');
    if (emailFullFallbackDays) emailFullFallbackDays.value = eb.fullFallbackDays !== undefined && eb.fullFallbackDays !== null ? eb.fullFallbackDays : 7;
    if (emailSubject) emailSubject.value = eb.subject || '';
    if (emailBody) emailBody.value = eb.body || '';
    if (emailStyle) emailStyle.value = eb.style || 'card';
    if (emailAccentColor) emailAccentColor.value = eb.accentColor || '#16A34A';
    if (emailPwd) emailPwd.value = '';
    if (emailPwdClear) emailPwdClear.checked = false;
    const emailHasPwd = eb.password === '__set__';
    if (emailPwdClearRow) emailPwdClearRow.style.display = emailHasPwd ? 'flex' : 'none';
    // 邮件备份仅支持加密导出：任何格式都必须设置加密密码，密码行恒显示。
    if (emailPwdRow) emailPwdRow.style.display = 'flex';

    // 加载备份状态，并在页面打开期间轮询（定时/启动备份的结果实时可见）
    app.loadBackupStatus();
    if (!app._backupStatusTimer) {
      app._backupStatusTimer = setInterval(() => {
        if (!document.hidden) app.loadBackupStatus();
      }, 15000);
    }
  };
}
