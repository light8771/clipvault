import { registerActions } from './actions.js';
import { app } from './app-context.js';
import { registerNotifications } from './notifications.js';
import { registerRuntimeState } from './runtime-state.js';
import { registerSourceData } from './source-data.js';
import { registerAuth } from './auth.js';
import { registerHistoryNavigation } from './history-navigation.js';
import { registerLicense } from './license.js';
import { registerTextUtils } from './text-utils.js';
import { registerHistoryActions } from './history-actions.js';
import { registerHistoryList } from './history-list.js';
import { registerAppearanceTheme } from './appearance-theme.js';
import { registerAppearanceBackground } from './appearance-background.js';
import { registerAppearanceLoader } from './appearance-loader.js';
import { registerHistoryLayout } from './history-layout.js';
import { registerGroups } from './groups.js';
import { registerHistoryLifecycle } from './history-lifecycle.js';

registerActions({
  'close-view-modal': () => app.closeModal(),
  'modal-copy': () => app.modalCopy(),
  'file-download': element => app.doDownload(Number(element.dataset.itemId), Number(element.dataset.fileIndex)),
  'file-open-directory': element => app.doOpenDir(Number(element.dataset.itemId), Number(element.dataset.fileIndex), element),
  'file-hash': element => app.doFileHash(Number(element.dataset.itemId), Number(element.dataset.fileIndex), element),
  'file-view': element => app.doFileView(Number(element.dataset.itemId), Number(element.dataset.fileIndex)),
  'view-item': element => app.doView(Number(element.dataset.itemId)),
  'locate-item': element => app.doLocate(Number(element.dataset.itemId)),
  'copy-item': element => app.doCopy(Number(element.dataset.itemId)),
  'star-item': element => app.doStar(Number(element.dataset.itemId), element),
  'toggle-sensitive': element => app.doToggleSensitive(Number(element.dataset.itemId)),
  'view-managed-image': element => app.doViewManagedImage(Number(element.dataset.itemId)),
  'assign-group': element => app.openGroupModal('assign', { itemId: Number(element.dataset.itemId) }),
  'delete-item': element => app.doDelete(Number(element.dataset.itemId), element.closest('article'))
});
registerNotifications();
registerRuntimeState();
registerSourceData();
registerAuth();
registerHistoryNavigation();
registerLicense();
registerTextUtils();
registerHistoryActions();
registerHistoryList();
registerAppearanceTheme();
registerAppearanceBackground();
registerAppearanceLoader();
registerHistoryLayout();
registerGroups();
registerHistoryLifecycle();
