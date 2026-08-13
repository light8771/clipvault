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
      const res = await fetch('/api/v1/system/restart', { method: 'POST' });
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
        const res = await fetch('/api/v1/session');
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
        const ok = await authedDownload('/api/v1/diagnostics', 'clipvault-diagnostics.zip');
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
  showExportScopeDialog = function(performExport) {
    const html = `
      <div class="modal-overlay" id="exportScopeOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">选择导出范围</span>
            <button class="modal-close" data-action="close-export-scope">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <div class="u-inline-22">
              <label class="u-inline-23"><input type="radio" name="exportScopeMode" value="full" checked> 全部记录</label>
              <label class="u-inline-24"><input type="radio" name="exportScopeMode" value="range"> 按日期范围</label>
            </div>
            <div id="exportRangeFields" class="hidden u-inline-22">
              <input type="date" id="exportFrom" class="lock-input u-inline-25">
              <span class="u-inline-26">至</span>
              <input type="date" id="exportTo" class="lock-input u-inline-27">
            </div>
            <button class="lock-btn" data-action="confirm-export-scope">开始导出</button>
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
    closeExportScopeDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', (e) => { if (e.target === overlay) closeExportScopeDialog(); });
    confirmExportScope = function() {
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

  doExport = async function(type) {
    if (!licenseState.licensed) { toast('导出为付费功能，激活后可导出全部数据', 'err'); openLicenseSettings(); return; }
    const fmt = type === 'data-xlsx' ? 'xlsx' : 'json';
    const fallback = 'clipvault-data.' + fmt;
    const label = '剪切板记录（' + (type === 'data-xlsx' ? 'Excel' : 'JSON') + '）';
    if (fmt === 'xlsx') {
	  if (!window.confirm('即将导出明文 Excel 数据。任何能访问导出文件的人都可以查看其中内容。是否继续？')) return;
	  showExportScopeDialog(function(scopeParams) {
		const baseUrl = '/api/v1/transfer/export?format=xlsx' + scopeParams;
		authedDownload(baseUrl, fallback).then(ok => { if (ok) toast('已导出 ' + label); });
	  });
    } else {
      if (!window.confirm('即将导出明文剪贴板数据。任何能访问导出文件的人都可以查看其中内容。是否继续？')) return;
      showExportScopeDialog(function(scopeParams) {
        const baseUrl = '/api/v1/transfer/export?format=json' + scopeParams;
        authedDownload(baseUrl, fallback).then(ok => { if (ok) toast('已导出 ' + label); });
      });
    }
  };

  // ── Encrypted export (.cvexport) ─────────────────────────────
  doExportCvexport = function() {
    if (!licenseState.licensed) { toast('导出为付费功能，激活后可导出全部数据', 'err'); openLicenseSettings(); return; }
    const html = `
      <div class="modal-overlay" id="cvexportOverlay">