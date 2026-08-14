import { app } from "./app-context.js";
import { createRemovableModal } from './modal-controller.js';
export function registerTransfer() {
  app.authedDownload = async function authedDownload(baseUrl, fallbackFilename, requestInit) {
    try {
      const res = await app.protectedFetch(baseUrl, requestInit);
      if (res.status === 402) {
        // 付费墙：未激活
        try {
          const d = await res.json();
          app.toast(d.error || '导出为付费功能，请先激活', 'err');
        } catch (_) {
          app.toast('导出为付费功能，请先激活', 'err');
        }
        app.openLicenseSettings();
        return false;
      }
      if (!res.ok) {
        app.toast('导出失败: ' + res.status, 'err');
        return false;
      }
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
    } catch (_) {
      app.toast('导出失败: 网络错误', 'err');
      return false;
    }
  };
  // ── Export scope dialog (full / date range) ─────────────────
  app.showExportScopeDialog = function (performExport) {
    const html = `
      <div class="modal-overlay" id="exportScopeOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">选择导出范围</span>
            <button class="modal-close" type="button" data-action="close-export-scope" aria-label="关闭导出范围">
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
    app.closeExportScopeDialog = createRemovableModal(overlay).close;
    app.confirmExportScope = function () {
      const mode = document.querySelector('input[name=exportScopeMode]:checked').value;
      let params = '';
      if (mode === 'range') {
        const from = document.getElementById('exportFrom').value;
        const to = document.getElementById('exportTo').value;
        if (!from || !to) {
          app.toast('请选择起止日期', 'err');
          return;
        }
        params = '&mode=range&from=' + encodeURIComponent(from) + '&to=' + encodeURIComponent(to);
      }
      app.closeExportScopeDialog();
      performExport(params);
    };
  };
  app.doExport = async function (type) {
    if (!app.licenseState.licensed) {
      app.toast('导出为付费功能，激活后可导出全部数据', 'err');
      app.openLicenseSettings();
      return;
    }
    const fmt = type === 'data-xlsx' ? 'xlsx' : 'json';
    const fallback = 'clipvault-data.' + fmt;
    const label = '剪切板记录（' + (type === 'data-xlsx' ? 'Excel' : 'JSON') + '）';
    if (fmt === 'xlsx') {
      if (!window.confirm('即将导出明文 Excel 数据。任何能访问导出文件的人都可以查看其中内容。是否继续？')) return;
      app.showExportScopeDialog(function (scopeParams) {
        const baseUrl = '/api/v1/transfer/export?format=xlsx' + scopeParams;
        app.authedDownload(baseUrl, fallback).then(ok => {
          if (ok) app.toast('已导出 ' + label);
        });
      });
    } else {
      if (!window.confirm('即将导出明文剪贴板数据。任何能访问导出文件的人都可以查看其中内容。是否继续？')) return;
      app.showExportScopeDialog(function (scopeParams) {
        const baseUrl = '/api/v1/transfer/export?format=json' + scopeParams;
        app.authedDownload(baseUrl, fallback).then(ok => {
          if (ok) app.toast('已导出 ' + label);
        });
      });
    }
  };

  // ── Encrypted export (.cvexport) ─────────────────────────────
  app.doExportCvexport = function () {
    if (!app.licenseState.licensed) {
      app.toast('导出为付费功能，激活后可导出全部数据', 'err');
      app.openLicenseSettings();
      return;
    }
    const html = `
      <div class="modal-overlay" id="cvexportOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">加密导出</span>
            <button class="modal-close" type="button" data-action="close-encrypted-export" aria-label="关闭加密导出">
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
    app.closeCvexportDialog = createRemovableModal(overlay, { initialFocus: pwdInput }).close;
    const rangeFields = document.getElementById('cvexportRangeFields');
    document.querySelectorAll('input[name=cvexportScopeMode]').forEach(r => {
      r.addEventListener('change', () => {
        rangeFields.classList.toggle('hidden', r.value !== 'range');
      });
    });
    form.addEventListener('submit', async e => {
      e.preventDefault();
      const pwd = pwdInput.value;
      const confirm = confirmInput.value;
      errEl.classList.add('hidden');
      if (!pwd) {
        errEl.textContent = '密码不能为空';
        errEl.classList.remove('hidden');
        return;
      }
      if (pwd !== confirm) {
        errEl.textContent = '两次输入的密码不一致';
        errEl.classList.remove('hidden');
        return;
      }
      const mode = document.querySelector('input[name=cvexportScopeMode]:checked').value;
      let scopeParams = '';
      if (mode === 'range') {
        const from = document.getElementById('cvexportFrom').value;
        const to = document.getElementById('cvexportTo').value;
        if (!from || !to) {
          errEl.textContent = '请选择起止日期';
          errEl.classList.remove('hidden');
          return;
        }
        scopeParams = '&mode=range&from=' + encodeURIComponent(from) + '&to=' + encodeURIComponent(to);
      }
      const ok = await app.authedDownload('/api/v1/transfer/export?format=cvexport' + scopeParams, 'clipvault-data.cvexport', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          password: pwd
        })
      });
      pwdInput.value = '';
      confirmInput.value = '';
      app.closeCvexportDialog();
      if (ok) app.toast('已加密导出');
    });
  };

  // ── Import dialog ────────────────────────────────────────────
  app.showImportDialog = function () {
    const html = `
      <div class="modal-overlay" id="importOverlay">
        <div class="modal-box secondary-box" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">导入数据</span>
            <button class="modal-close" type="button" data-action="close-import" aria-label="关闭导入">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <p class="u-inline-29">
              选择 JSON、Excel 或加密 .cvexport 文件导入。导入为追加模式：不会清空现有数据，也不做去重，重复导入同一文件会产生重复记录。加密文件需输入密码。提示：Excel 文件较大时解析慢且占内存，大数据量建议用 JSON 或加密 .cvexport 格式。
            </p>
            <form id="importForm" class="lock-form">
              <input type="file" id="importFile" accept=".json,.xlsx,.cvexport" class="u-inline-30">
              <input type="password" id="importPwd" class="lock-input u-inline-31" placeholder="解密密码（仅 .cvexport 需要）" autocomplete="off">
              <button class="lock-btn" type="submit">开始导入</button>
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
    app.closeImportDialog = createRemovableModal(overlay, {
      initialFocus: document.getElementById('importFile')
    }).close;
    document.getElementById('importForm').addEventListener('submit', event => {
      event.preventDefault();
      app.doImport();
    });
  };
  app.doImport = async function doImport() {
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
    const formats = {
      json: 'json',
      xlsx: 'xlsx',
      cvexport: 'cvexport'
    };
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
      const headers = {
        'X-ClipVault-Import-Format': format
      };
      if (pwd) headers['X-ClipVault-Import-Password'] = pwd;
      const res = await app.protectedFetch('/api/v1/transfer/import', {
        method: 'POST',
        headers,
        body: file
      });
      const d = await app.readImportResponse(res);
      if (d.ok) {
        app.closeImportDialog();
        app.notifyImportSuccess(d);
      } else {
        errEl.textContent = d.error || '导入失败';
        errEl.classList.remove('hidden');
      }
    } catch (e) {
      errEl.textContent = '网络错误: ' + e.message;
      errEl.classList.remove('hidden');
    }
  };
  app.notifyImportSuccess = function notifyImportSuccess(data) {
    if (data.runtimeError) {
      app.toast('导入成功，但部分运行时设置需重启后生效：' + data.runtimeError, 'err');
      return;
    }
    if (data.hotkeyError) {
      app.toast('导入成功，' + data.hotkeyError, 'err');
      return;
    }
    app.toast('导入成功');
  };
  app.readImportResponse = async function readImportResponse(res) {
    try {
      return await res.json();
    } catch (_) {}
    return {
      ok: false,
      error: `\u5bfc\u5165\u8bf7\u6c42\u5931\u8d25 (${res.status})`
    };
  };
  app.backupAction = async function backupAction(url, successMsg) {
    if (!(await app.flushPendingConfigSave())) return;
    try {
      const res = await app.protectedFetch(url, {
        method: 'POST'
      });
      const d = await res.json();
      if (d.ok) {
        app.toast(successMsg);
        app.loadBackupStatus();
      } else {
        const message = d.code === 'license_required' ? '需要先激活 ClipVault 才能使用备份功能' : d.error || '操作失败';
        app.toast(message, 'err');
      }
    } catch (e) {
      app.toast('网络错误: ' + e.message, 'err');
    }
  };
  app.doBackupDirNow = function () {
    app.backupAction('/api/v1/backup/directory', '目录备份成功');
  };
  app.doBackupDirFullNow = function () {
    if (!window.confirm('将执行一次全量备份并重置增量链。是否继续？')) return;
    app.backupAction('/api/v1/backup/directory?forceFull=1', '目录全量备份成功（增量链已重置）');
  };
  app.doBackupEmailNow = function () {
    app.backupAction('/api/v1/backup/email', '邮件备份已发送');
  };
  app.doBackupEmailFullNow = function () {
    if (!window.confirm('将执行一次全量备份并重置增量链。是否继续？')) return;
    app.backupAction('/api/v1/backup/email?forceFull=1', '邮件全量备份已发送（增量链已重置）');
  };
  app.doBackupEmailTest = function () {
    app.backupAction('/api/v1/backup/email-test', '测试邮件已发送');
  };
  app.formatBackupStatus = function formatBackupStatus(s) {
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
        if (row) row.style.display = msg ? '' : 'none';
        if (text) text.textContent = msg;
      }
      if (es) {
        const row = document.getElementById('cfg-emailBackupStatusRow');
        const text = document.getElementById('cfg-emailBackupStatusText');
        const msg = app.formatBackupStatus(es);
        if (row) row.style.display = msg ? '' : 'none';
        if (text) text.textContent = msg;
      }
    } catch (_) {}
  };
  app.showEmailBackupHistory = async function () {
    try {
      const res = await app.protectedFetch('/api/v1/backup/email-history');
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || '读取发送历史失败');
      app.renderEmailBackupHistory(data.history || []);
    } catch (e) {
      app.toast('读取发送历史失败：' + (e.message || e), 'err');
    }
  };
  app.renderEmailBackupHistory = function renderEmailBackupHistory(history) {
    document.getElementById('emailBackupHistoryOverlay')?.remove();
    const rows = history.map(record => {
      const mode = record.mode === 'manual' ? '手动' : '自动';
      const format = {
        cvexport: '加密 cvexport',
        json: 'JSON',
        xlsx: 'Excel'
      }[record.format] || record.format;
      const time = new Date(record.sentAt).toLocaleString('zh-CN');
      return `<tr><td>${app.esc(mode)}</td><td>${app.esc(record.from)}</td><td>${app.esc(record.to)}</td><td>${app.esc(time)}</td><td>${app.esc(format)}</td></tr>`;
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
    document.body.appendChild(overlay);
    const close = createRemovableModal(overlay).close;
    overlay.querySelector('.modal-close').addEventListener('click', close);
  };
}
