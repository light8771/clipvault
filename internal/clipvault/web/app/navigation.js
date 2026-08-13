import { app } from "./app-context.js";
export function registerNavigation() {
  app.viewSwitchSequence = 0;
  app.switchView = async function (view) {
    const request = ++app.viewSwitchSequence;
    const isHistory = view === 'history';
    if (!isHistory) {
      app.setupAutoSave();
      const ok = await app.requestSecondaryAuth();
      if (request !== app.viewSwitchSequence) return false;
      if (!ok) {
        document.getElementById('viewHistory').classList.remove('hidden');
        document.getElementById('viewSettings').classList.add('hidden');
        document.getElementById('tabHistory').classList.add('active');
        document.getElementById('tabSettings').classList.remove('active');
        app.toast('已取消进入设置，需要二次密码验证', 'err');
        return false;
      }
      await app.loadSettings();
      if (request !== app.viewSwitchSequence) return false;
    }
    document.getElementById('viewHistory').classList.toggle('hidden', !isHistory);
    document.getElementById('viewSettings').classList.toggle('hidden', isHistory);
    document.getElementById('tabHistory').classList.toggle('active', isHistory);
    document.getElementById('tabSettings').classList.toggle('active', !isHistory);
    return true;
  };

  // ── Filter chips ──────────────────────────────────────────
  app.GROUP_CHIP = document.getElementById('groupChip');
  document.getElementById('kindFilters').addEventListener('click', async e => {
    const chip = e.target.closest('.filter-chip');
    if (!chip) return;
    const kind = chip.dataset.kind;
    // 分组 chip：打开分组选择弹窗
    if (kind === '__group__') {
      if (app.allGroups.length === 0) {
        await app.loadGroups();
      }
      app.openGroupModal('filter');
      return;
    }
    // 敏感数据分组需要二次验证
    if (kind === '__sensitive__') {
      const ok = await app.requestSecondaryAuth();
      if (!ok) {
        app.toast('需要二次验证才能查看敏感数据', 'err');
        return;
      }
    }
    document.querySelectorAll('.filter-chip').forEach(c => c.classList.remove('active'));
    chip.classList.add('active');
    app.currentKind = kind;
    app.currentGroupID = 0; // reset group filter when switching to other kinds
    app.resetSearch();
  });

  // ── Toast ─────────────────────────────────────────────────
  app.toast = function toast(msg, type = 'ok') {
    const el = document.createElement('div');
    el.className = 'tk ' + type;
    el.textContent = msg;
    el.setAttribute('role', type === 'err' ? 'alert' : 'status');
    el.setAttribute('aria-live', type === 'err' ? 'assertive' : 'polite');
    app.TC.appendChild(el);
    requestAnimationFrame(() => el.classList.add('show'));
    const duration = type === 'err' ? 7000 : 3500;
    setTimeout(() => {
      el.classList.remove('show');
      setTimeout(() => el.remove(), 260);
    }, duration);
  };
}
