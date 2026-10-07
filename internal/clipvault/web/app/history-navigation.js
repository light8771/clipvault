import { app } from './app-context.js';
import { setupHistorySubtypes, subtypeLabel } from './history-subtypes.js';

const t = window.clipvaultI18n.t;

export function registerHistoryNavigation() {
  const filters = document.getElementById('kindFilters');
  let selectionRevision = 0;
  const menu = setupHistorySubtypes(filters, select);
  app.resetSubtypeFilter = (keepLocate = false) => {
    if (!keepLocate) app.cancelLocate?.();
    selectionRevision++;
    app.currentSubtype = '';
    filters.querySelectorAll('.kind-subtype-label').forEach(label => label.remove());
    menu.close();
  };
  async function select(kind, subtype = '') {
    const pendingRevision = ++selectionRevision;
    menu.close();
    if (kind === '__sensitive__') {
      const authorized = await app.requestSecondaryAuth();
      if (pendingRevision !== selectionRevision) return;
      if (!authorized) {
        app.toast(t('history.sensitive_auth_required'), 'err');
        return;
      }
    }
    app.resetSubtypeFilter();
    const revision = selectionRevision;
    filters.querySelectorAll('.filter-chip').forEach(chip => chip.classList.remove('active'));
    const chip = filters.querySelector('[data-kind="' + kind + '"]');
    chip.classList.add('active');
    if (subtype) {
      const label = document.createElement('span');
      label.className = 'kind-subtype-label';
      label.textContent = subtypeLabel(subtype);
      chip.appendChild(label);
    }
    chip.focus();
    app.currentKind = kind;
    app.currentSubtype = subtype;
    app.currentGroupID = 0;
    const recordType = kind === '__events__' ? 'event' : 'content';
    const loaded = await app.loadSourceExesForHistory(recordType);
    if (loaded && revision === selectionRevision) app.resetSearch();
  }
  app.GROUP_CHIP = document.getElementById('groupChip');
  filters.addEventListener('click', async event => {
    const chip = event.target.closest('.filter-chip');
    if (!chip) return;
    const kind = chip.dataset.kind;
    if (kind === '__group__') {
      menu.close();
      const revision = ++selectionRevision;
      if (app.allGroups.length === 0) await app.loadGroups();
      if (revision === selectionRevision) app.openGroupModal('filter');
      return;
    }
    await select(kind);
  });
}
