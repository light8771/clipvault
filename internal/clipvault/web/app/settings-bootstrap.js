import { registerActions } from './actions.js';
import { app } from './app-context.js';
import { registerNotifications } from './notifications.js';
import { registerRuntimeState } from './runtime-state.js';
import { registerSourceData } from './source-data.js';
import { registerAuth } from './auth.js';
import { registerRecovery } from './recovery.js';
import { registerLicense } from './license.js';
import { registerTextUtils } from './text-utils.js';
import { registerSettingsNavigation } from './settings-navigation.js';
import { registerAppearanceTheme } from './appearance-theme.js';
import { registerAppearanceBackground } from './appearance-background.js';
import { registerAppearanceLoader } from './appearance-loader.js';
import { registerAppearanceMedia } from './appearance-media.js';
import { registerAppearanceForm } from './appearance-form.js';
import { registerSettingsStorage } from './settings-storage.js';
import { registerSettingsSources } from './settings-sources.js';
import { registerConfigSave } from './config-save.js';
import { registerConfigEvents } from './config-events.js';
import { registerTransfer } from './transfer.js';
import { registerGroups } from './groups.js';
import { registerGroupsPage } from './groups-page.js';
import { registerCleanupModal } from './cleanup-modal.js';
import { registerSettingsLifecycle } from './settings-lifecycle.js';
import { registerSettingsSystem } from './settings-system.js';
import { registerLogs } from './logs.js';
import { registerDataAPI } from './data-api.js';
import { registerDataAPIAudit } from './data-api-audit.js';

registerActions({
  'copy-purchase-info': () => app.copyPurchaseInfo(),
  'activate-license': () => app.activateLicense(),
  'open-cleanup': () => app.openCleanupModal(),
  'close-cleanup-modal': () => app.closeCleanupModal(),
  'confirm-cleanup': () => app.doCleanup(),
  'open-source-apps': () => app.openSourceAppsDialog(),
  'close-source-apps': () => app.closeSourceAppsDialog(),
  'save-source-apps': () => app.saveSourceAppsDialog(),
  'show-password': element => app.showPwdDialog(element.dataset.passwordType, element.dataset.passwordMode),
  'show-recovery': element => app.showRecoveryDialog(element.dataset.mode),
  'open-groups': element => app.openGroupModal(element.dataset.mode),
  'export': element => app.doExport(element.dataset.format),
  'export-encrypted': () => app.doExportCvexport(),
  'show-import': () => app.showImportDialog(),
  'backup-directory': () => app.doBackupDirNow(),
  'backup-directory-full': () => app.doBackupDirFullNow(),
  'backup-dir-history': () => app.showBackupHistory('dir'),
  'backup-email': () => app.doBackupEmailNow(),
  'backup-email-full': () => app.doBackupEmailFullNow(),
  'backup-email-test': () => app.doBackupEmailTest(),
  'backup-email-history': () => app.showBackupHistory('email'),
  'close-password': () => app.closePwdDialog(),
  'close-isolated-database': () => app.closeIsolatedDatabaseDialog(),
  'close-recovery': () => app.closeRecDialog(),
  'close-migrate': () => app.closeMigrateDialog(),
  'close-export-scope': () => app.closeExportScopeDialog(),
  'confirm-export-scope': () => app.confirmExportScope(),
  'close-encrypted-export': () => app.closeCvexportDialog(),
  'close-import': () => app.closeImportDialog(),
  'remove-pattern': element => app.removePattern(Number(element.dataset.index)),
  'clear-config-changes': element => app.clearConfigChangeLog(element),
  'clear-search-history': element => app.clearSearchHistory(element),
  'oplog-prev': () => app.turnConfigChangeLogPage(-1),
  'oplog-next': () => app.turnConfigChangeLogPage(1),
  'shlog-prev': () => app.turnSearchHistoryPage(-1),
  'shlog-next': () => app.turnSearchHistoryPage(1),
  'data-api-save': () => app.dataApiSave(),
  'data-api-copy-url': () => app.dataApiCopyURL(),
  'data-api-copy-example': () => app.dataApiCopyExample(),
  'data-api-open-doc': () => app.openDataAPIDoc(),
  'data-api-close-doc': () => app.closeDataAPIDoc(),
  'data-api-copy-doc': element => app.dataApiCopyDoc(element),
  'data-api-doc-nav': element => app.dataApiDocNav(element),
  'data-api-token-create': () => app.dataApiTokenCreate(),
  'data-api-token-copy': () => app.dataApiTokenCopy(),
  'data-api-token-dismiss': () => app.dataApiTokenDismiss(),
  'data-api-token-revoke': element => app.dataApiTokenRevoke(element),
  'data-api-token-regenerate': element => app.dataApiTokenRegenerate(element),
  'data-api-audit-prev': () => app.turnDataAPIAuditPage(-1),
  'data-api-audit-next': () => app.turnDataAPIAuditPage(1),
  'data-api-audit-first': () => app.gotoDataAPIAuditPage(0),
  'data-api-audit-last': () => app.gotoDataAPIAuditPage(Number.MAX_SAFE_INTEGER),
  'data-api-audit-page': element => app.gotoDataAPIAuditPage(parseInt(element.dataset.page, 10) || 0),
  'data-api-audit-toggle': element => app.toggleDataAPIAuditRow(element),
  'data-api-audit-apply': () => app.applyDataAPIAuditFilters(),
  'data-api-audit-reset': () => app.resetDataAPIAuditFilters(),
  'data-api-audit-export': element => app.exportDataAPIAudit(element),
  'data-api-audit-refresh': () => app.refreshDataAPIAudit(),
  'data-api-audit-clear': element => app.clearDataAPIAudit(element)
});
registerNotifications();
registerRuntimeState();
registerSourceData();
registerAuth();
registerRecovery();
registerLicense();
registerTextUtils();
registerSettingsNavigation();
registerAppearanceTheme();
registerAppearanceBackground();
registerAppearanceLoader();
registerAppearanceMedia();
registerAppearanceForm();
registerSettingsStorage();
registerSettingsSources();
registerConfigSave();
registerConfigEvents();
registerTransfer();
registerGroups();
registerGroupsPage();
registerCleanupModal();
registerSettingsSystem();
registerSettingsLifecycle();
registerLogs();
registerDataAPIAudit(); // 先注册审计（提供令牌筛选渲染器），再注册数据接口主体
registerDataAPI();
