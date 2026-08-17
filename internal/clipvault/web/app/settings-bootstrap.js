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
import { registerCleanupModal } from './cleanup-modal.js';
import { registerSettingsLifecycle } from './settings-lifecycle.js';

registerActions({
  'copy-purchase-info': () => app.copyPurchaseInfo(),
  'activate-license': () => app.activateLicense(),
  'settings-nav': element => app.navigateToSettingsSection(element.dataset.section, true),
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
  'backup-email': () => app.doBackupEmailNow(),
  'backup-email-full': () => app.doBackupEmailFullNow(),
  'backup-email-test': () => app.doBackupEmailTest(),
  'backup-email-history': () => app.showEmailBackupHistory(),
  'close-password': () => app.closePwdDialog(),
  'close-isolated-database': () => app.closeIsolatedDatabaseDialog(),
  'close-recovery': () => app.closeRecDialog(),
  'close-migrate': () => app.closeMigrateDialog(),
  'close-export-scope': () => app.closeExportScopeDialog(),
  'confirm-export-scope': () => app.confirmExportScope(),
  'close-encrypted-export': () => app.closeCvexportDialog(),
  'close-import': () => app.closeImportDialog(),
  'remove-pattern': element => app.removePattern(Number(element.dataset.index))
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
registerCleanupModal();
registerSettingsLifecycle();
