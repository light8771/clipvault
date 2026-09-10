import { app } from "./app-context.js";
import { createRemovableModal } from './modal-controller.js';
import { createOperationState } from './operation-state.js';
import { downloadProtectedFile } from './download.js';

const t = window.clipvaultI18n.t;

// 弹窗关闭按钮 SVG（三个对话框共用）。
const closeSvg = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';

export function registerTransfer() {
  app.authedDownload = downloadProtectedFile;
  // ── Export scope dialog (full / date range) ─────────────────
  app.showExportScopeDialog = function (performExport) {
    const html = `
      <div class="modal-overlay" id="exportScopeOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${app.esc(t('transfer.scope_title'))}</span>
            <button class="modal-close" type="button" data-action="close-export-scope" aria-label="${app.esc(t('transfer.scope_close_label'))}">${closeSvg}</button>
          </div>
          <div class="modal-body u-inline-16">
            <div class="u-inline-22">
              <label class="u-inline-23"><input type="radio" name="exportScopeMode" value="full" checked> ${app.esc(t('transfer.scope_all'))}</label>
              <label class="u-inline-24"><input type="radio" name="exportScopeMode" value="range"> ${app.esc(t('transfer.scope_range'))}</label>
            </div>
            <div id="exportRangeFields" class="hidden u-inline-22">
              <input type="date" id="exportFrom" class="lock-input u-inline-25">
              <span class="u-inline-26">${app.esc(t('transfer.range_sep'))}</span>
              <input type="date" id="exportTo" class="lock-input u-inline-27">
            </div>
            <button class="lock-btn" data-action="confirm-export-scope">${app.esc(t('transfer.start_export'))}</button>
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
    const operation = createOperationState(overlay.querySelector('[data-modal-panel]'), overlay.querySelector('.modal-body'));
    app.closeExportScopeDialog = createRemovableModal(overlay, { canClose: () => !operation.busy }).close;
    app.confirmExportScope = async function () {
      if (operation.busy) return;
      const mode = document.querySelector('input[name=exportScopeMode]:checked').value;
      let params = '';
      if (mode === 'range') {
        const from = document.getElementById('exportFrom').value;
        const to = document.getElementById('exportTo').value;
        if (!from || !to) {
          app.toast(t('transfer.need_date_range'), 'err');
          return;
        }
        params = '&mode=range&from=' + encodeURIComponent(from) + '&to=' + encodeURIComponent(to);
      }
      operation.begin(t('transfer.exporting'));
      let ok = false;
      try {
        ok = await performExport(params);
      } catch (err) {
        app.toast(err.message || t('transfer.export_network_error'), 'err');
      } finally {
        operation.finish(ok ? '' : t('transfer.export_retry'), !ok);
      }
      if (ok) app.closeExportScopeDialog();
    };
  };
  app.doExport = async function (type) {
    if (!app.licenseState.licensed) {
      app.toast(t('transfer.paywall_full'), 'err');
      app.openLicenseSettings();
      return;
    }
    const fmt = type === 'data-xlsx' ? 'xlsx' : 'json';
    const fallback = 'clipvault-data.' + fmt;
    const label = t(type === 'data-xlsx' ? 'transfer.export_label_xlsx' : 'transfer.export_label_json');
    if (!window.confirm(t(fmt === 'xlsx' ? 'transfer.plain_confirm_xlsx' : 'transfer.plain_confirm'))) return;
    app.showExportScopeDialog(async scopeParams => {
      const ok = await app.authedDownload('/api/v1/transfer/export?format=' + fmt + scopeParams, fallback);
      if (ok) app.toast(t('transfer.exported_with', { label }));
      return ok;
    });
  };

  // ── Encrypted export (.cvexport) ─────────────────────────────
  app.doExportCvexport = function () {
    if (!app.licenseState.licensed) {
      app.toast(t('transfer.paywall_full'), 'err');
      app.openLicenseSettings();
      return;
    }
    const html = `
      <div class="modal-overlay" id="cvexportOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${app.esc(t('transfer.export_encrypted_btn'))}</span>
            <button class="modal-close" type="button" data-action="close-encrypted-export" aria-label="${app.esc(t('transfer.encrypted_close_label'))}">${closeSvg}</button>
          </div>
          <div class="modal-body u-inline-16">
            <p class="u-inline-28">
              ${app.esc(t('transfer.encrypted_note'))}
            </p>
            <div class="u-inline-22">
              <label class="u-inline-23"><input type="radio" name="cvexportScopeMode" value="full" checked> ${app.esc(t('transfer.scope_all'))}</label>
              <label class="u-inline-24"><input type="radio" name="cvexportScopeMode" value="range"> ${app.esc(t('transfer.scope_range'))}</label>
            </div>
            <div id="cvexportRangeFields" class="hidden u-inline-22">
              <input type="date" id="cvexportFrom" class="lock-input u-inline-25">
              <span>${app.esc(t('transfer.range_sep'))}</span>
              <input type="date" id="cvexportTo" class="lock-input u-inline-27">
            </div>
            <form id="cvexportForm" class="lock-form">
              <input type="password" id="cvexportPwd" class="lock-input" placeholder="${app.esc(t('backup.encryption_password'))}" autocomplete="new-password">
              <input type="password" id="cvexportConfirm" class="lock-input" placeholder="${app.esc(t('auth.pwd_confirm'))}" autocomplete="new-password">
              <button type="submit" class="lock-btn">${app.esc(t('transfer.export_encrypted_btn'))}</button>
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
    const operation = createOperationState(overlay.querySelector('[data-modal-panel]'), overlay.querySelector('.modal-body'));
    app.closeCvexportDialog = createRemovableModal(overlay, { initialFocus: pwdInput, canClose: () => !operation.busy }).close;
    const rangeFields = document.getElementById('cvexportRangeFields');
    document.querySelectorAll('input[name=cvexportScopeMode]').forEach(r => {
      r.addEventListener('change', () => {
        rangeFields.classList.toggle('hidden', r.value !== 'range');
      });
    });
    form.addEventListener('submit', async e => {
      e.preventDefault();
      if (operation.busy) return;
      const pwd = pwdInput.value;
      const confirm = confirmInput.value;
      errEl.classList.add('hidden');
      if (!pwd) {
        errEl.textContent = t('transfer.pwd_required');
        errEl.classList.remove('hidden');
        return;
      }
      if (pwd !== confirm) {
        errEl.textContent = t('auth.pwd_mismatch');
        errEl.classList.remove('hidden');
        return;
      }
      const mode = document.querySelector('input[name=cvexportScopeMode]:checked').value;
      let scopeParams = '';
      if (mode === 'range') {
        const from = document.getElementById('cvexportFrom').value;
        const to = document.getElementById('cvexportTo').value;
        if (!from || !to) {
          errEl.textContent = t('transfer.need_date_range');
          errEl.classList.remove('hidden');
          return;
        }
        scopeParams = '&mode=range&from=' + encodeURIComponent(from) + '&to=' + encodeURIComponent(to);
      }
      operation.begin(t('transfer.exporting'));
      let ok = false;
      try {
        ok = await app.authedDownload('/api/v1/transfer/export?format=cvexport' + scopeParams, 'clipvault-data.cvexport', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ password: pwd })
        });
      } finally {
        operation.finish(ok ? '' : t('transfer.export_retry'), !ok);
      }
      if (ok) {
        pwdInput.value = '';
        confirmInput.value = '';
        app.closeCvexportDialog();
        app.toast(t('transfer.encrypted_done'));
      }
    });
  };

  // ── Import dialog ────────────────────────────────────────────
  app.showImportDialog = function () {
    const html = `
      <div class="modal-overlay" id="importOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${app.esc(t('transfer.import_data_title'))}</span>
            <button class="modal-close" type="button" data-action="close-import" aria-label="${app.esc(t('transfer.import_close_label'))}">${closeSvg}</button>
          </div>
          <div class="modal-body u-inline-16">
            <p class="u-inline-29">
              ${app.esc(t('transfer.import_note'))}
            </p>
            <form id="importForm" class="lock-form">
              <input type="file" id="importFile" accept=".json,.xlsx,.cvexport" class="u-inline-30">
              <input type="password" id="importPwd" class="lock-input u-inline-31" placeholder="${app.esc(t('transfer.import_pwd_placeholder'))}" autocomplete="off">
              <button class="lock-btn" type="submit">${app.esc(t('transfer.start_import'))}</button>
            </form>
            <p class="lock-err hidden u-inline-32" id="importErr"></p>
          </div>
        </div>
      </div>
    `;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('importOverlay');
    const operation = createOperationState(overlay.querySelector('[data-modal-panel]'), overlay.querySelector('.modal-body'));
    overlay.transferOperation = operation;
    app.closeImportDialog = createRemovableModal(overlay, {
      initialFocus: document.getElementById('importFile'), canClose: () => !operation.busy
    }).close;
    document.getElementById('importForm').addEventListener('submit', event => {
      event.preventDefault();
      app.doImport();
    });
  };
  app.doImport = async function doImport() {
    const operation = document.getElementById('importOverlay')?.transferOperation;
    if (!operation || operation.busy) return;
    const fileInput = document.getElementById('importFile');
    const pwdInput = document.getElementById('importPwd');
    const errEl = document.getElementById('importErr');
    errEl.classList.add('hidden');
    if (!fileInput.files || !fileInput.files[0]) {
      errEl.textContent = t('transfer.need_file');
      errEl.classList.remove('hidden');
      return;
    }
    const file = fileInput.files[0];
    const extension = file.name.toLowerCase().split('.').pop();
    const formats = {
      json: 'json',
      xlsx: 'xlsx',
      cvexport: 'cvexport'
    };
    const format = formats[extension];
    if (!format) {
      errEl.textContent = t('transfer.unsupported_format');
      errEl.classList.remove('hidden');
      return;
    }
    const isCvexport = format === 'cvexport';
    const pwd = pwdInput.value;
    if (isCvexport && !pwd) {
      errEl.textContent = t('transfer.encrypted_needs_pwd');
      errEl.classList.remove('hidden');
      return;
    }
    operation.begin(t('transfer.importing'));
    let imported = null;
    try {
      const headers = { 'X-ClipVault-Import-Format': format };
      if (pwd) headers['X-ClipVault-Import-Password'] = pwd;
      const res = await app.protectedFetch('/api/v1/transfer/import', {
        method: 'POST',
        headers,
        body: file
      });
      const d = await app.readImportResponse(res);
      if (res.ok && d.ok) {
        imported = d;
      } else {
        errEl.textContent = d.error || t('transfer.import_failed');
        errEl.classList.remove('hidden');
      }
    } catch (e) {
      errEl.textContent = t('common.network_error') + ': ' + e.message;
      errEl.classList.remove('hidden');
    } finally {
      operation.finish();
    }
    if (imported) {
      pwdInput.value = '';
      app.closeImportDialog();
      app.notifyImportSuccess(imported);
    }
  };
  app.notifyImportSuccess = function notifyImportSuccess(data) {
    if (data.runtimeError) {
      app.toast(t('transfer.import_ok_runtime', { error: data.runtimeError }), 'err');
      return;
    }
    if (data.hotkeyError) {
      app.toast(t('transfer.import_ok_with', { error: data.hotkeyError }), 'err');
      return;
    }
    app.toast(t('transfer.import_ok'));
  };
  app.readImportResponse = async function readImportResponse(res) {
    try {
      return await res.json();
    } catch (_) {}
    return {
      ok: false,
      error: t('transfer.import_request_failed', { status: res.status })
    };
  };
  // 手动备份触发：服务端异步执行，这里只处理"是否成功启动"。
  // 执行进度与结果由状态轮询（loadBackupStatus）展示。
  app.backupAction = async function backupAction(url, startedMsg) {
    if (!(await app.flushPendingConfigSave())) return;
    let res;
    try {
      res = await app.protectedFetch(url, {
        method: 'POST'
      });
    } catch (e) {
      app.toast(t('common.network_error') + ': ' + e.message, 'err');
      return;
    }
    let d = {};
    try {
      d = await res.json();
    } catch (_) {
      app.toast(t('transfer.action_failed_status', { status: res.status }), 'err');
      return;
    }
    if (res.ok && d.ok) {
      app.toast(d.started ? startedMsg : (startedMsg || t('transfer.action_ok')));
      app.loadBackupStatus();
      return;
    }
    const message = d.code === 'license_required' ? t('transfer.backup_needs_license') : d.error || t('transfer.action_failed_status', { status: res.status });
    app.toast(message, 'err');
  };
  app.doBackupDirNow = function () {
    app.backupAction('/api/v1/backup/directory', t('transfer.dir_backup_started'));
  };
  app.doBackupDirFullNow = function () {
    if (!window.confirm(t('transfer.force_full_confirm'))) return;
    app.backupAction('/api/v1/backup/directory?forceFull=1', t('transfer.dir_full_backup_started'));
  };
  app.doBackupEmailNow = function () {
    app.backupAction('/api/v1/backup/email', t('transfer.email_backup_started'));
  };
  app.doBackupEmailFullNow = function () {
    if (!window.confirm(t('transfer.force_full_confirm'))) return;
    app.backupAction('/api/v1/backup/email?forceFull=1', t('transfer.email_full_backup_started'));
  };
  app.doBackupEmailTest = function () {
    app.backupAction('/api/v1/backup/email-test', t('transfer.test_email_sent'));
  };
  // localizeBackupError 翻译备份 runner 写入的错误文本：整体词条 key 直接
  // 翻译；「key: 原始详情」形态翻译前缀；纯文本（历史数据/原始错误）原样。
  function localizeBackupError(raw) {
    if (typeof raw !== 'string' || !raw) return raw;
    if (/^[a-z0-9_.]+$/.test(raw)) return t(raw);
    const colonIdx = raw.indexOf(': ');
    if (colonIdx > 0 && /^[a-z0-9_.]+$/.test(raw.slice(0, colonIdx))) {
      return t(raw.slice(0, colonIdx)) + raw.slice(colonIdx);
    }
    return raw;
  }

  app.formatBackupStatus = function formatBackupStatus(s) {
    if (!s) return '';
    const triggerKey = { scheduled: 'transfer.trigger_scheduled', startup: 'transfer.trigger_startup', manual: 'transfer.trigger_manual' }[s.trigger];
    const triggerLabel = triggerKey ? t(triggerKey) : '';
    if (s.running) {
      return '🔄 ' + t('transfer.status_running', { trigger: triggerLabel || t('transfer.trigger_unknown') });
    }
    if (!s.lastTime || s.lastTime === '0001-01-01T00:00:00Z') return '';
    const time = new Date(s.lastTime).toLocaleString(window.clipvaultI18n.locale());
    const via = triggerLabel ? triggerLabel + t('transfer.trigger_suffix') + ' · ' : '';
    if (!s.lastOK) {
      // lastError 是 key/前缀拼接文本（备份 runner 无 UI 上下文，写入 key，
      // 展示时翻译：整体命中词条则整体替换，否则按「前缀 key: 详情」拆分翻译）。
      const rawError = s.lastError || t('transfer.unknown_error');
      const localizedError = localizeBackupError(rawError);
      return '❌ ' + t('transfer.status_failed', { via: via, time: time, error: localizedError });
    }
    if (s.skipped) {
      return '⏭ ' + t('transfer.status_skipped', { via: via, time: time });
    }
    let modeLabel;
    if (s.actualMode === 'incremental') {
      modeLabel = t('backup.scope_incremental');
    } else if (s.actualMode === 'range') {
      modeLabel = t('transfer.mode_range');
    } else {
      modeLabel = s.autoFallback ? t('transfer.mode_full_fallback') : t('backup.scope_full');
    }
    let detail = '';
    if (s.actualMode === 'incremental') {
      if (s.fullFallbackDays > 0 && s.nextFullDue) {
        detail = ' · ' + t('transfer.next_full_due', { time: new Date(s.nextFullDue).toLocaleString(window.clipvaultI18n.locale()) });
      } else if (s.fullFallbackDays > 0) {
        detail = ' · ' + t('transfer.next_full_first');
      } else {
        detail = ' · ' + t('transfer.no_fallback_hint');
      }
    }
    const icon = s.autoFallback ? '⚠️' : '✅';
    const file = s.backupFile ? ' · ' + s.backupFile : '';
    return icon + ' ' + modeLabel + ' · ' + via + time + file + detail;
  };
  app.loadBackupStatus = async function loadBackupStatus() {
    try {
      const d = await app.fetch('/api/v1/backup').then(r => r.json());
      const ds = d.dirStatus;
      const es = d.emailStatus;
      if (ds) {
        const row = document.getElementById('cfg-dirBackupStatusRow');
        const text = document.getElementById('cfg-dirBackupStatusText');
        const msg = app.formatBackupStatus(ds);
        if (row) row.style.display = msg ? 'flex' : 'none';
        if (text) text.textContent = msg;
      }
      if (es) {
        const row = document.getElementById('cfg-emailBackupStatusRow');
        const text = document.getElementById('cfg-emailBackupStatusText');
        const msg = app.formatBackupStatus(es);
        if (row) row.style.display = msg ? 'flex' : 'none';
        if (text) text.textContent = msg;
      }
    } catch (_) {}
  };
  // 通用备份历史（目录/邮件共用），含成功、失败与跳过记录。
  app.showBackupHistory = async function (kind) {
    const label = t(kind === 'dir' ? 'transfer.dir_label' : 'transfer.email_label');
    try {
      const res = await app.protectedFetch('/api/v1/backup/history?kind=' + encodeURIComponent(kind));
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || ('HTTP ' + res.status));
      app.renderBackupHistory(kind, data.history || []);
    } catch (e) {
      app.toast(t('transfer.history_load_failed', { label: label, message: e.message || e }), 'err');
    }
  };
  app.showEmailBackupHistory = function () {
    app.showBackupHistory('email');
  };
  app.renderBackupHistory = function renderBackupHistory(kind, history) {
    document.getElementById('backupHistoryOverlay')?.remove();
    const label = t(kind === 'dir' ? 'transfer.dir_label' : 'transfer.email_label');
    const triggerMap = { scheduled: 'transfer.trigger_scheduled', startup: 'transfer.trigger_startup', manual: 'transfer.trigger_manual' };
    const modeMap = { full: 'backup.scope_full', incremental: 'backup.scope_incremental', range: 'transfer.mode_range', skipped: 'transfer.mode_skipped' };
    const formatLabel = { cvexport: 'cvexport', json: 'JSON', xlsx: 'Excel' };
    const rows = history.map(record => {
      const time = new Date(record.createdAt).toLocaleString(window.clipvaultI18n.locale());
      const trigger = triggerMap[record.trigger] ? t(triggerMap[record.trigger]) : record.trigger || '-';
      let result;
      if (record.ok) {
        const mode = modeMap[record.mode] ? t(modeMap[record.mode]) : record.mode || '-';
        const parts = record.parts > 1 ? t('transfer.parts_count', { count: record.parts }) + ' · ' : '';
        const items = record.mode === 'skipped' ? '' : parts + t('transfer.item_count_simple', { count: record.items || 0 });
        result = '✅ ' + mode + (items ? ' · ' + items : '');
      } else {
        result = '❌ ' + (record.error || t('transfer.failed_short'));
      }
      const format = formatLabel[record.format] || record.format || '-';
      const target = record.mode === 'skipped' ? '' : (record.target || '');
      return `<tr><td>${app.esc(time)}</td><td>${app.esc(trigger)}</td><td>${app.esc(result)}</td><td>${app.esc(format)}</td><td title="${app.esc(record.target || '')}">${app.esc(target)}</td></tr>`;
    }).join('');
    const content = rows || '<tr><td colspan="5" class="backup-history-empty">' + app.esc(t('transfer.history_empty', { label: label })) + '</td></tr>';
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.id = 'backupHistoryOverlay';
    overlay.innerHTML = `
      <div class="modal-box backup-history-modal" role="dialog" aria-modal="true" aria-labelledby="backupHistoryTitle" data-modal-panel>
        <div class="modal-head">
          <span class="modal-title" id="backupHistoryTitle">${app.esc(label)}${app.esc(t('transfer.history_title_suffix'))}</span>
          <button class="modal-close" type="button" aria-label="${app.esc(t('common.close'))}">&times;</button>
        </div>
        <div class="modal-body backup-history-body">
          <table class="backup-history-table">
            <thead><tr><th>${app.esc(t('mcp.col_time'))}</th><th>${app.esc(t('transfer.trigger_col'))}</th><th>${app.esc(t('mcp.col_result'))}</th><th>${app.esc(t('transfer.format_col'))}</th><th>${app.esc(t('transfer.target_col'))}</th></tr></thead>
            <tbody>${content}</tbody>
          </table>
        </div>
      </div>`;
    document.body.appendChild(overlay);
    const close = createRemovableModal(overlay).close;
    overlay.querySelector('.modal-close').addEventListener('click', close);
  };
}
