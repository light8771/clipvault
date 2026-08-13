import { app } from "./app-context.js";
export function registerLicense() {
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
      badge.classList.toggle('warn', !d.licensed);
      if (d.licensed) {
        badge.textContent = '已激活';
        hint.textContent = '当前设备永久有效';
      } else {
        badge.textContent = '未激活';
        hint.textContent = app.licenseStatusText(d.status);
      }
      banner.classList.toggle('hidden', d.licensed);
      activateBlock.classList.toggle('hidden', d.licensed);
      details.classList.toggle('hidden', !d.licensed);
      if (!d.licensed) {
        // 限额未知时退化为不带数字的说法，但保留横幅本身（它同时是激活入口）。
        const limit = app.licenseState.freeLimit;
        banner.querySelector('span').textContent = limit > 0 ? '免费版运行中：剪贴板内容会照常完整保存，当前可浏览和检索最近 ' + limit + ' 条。激活后解锁完整历史记录，以及导出与备份功能。' : '免费版运行中：剪贴板内容会照常完整保存，当前可浏览和检索的历史记录条数有限。激活后解锁完整历史记录，以及导出与备份功能。';
      }
      if (d.licenseId) document.getElementById('licenseIdText').textContent = d.licenseId;
      if (d.machineCode) code.value = d.machineCode;
      await app.loadMachineID();
      await app.refreshState();
    } catch (_) {
      document.getElementById('licenseStatusBadge').textContent = '读取失败';
      document.getElementById('licenseStatusHint').textContent = '无法读取本机授权状态，请查看本地日志';
    }
  };
  app.licenseStatusText = function licenseStatusText(status) {
    if (status === 'machine_mismatch') return '授权与当前设备不匹配，请申请重新签发';
    if (status === 'corrupt') return '本地授权信息损坏，请重新输入激活码';
    if (status === 'machine_error') return '无法读取设备信息，请复制诊断信息联系支持';
    if (status === 'unconfigured') return '正式授权公钥尚未配置';
    return '输入对应当前设备的激活码后即可永久使用';
  };
  app.loadMachineID = async function loadMachineID() {
    try {
      const d = await app.fetch('/api/v1/license/machine').then(r => r.json());
      app.fullMachineId = d.machineId || '';
      if (d.machineCode) document.getElementById('licenseMachineCode').value = d.machineCode;
    } catch (_) {}
  };
  app.openLicenseSettings = async function () {
    if (await app.switchView('settings')) app.snavTo('s-license', 'snav-license');
  };
  app.copyPurchaseInfo = async function () {
    if (!app.fullMachineId) await app.loadMachineID();
    if (!app.fullMachineId) {
      app.toast('机器码读取失败', 'err');
      return;
    }
    const displayCode = document.getElementById('licenseMachineCode').value || '';
    const info = '产品：ClipVault\n版本：' + (app.licenseState.version || '未知') + '\n机器码：' + app.fullMachineId + (displayCode && displayCode !== '读取中…' ? '\n识别码：' + displayCode : '');
    try {
      await navigator.clipboard.writeText(info);
      app.toast('购买信息已复制');
    } catch (_) {
      app.toast('复制失败', 'err');
    }
  };
  app.activateLicense = async function () {
    const input = document.getElementById('licenseTokenInput');
    const button = document.getElementById('licenseActivateBtn');
    const message = document.getElementById('licenseMessage');
    const token = input.value.trim();
    if (!token) {
      message.textContent = '请粘贴完整激活码';
      message.className = 'license-message err';
      return;
    }
    button.disabled = true;
    button.textContent = '正在验证…';
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
      message.textContent = '激活成功，已解锁全部历史记录与导出备份功能';
      message.className = 'license-message ok';
      app.toast('激活成功');
      await app.loadLicenseStatus();
    } catch (_) {
      message.textContent = '本地服务暂时无法完成激活，请重试';
      message.className = 'license-message err';
    } finally {
      button.disabled = false;
      button.textContent = '立即激活';
    }
  };
  app.licenseActivationError = function licenseActivationError(code, message) {
    if (code === 'license_bad_format') return '激活码不完整，请重新复制后粘贴';
    if (code === 'license_bad_signature') return '激活码验证未通过，请确认输入是否正确。';
    if (code === 'license_machine_mismatch') return '此激活码不适用于当前设备，请重新提供本机机器码。';
    if (code === 'license_unsupported') return '当前版本无法识别此激活码，请联系技术支持。';
    if (code === 'license_machine_unavailable') return '无法读取设备信息，尚未进行激活';
    return message || '激活码有效性或本地保存失败，请重试';
  };
}
