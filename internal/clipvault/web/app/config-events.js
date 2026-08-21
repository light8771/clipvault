import { app } from "./app-context.js";

const t = window.clipvaultI18n.t;

export function registerConfigEvents() {
  app.setupAutoSave = function setupAutoSave() {
    const settingsEl = document.getElementById('viewSettings');
    if (app.autoSaveBound) return;
    app.autoSaveBound = true;
    const listenAddrEl = document.getElementById('cfg-listenAddr');
    const timeZoneEl = document.getElementById('cfg-timeZone');
    const brandTitleEl = document.getElementById('cfg-brandTitle');
    brandTitleEl?.addEventListener('input', () => app.applyBrand({
      ...app.currentCfg?.brand,
      title: brandTitleEl.value
    }));
    ['logo', 'favicon'].forEach(kind => {
      const fileInput = document.getElementById(`cfg-brand${kind === 'logo' ? 'Logo' : 'Favicon'}File`);
      document.getElementById(`brand${kind === 'logo' ? 'Logo' : 'Favicon'}UploadBtn`)?.addEventListener('click', () => fileInput?.click());
      document.getElementById(`brand${kind === 'logo' ? 'Logo' : 'Favicon'}ResetBtn`)?.addEventListener('click', () => app.resetBrandAsset(kind));
      fileInput?.addEventListener('change', () => {
        app.saveBrandAsset(kind, fileInput.files?.[0]);
        fileInput.value = '';
      });
    });
    const backgroundFileInput = document.getElementById('cfg-backgroundMediaFile');
    document.getElementById('backgroundMediaUploadBtn')?.addEventListener('click', () => backgroundFileInput?.click());
    document.getElementById('backgroundMediaResetBtn')?.addEventListener('click', app.resetBackgroundMedia);
    backgroundFileInput?.addEventListener('change', () => {
      app.saveBackgroundMedia(backgroundFileInput.files?.[0]);
      backgroundFileInput.value = '';
    });
    document.querySelectorAll('[id^="cfg-background"][type="range"]').forEach(control => {
      control.addEventListener('input', () => {
        app.updateBackgroundOutputs(app.backgroundFromForm());
        app.applyBackground(app.backgroundFromForm());
      });
    });
    document.getElementById('cfg-backgroundFit')?.addEventListener('change', () => {
      app.applyBackground(app.backgroundFromForm());
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
        app.applyTheme(app.themeFromForm());
        app.scheduleConfigSave(150);
      });
    });
    ['cfg-themeMode', 'cfg-themePrimary', 'cfg-themeAccent'].forEach(id => {
      const control = document.getElementById(id);
      if (!control) return;
      control.addEventListener('input', () => app.applyTheme(app.themeFromForm()));
      control.addEventListener('change', () => app.applyTheme(app.themeFromForm()));
    });
    const fontScale = document.getElementById('cfg-themeFontScale');
    fontScale?.addEventListener('input', () => {
      const output = document.getElementById('cfg-themeFontScaleOutput');
      if (output) output.value = `${fontScale.value}%`;
      app.applyTheme(app.themeFromForm());
    });
    const surfaceOpacityControl = document.getElementById('cfg-themeSurfaceOpacity');
    surfaceOpacityControl?.addEventListener('input', () => {
      const output = document.getElementById('cfg-themeSurfaceOpacityOutput');
      if (output) output.value = `${surfaceOpacityControl.value}%`;
      app.applyTheme(app.themeFromForm());
    });
    // 布局与导航栏：实时预览（自动保存由下方 settingsEl 委托事件统一触发）
    const navOpacityControl = document.getElementById('cfg-navOpacity');
    const navOpacityFollow = document.getElementById('cfg-navOpacityFollow');
    const navOpacityOutput = document.getElementById('cfg-navOpacityOutput');
    navOpacityControl?.addEventListener('input', () => {
      if (navOpacityOutput) navOpacityOutput.value = `${navOpacityControl.value}%`;
      app.applyTheme(app.themeFromForm());
    });
    navOpacityFollow?.addEventListener('change', () => {
      if (navOpacityControl) navOpacityControl.disabled = navOpacityFollow.checked;
      if (navOpacityOutput) navOpacityOutput.value = navOpacityFollow.checked ? t('appearance.follow') : `${navOpacityControl?.value || 100}%`;
      app.applyTheme(app.themeFromForm());
    });
    const navBlurControl = document.getElementById('cfg-navBlur');
    navBlurControl?.addEventListener('input', () => {
      const output = document.getElementById('cfg-navBlurOutput');
      if (output) output.value = `${navBlurControl.value}px`;
      app.applyTheme(app.themeFromForm());
    });
    ['cfg-navBorder', 'cfg-navSticky', 'cfg-historyLayout'].forEach(id => {
      document.getElementById(id)?.addEventListener('change', () => app.applyTheme(app.themeFromForm()));
    });
    // 首页设置搜索框不是配置项，输入不得触发自动保存。
    const isExcluded = target => target === listenAddrEl || target === timeZoneEl
      || target?.id === 'settingsSearch' || target?.type === 'file';
    settingsEl.addEventListener('input', e => {
      if (isExcluded(e.target)) return;
      app.scheduleConfigSave(800);
    });
    settingsEl.addEventListener('change', e => {
      if (isExcluded(e.target)) return;
      app.scheduleConfigSave(300);
    });
    // 监听地址失焦时立即保存（触发重启确认）
    if (listenAddrEl) {
      listenAddrEl.addEventListener('input', app.updateListenAddressWarning);
      listenAddrEl.addEventListener('blur', () => {
        app.scheduleConfigSave(0);
      });
    }
    if (timeZoneEl) {
      timeZoneEl.addEventListener('blur', () => app.scheduleConfigSave(0));
    }
    // 过滤规则增删也触发保存
    const addBtn = document.getElementById('addPatternBtn');
    if (addBtn) {
      addBtn.addEventListener('click', () => {
        app.scheduleConfigSave(300);
      });
    }
    // 目录备份仅支持加密导出：cvexport 直接加密；json/xlsx 打进 zip 后加密。
    // 密码行恒显示，任何格式都需要加密密码。
    const dirFmt = document.getElementById('cfg-dirBackupFormat');
    if (dirFmt) {
      dirFmt.addEventListener('change', () => {
        const row = document.getElementById('cfg-dirBackupPwdRow');
        if (row) row.style.display = 'flex';
      });
    }
    // 邮件备份仅支持加密导出：cvexport 直接加密；json/xlsx 打进 zip 后加密。
    // 密码行恒显示，任何格式都需要加密密码。
    const emailFmt = document.getElementById('cfg-emailBackupFormat');
    if (emailFmt) {
      emailFmt.addEventListener('change', () => {
        const row = document.getElementById('cfg-emailBackupPwdRow');
        if (row) row.style.display = 'flex';
      });
    }
    document.getElementById('storageOpenDirBtn')?.addEventListener('click', app.openDataDirectory);
    document.getElementById('storageRelocateBtn')?.addEventListener('click', app.openMigrateDialog);
    const diagnosticsButton = document.getElementById('diagnosticsExportBtn');
    diagnosticsButton?.addEventListener('click', async () => {
      if (app.diagnosticsExportInFlight) return;
      app.diagnosticsExportInFlight = true;
      diagnosticsButton.disabled = true;
      try {
        const ok = await app.authedDownload('/api/v1/diagnostics', 'clipvault-diagnostics.zip');
        if (ok) app.toast(t('maintenance.logs_exported'));
      } finally {
        app.diagnosticsExportInFlight = false;
        diagnosticsButton.disabled = false;
      }
    });
    const dirEnabledEl = document.getElementById('cfg-dirBackupEnabled');
    if (dirEnabledEl) {
      dirEnabledEl.addEventListener('click', e => {
        if (!dirEnabledEl.checked) return;
        const missing = app.validateDirBackupConfig();
        if (missing.length) {
          e.preventDefault();
          app.toast(t('backup.complete_config_first', { items: missing.join('、') }), 'err');
        }
      });
    }
    const emailEnabledEl = document.getElementById('cfg-emailBackupEnabled');
    if (emailEnabledEl) {
      emailEnabledEl.addEventListener('click', e => {
        if (!emailEnabledEl.checked) return;
        const missing = app.validateEmailBackupConfig();
        if (missing.length) {
          e.preventDefault();
          app.toast(t('backup.complete_config_first', { items: missing.join('、') }), 'err');
        }
      });
    }
  };
  app.backupPwdValue = function backupPwdValue(pwdId, clearId) {
    const pwd = document.getElementById(pwdId)?.value || '';
    if (pwd) return pwd;
    const clearEl = document.getElementById(clearId);
    if (clearEl && clearEl.checked) return '__clear__';
    return '';
  };
  app.smtpPasswordValue = function smtpPasswordValue() {
    const user = (document.getElementById('cfg-emailBackupSMTPUser')?.value || '').trim();
    const password = document.getElementById('cfg-emailBackupSMTPPass')?.value || '';
    if (password) return password;
    if (!user) return '__clear__';
    return '';
  };
  app.validateDirBackupConfig = function validateDirBackupConfig() {
    const missing = [];
    const interval = parseInt(document.getElementById('cfg-dirBackupInterval')?.value) || 0;
    const dir = (document.getElementById('cfg-dirBackupDir')?.value || '').trim();
    const pwd = document.getElementById('cfg-dirBackupPwd')?.value || '';
    const clearChecked = document.getElementById('cfg-dirBackupPwdClear')?.checked || false;
    const hasExistingPwd = app.currentCfg?.backup?.dirBackup?.password === '__set__';
    if (interval < 1 || interval > 720) missing.push(t('backup.missing_interval'));
    if (!dir) missing.push(t('backup.missing_target_dir'));
    // 目录备份仅支持加密导出（json/xlsx 为 zip 加密打包），任何格式都需要加密密码。
    // 有新输入用新密码；无新输入且勾选清除则为空；无新输入未勾选则依赖已存密码
    const effectivePwd = pwd || (clearChecked ? '' : hasExistingPwd ? '__set__' : '');
    if (!effectivePwd) missing.push(t('backup.missing_encryption_pwd'));
    return missing;
  };
  app.validateEmailBackupConfig = function validateEmailBackupConfig() {
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
    const eb = app.currentCfg?.backup?.emailBackup || {};
    const hasExistingSmtpPass = eb.smtpPass === '__set__';
    const hasExistingPwd = eb.password === '__set__';
    if (interval < 1 || interval > 720) missing.push(t('backup.missing_interval'));
    if (!host) missing.push(t('backup.missing_smtp_host'));
    if (port <= 0 || port > 65535) missing.push(t('backup.missing_smtp_port'));
    const effectiveSmtpPass = pass || (user && hasExistingSmtpPass ? '__set__' : '');
    if (Boolean(user) !== Boolean(effectiveSmtpPass)) {
      missing.push(t('backup.missing_smtp_user_pass_pair'));
    }
    if (user && !document.getElementById('cfg-emailBackupSMTPUser')?.validity.valid) {
      missing.push(t('backup.missing_smtp_user_email'));
    }
    if (!from || !document.getElementById('cfg-emailBackupFrom')?.validity.valid) missing.push(t('backup.missing_from'));
    if (!to || !document.getElementById('cfg-emailBackupTo')?.validity.valid) missing.push(t('backup.missing_to'));
    if (!subject.trim() || /[\r\n]/.test(subject) || Array.from(subject).length > 200) missing.push(t('backup.missing_subject'));
    if (Array.from(body).length > 5000) missing.push(t('backup.missing_body'));
    if (!['plain', 'card', 'accent'].includes(style)) missing.push(t('backup.missing_style'));
    if (!/^#[0-9A-Fa-f]{6}$/.test(accentColor)) missing.push(t('backup.missing_accent_color'));
    // 邮件备份仅支持加密导出（json/xlsx 为 zip 加密打包），任何格式都需要加密密码
    const effectivePwd = pwd || (clearChecked ? '' : hasExistingPwd ? '__set__' : '');
    if (!effectivePwd) missing.push(t('backup.missing_encryption_pwd'));
    return missing;
  };
}
