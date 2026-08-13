import { app } from "./app-context.js";
export function registerActions() {
  app.fetch = app.apiFetch;
  app.closeSecondaryModal = undefined;
  app.showPwdDialog = undefined;
  app.closePwdDialog = undefined;
  app.showIsolatedDatabaseDialog = undefined;
  app.closeIsolatedDatabaseDialog = undefined;
  app.showRecoveryDialog = undefined;
  app.closeRecDialog = undefined;
  app.switchView = undefined;
  app.openLicenseSettings = undefined;
  app.copyPurchaseInfo = undefined;
  app.activateLicense = undefined;
  app.closeModal = undefined;
  app.modalCopy = undefined;
  app.snavTo = undefined;
  app.openMigrateDialog = undefined;
  app.closeMigrateDialog = undefined;
  app.openSourceAppsDialog = undefined;
  app.closeSourceAppsDialog = undefined;
  app.saveSourceAppsDialog = undefined;
  app.removePattern = undefined;
  app.showExportScopeDialog = undefined;
  app.closeExportScopeDialog = undefined;
  app.confirmExportScope = undefined;
  app.doExport = undefined;
  app.doExportCvexport = undefined;
  app.closeCvexportDialog = undefined;
  app.showImportDialog = undefined;
  app.closeImportDialog = undefined;
  app.doBackupDirNow = undefined;
  app.doBackupDirFullNow = undefined;
  app.doBackupEmailNow = undefined;
  app.doBackupEmailFullNow = undefined;
  app.doBackupEmailTest = undefined;
  app.showEmailBackupHistory = undefined;
  app.closeGroupEditDialog = undefined;
  app.closeConfirmDialog = undefined;
  app.actions = {
    'switch-view': el => app.switchView(el.dataset.view),
    'open-license-settings': () => app.openLicenseSettings(),
    'copy-purchase-info': () => app.copyPurchaseInfo(),
    'activate-license': () => app.activateLicense(),
    'settings-nav': el => app.snavTo(el.dataset.section, el.dataset.nav),
    'open-source-apps': () => app.openSourceAppsDialog(),
    'close-source-apps': () => app.closeSourceAppsDialog(),
    'save-source-apps': () => app.saveSourceAppsDialog(),
    'show-password': el => app.showPwdDialog(el.dataset.passwordType, el.dataset.passwordMode),
    'show-recovery': el => app.showRecoveryDialog(el.dataset.mode),
    'open-groups': el => app.openGroupModal(el.dataset.mode),
    'export': el => app.doExport(el.dataset.format),
    'export-encrypted': () => app.doExportCvexport(),
    'show-import': () => app.showImportDialog(),
    'backup-directory': () => app.doBackupDirNow(),
    'backup-directory-full': () => app.doBackupDirFullNow(),
    'backup-email': () => app.doBackupEmailNow(),
    'backup-email-full': () => app.doBackupEmailFullNow(),
    'backup-email-test': () => app.doBackupEmailTest(),
    'backup-email-history': () => app.showEmailBackupHistory(),
    'close-view-modal': () => app.closeModal(),
    'modal-copy': () => app.modalCopy(),
    'close-secondary': () => app.closeSecondaryModal(),
    'close-password': () => app.closePwdDialog(),
    'close-isolated-database': () => app.closeIsolatedDatabaseDialog(),
    'close-recovery': () => app.closeRecDialog(),
    'close-migrate': () => app.closeMigrateDialog(),
    'close-export-scope': () => app.closeExportScopeDialog(),
    'confirm-export-scope': () => app.confirmExportScope(),
    'close-encrypted-export': () => app.closeCvexportDialog(),
    'close-import': () => app.closeImportDialog(),
    'import': () => app.doImport(),
    'close-group-edit': () => app.closeGroupEditDialog(),
    'close-confirm': () => app.closeConfirmDialog(),
    'file-download': el => app.doDownload(Number(el.dataset.itemId), Number(el.dataset.fileIndex)),
    'file-open-directory': el => app.doOpenDir(Number(el.dataset.itemId), Number(el.dataset.fileIndex)),
    'file-hash': el => app.doFileHash(Number(el.dataset.itemId), Number(el.dataset.fileIndex)),
    'file-view': el => app.doFileView(Number(el.dataset.itemId), Number(el.dataset.fileIndex)),
    'remove-pattern': el => app.removePattern(Number(el.dataset.index)),
    'view-item': el => app.doView(Number(el.dataset.itemId)),
    'locate-item': el => app.doLocate(Number(el.dataset.itemId)),
    'copy-item': el => app.doCopy(Number(el.dataset.itemId), el),
    'star-item': el => app.doStar(Number(el.dataset.itemId), el),
    'assign-group': el => app.openGroupModal('assign', {
      itemId: Number(el.dataset.itemId)
    }),
    'delete-item': el => app.doDelete(Number(el.dataset.itemId), el.closest('article'))
  };
  document.addEventListener('click', event => {
    if (event.target.closest('[data-modal-panel]')) event.stopPropagation();
    const target = event.target.closest('[data-action]');
    if (!target) return;
    const action = app.actions[target.dataset.action];
    if (!action) return;
    event.preventDefault();
    action(target, event);
  });
  document.querySelectorAll('[data-credential-form]').forEach(form => {
    form.addEventListener('submit', event => event.preventDefault());
  });
  app.smtpUser = document.getElementById('cfg-emailBackupSMTPUser');
  app.smtpCredentialUser = document.getElementById('cfg-emailBackupSMTPCredentialUser');
  if (app.smtpUser && app.smtpCredentialUser) {
    app.smtpUser.addEventListener('input', () => {
      app.smtpCredentialUser.value = app.smtpUser.value;
    });
  }
  app.Q = document.getElementById('q');
  app.PAGESIZE = document.getElementById('pagesize');
  app.LIST = document.getElementById('list');
  app.DOT = document.getElementById('dot');
  app.ST = document.getElementById('st');
  app.BADGE = document.getElementById('badge');
  app.TOG = document.getElementById('tog');
  app.TOGTX = document.getElementById('togTx');
  app.RC = document.getElementById('rc');
  app.AR = document.getElementById('ar');
  app.TC = document.getElementById('tc');
  app.debounce = 0;
  app.arTimer = 0;
  app.currentKind = '';
  app.currentPage = 0;
  app.totalCount = 0;
  app.fullTotalCount = 0;
  app.pendingLocateId = 0;
  app.locateActive = false;
  app.filterDateFrom = '';
  app.filterDateTo = '';
  app.currentGroupID = 0;
  app.sourceAliases = {};
  app.sourceWhitelist = null;
  app.sourceAppsDraft = null;
  app.allSourceExes = [];
  app.currentSourceExes = [];
  app.licenseState = {
    licensed: true,
    enforced: true,
    status: 'loading',
    machineCode: '',
    version: '',
    freeLimit: 0
  };
  app.fullMachineId = '';
  app.items = new Map();
  app.filePaths = new Map();
  app.NON_TEXT_KINDS = ['image', 'video', 'archive', 'office', 'files'];
  app.IMAGE_EXTS = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'svg', 'ico', 'tiff', 'tif', 'heic', 'heif', 'avif'];
  app.allGroups = [];
  app.GROUP_EXPAND = new Set();
  app.groupModalMode = null;
  app.groupModalOpts = {};
  app.authState = {
    hasLockPassword: false,
    hasSecondaryPassword: false,
    authenticated: false,
    secondaryAuthed: false
  };
  app.secondaryResolve = null;
  app.secondaryAuthPromise = null;
  app.LOCK_OVERLAY = document.getElementById('lockOverlay');
  app.LOCK_FORM = document.getElementById('lockForm');
  app.LOCK_INPUT = document.getElementById('lockPassword');
  app.LOCK_ERR = document.getElementById('lockErr');
  app.SECONDARY_MODAL = document.getElementById('secondaryModal');
  app.SECONDARY_FORM = document.getElementById('secondaryForm');
  app.SECONDARY_INPUT = document.getElementById('secondaryPassword');
  app.SECONDARY_ERR = document.getElementById('secondaryErr');
}
