import { app } from './app-context.js';

// 设置首页运行信息区域。
export function registerSettingsSystem() {
  const elStatus = document.getElementById('sysStatus');
  const elRecords = document.getElementById('sysRecords');
  const elDBSize = document.getElementById('sysDBSize');
  const elUptime = document.getElementById('sysUptime');
  const elVersion = document.getElementById('sysVersion');
  const elDataDir = document.getElementById('sysDataDir');
  if (!elStatus) return;

  let baseUptimeSec = 0;
  let baseFetchedAt = 0;

  function formatBytes(bytes) {
    const n = Number(bytes);
    if (!Number.isFinite(n) || n < 0) return '-';
    if (n < 1024) return n + ' B';
    const units = ['KB', 'MB', 'GB', 'TB'];
    let value = n;
    let unit = 'B';
    for (const next of units) {
      if (value < 1024) break;
      value /= 1024;
      unit = next;
    }
    return (value >= 100 ? Math.round(value) : value.toFixed(1)) + ' ' + unit;
  }

  function formatUptime(totalSec) {
    const sec = Math.max(0, Math.floor(totalSec));
    const days = Math.floor(sec / 86400);
    const hours = Math.floor((sec % 86400) / 3600);
    const minutes = Math.floor((sec % 3600) / 60);
    if (days > 0) return days + ' 天 ' + hours + ' 小时';
    if (hours > 0) return hours + ' 小时 ' + minutes + ' 分钟';
    if (minutes > 0) return minutes + ' 分钟';
    return sec + ' 秒';
  }

  function renderUptime() {
    if (!elUptime || !baseFetchedAt) return;
    const elapsed = (Date.now() - baseFetchedAt) / 1000;
    elUptime.textContent = formatUptime(baseUptimeSec + elapsed);
  }

  app.loadSystemInfo = async function loadSystemInfo() {
    try {
      const d = await app.fetch('/api/v1/system/info').then(r => r.json());
      elStatus.textContent = d.paused ? '已暂停' : '记录中';
      elStatus.classList.toggle('sysinfo-warn', !!d.paused);
      if (elRecords) elRecords.textContent = Number.isFinite(d.recordCount) ? d.recordCount.toLocaleString() + ' 条' : '-';
      if (elDBSize) elDBSize.textContent = formatBytes(d.dbBytes);
      if (elVersion) elVersion.textContent = 'v' + (d.version || '-');
      if (elDataDir) {
        elDataDir.textContent = d.dataDir || '-';
        elDataDir.title = d.dataDir || '';
      }
      baseUptimeSec = Number(d.uptimeSec) || 0;
      baseFetchedAt = Date.now();
      renderUptime();
    } catch (_) {}
  };

  setInterval(renderUptime, 30000);
  setInterval(() => {
    if (document.visibilityState === 'visible') app.loadSystemInfo();
  }, 60000);
}
