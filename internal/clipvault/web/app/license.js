import { app } from "./app-context.js";

const t = window.clipvaultI18n.t;

export function registerLicense() {
  app.licenseState = {
    licensed: true,
    enforced: true,
    status: 'loading',
    machineCode: '',
    version: '',
    freeLimit: 0
  };
  app.fullMachineId = '';
  app.normalizeLicenseState = function normalizeLicenseState(d) {
    const raw = Number(d && d.freeLimit);
    const freeLimit = Number.isFinite(raw) && raw > 0 ? Math.floor(raw) : 0;
    return Object.assign({}, d, {
      freeLimit
    });
  };
  app.loadLicenseStatus = async function loadLicenseStatus() {
    try {
      const d = await app.fetch('/api/v1/license').then(r => r.json());
      app.licenseState = app.normalizeLicenseState(d);
      const banner = document.getElementById('licenseBanner');
      const badge = document.getElementById('licenseStatusBadge');
      const hint = document.getElementById('licenseStatusHint');
      const code = document.getElementById('licenseMachineCode');
      const activateBlock = document.getElementById('licenseActivateBlock');
      const details = document.getElementById('licenseDetailsRow');
      badge?.classList.toggle('warn', !d.licensed);
      if (d.licensed && badge && hint) {
        badge.textContent = t('license.badge_licensed');
        hint.textContent = t('license.hint_licensed');
      } else if (badge && hint) {
        badge.textContent = t('license.badge_unlicensed');
        hint.textContent = app.licenseStatusText(d.status);
      }
      banner?.classList.toggle('hidden', d.licensed);
      activateBlock?.classList.toggle('hidden', d.licensed);
      details?.classList.toggle('hidden', !d.licensed);
      if (!d.licensed && banner) {
        // 限额未知时退化为不带数字的说法，但保留横幅本身（它同时是激活入口）。
        const limit = app.licenseState.freeLimit;
        banner.querySelector('span').textContent = limit > 0
          ? t('nav.free_tier_banner_limited', { count: limit })
          : t('nav.free_tier_banner');
      }
      const licenseID = document.getElementById('licenseIdText');
      if (d.licenseId && licenseID) licenseID.textContent = d.licenseId;
      if (d.machineCode && code) code.value = d.machineCode;
      if (d.enforced !== false) await app.loadMachineID();
      if (app.refreshState) await app.refreshState();
    } catch (_) {
      const badge = document.getElementById('licenseStatusBadge');
      const hint = document.getElementById('licenseStatusHint');
      if (badge) badge.textContent = t('license.read_failed');
      if (hint) hint.textContent = t('license.read_failed_hint');
    }
  };
  app.licenseStatusText = function licenseStatusText(status) {
    if (status === 'machine_mismatch') return t('license.status_machine_mismatch');
    if (status === 'corrupt') return t('license.status_corrupt');
    if (status === 'machine_error') return t('license.status_machine_error');
    if (status === 'unconfigured') return t('license.status_unconfigured');
    return t('license.status_unlicensed');
  };
  app.loadMachineID = async function loadMachineID() {
    try {
      const d = await app.fetch('/api/v1/license/machine').then(r => r.json());
      app.fullMachineId = d.machineId || '';
      const code = document.getElementById('licenseMachineCode');
      if (d.machineCode && code) code.value = d.machineCode;
    } catch (_) {}
  };
  app.openLicenseSettings = async function () {
    if (window.location.pathname === '/license') return;
    window.location.href = '/license';
  };
  app.copyPurchaseInfo = async function () {
    if (!app.fullMachineId) await app.loadMachineID();
    if (!app.fullMachineId) {
      app.toast(t('license.machine_code_failed'), 'err');
      return;
    }
    const displayCode = document.getElementById('licenseMachineCode').value || '';
    const info = t('license.purchase_info', {
      version: app.licenseState.version || t('common.unknown'),
      machineId: app.fullMachineId,
      displayCode: displayCode && displayCode !== t('common.loading') ? t('license.purchase_info_display', { code: displayCode }) : ''
    });
    try {
      await navigator.clipboard.writeText(info);
      app.toast(t('license.purchase_info_copied'));
    } catch (_) {
      app.toast(t('common.copy_failed'), 'err');
    }
  };
  app.activateLicense = async function () {
    const input = document.getElementById('licenseTokenInput');
    const button = document.getElementById('licenseActivateBtn');
    const message = document.getElementById('licenseMessage');
    const token = input.value.trim();
    if (!token) {
      message.textContent = t('license.paste_token');
      message.className = 'license-message err';
      return;
    }
    button.disabled = true;
    button.textContent = t('license.verifying');
    message.textContent = '';
    try {
      const res = await app.fetch('/api/v1/license/activation', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          token
        })
      });
      const d = await res.json();
      if (!res.ok || !d.ok) {
        message.textContent = app.licenseActivationError(d.code, d.error);
        message.className = 'license-message err';
        return;
      }
      input.value = '';
      message.textContent = t('license.activated_ok');
      message.className = 'license-message ok';
      app.toast(t('license.activated_ok_toast'));
      await app.loadLicenseStatus();
    } catch (_) {
      message.textContent = t('license.activate_retry');
      message.className = 'license-message err';
    } finally {
      button.disabled = false;
      button.textContent = t('nav.activate_now');
    }
  };
  app.licenseActivationError = function licenseActivationError(code, message) {
    if (code === 'license_bad_format') return t('license.err_bad_format');
    if (code === 'license_bad_signature') return t('license.err_bad_signature');
    if (code === 'license_machine_mismatch') return t('license.err_machine_mismatch');
    if (code === 'license_unsupported') return t('license.err_unsupported');
    if (code === 'license_machine_unavailable') return t('license.err_machine_unavailable');
    return message || t('license.err_generic');
  };
}
