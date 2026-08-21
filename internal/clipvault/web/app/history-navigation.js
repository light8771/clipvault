import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

export function registerHistoryNavigation() {
  app.GROUP_CHIP = document.getElementById('groupChip');
  document.getElementById('kindFilters').addEventListener('click', async event => {
    const chip = event.target.closest('.filter-chip');
    if (!chip) return;
    const kind = chip.dataset.kind;
    if (kind === '__group__') {
      if (app.allGroups.length === 0) await app.loadGroups();
      app.openGroupModal('filter');
      return;
    }
    if (kind === '__sensitive__') {
      const authorized = await app.requestSecondaryAuth();
      if (!authorized) {
        app.toast(t('history.sensitive_auth_required'), 'err');
        return;
      }
    }
    document.querySelectorAll('.filter-chip').forEach(element => element.classList.remove('active'));
    chip.classList.add('active');
    app.currentKind = kind;
    app.currentGroupID = 0;
    app.resetSearch();
  });
}
