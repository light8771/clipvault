import { app } from './app-context.js';
import { createRemovableModal } from './modal-controller.js';

const t = window.clipvaultI18n.t;

export function registerSettingsLogDirectory() {
  app.openLogDirectoryDialog = function openLogDirectoryDialog() {
    if (app.logDirectoryChangeInFlight || document.getElementById('logDirectoryOverlay')) return;
    const html = `
      <div class="modal-overlay" id="logDirectoryOverlay">
        <div class="modal-box secondary-box migrate-modal" data-modal-panel>
          <div class="modal-head">
            <span class="modal-title">${t('storage.log_dialog_title')}</span>
            <button class="modal-close" type="button" data-action="close-log-directory" aria-label="${t('storage.log_dialog_close_label')}">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="modal-body u-inline-16">
            <div class="migrate-notes"><p>${t('storage.log_dialog_note')}</p></div>
            <div class="migrate-field">
              <label class="migrate-label">${t('storage.current_log_dir')}</label>
              <code class="migrate-dir" id="logDirectoryCurrent"></code>
            </div>
            <div class="migrate-field">
              <label class="migrate-label" for="logDirectoryNew">${t('storage.new_log_dir')}</label>
              <input type="text" id="logDirectoryNew" class="field-input" autocomplete="off" placeholder="${t('storage.new_log_dir_placeholder')}">
            </div>
            <p class="lock-err hidden" id="logDirectoryErr"></p>
            <div class="migrate-actions">
              <button type="button" class="btn-export" id="logDirectoryCancel">${t('common.cancel')}</button>
              <button type="button" class="lock-btn" id="logDirectoryApply">${t('storage.log_apply')}</button>
            </div>
          </div>
        </div>
      </div>`;
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('logDirectoryOverlay');
    const input = document.getElementById('logDirectoryNew');
    const error = document.getElementById('logDirectoryErr');
    const apply = document.getElementById('logDirectoryApply');
    const cancel = document.getElementById('logDirectoryCancel');
    document.getElementById('logDirectoryCurrent').textContent = document.getElementById('storageCurrentLogDir')?.textContent || '-';
    const modal = createRemovableModal(overlay, {
      initialFocus: input,
      canClose: () => !app.logDirectoryChangeInFlight
    });
    overlay.querySelector('[data-action="close-log-directory"]').addEventListener('click', modal.close);
    cancel.addEventListener('click', modal.close);
    apply.addEventListener('click', async () => {
      const target = input.value.trim();
      error.classList.add('hidden');
      if (!target) {
        error.textContent = t('storage.log_need_dir');
        error.classList.remove('hidden');
        return;
      }
      app.logDirectoryChangeInFlight = true;
      apply.disabled = true;
      cancel.disabled = true;
      input.disabled = true;
      try {
        const res = await app.protectedFetch('/api/v1/storage/log-directory', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ logDir: target })
        });
        await app.requireOK(res, t('storage.log_change_failed'));
        app.logDirectoryChangeInFlight = false;
        modal.close();
        app.toast(t('storage.log_change_scheduled'));
      } catch (e) {
        error.textContent = t('storage.log_change_failed_with', { message: e.message || e });
        error.classList.remove('hidden');
        apply.disabled = false;
        cancel.disabled = false;
        input.disabled = false;
      } finally {
        app.logDirectoryChangeInFlight = false;
        void app.loadStorageStatus();
      }
    });
  };
}
