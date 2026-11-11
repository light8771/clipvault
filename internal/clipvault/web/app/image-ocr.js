import { app } from './app-context.js';
import { createRemovableModal } from './modal-controller.js';

const t = window.clipvaultI18n.t;

export async function openImageOCR(id) {
  const existing = document.querySelector(`.ocr-overlay[data-item-id="${id}"]`);
  if (existing) { existing.querySelector('.ocr-close').focus(); return; }
  const overlay = document.createElement('div');
  overlay.className = 'modal-overlay ocr-overlay';
  overlay.dataset.itemId = String(id);
  overlay.innerHTML = `<div class="modal-box ocr-dialog" data-modal-panel>
    <div class="modal-head"><h2 class="modal-title">${app.esc(t('ocr.title'))}</h2><button type="button" class="modal-close ocr-close">${app.esc(t('common.close'))}</button></div>
    <div class="modal-body ocr-body">
      <label class="ocr-image-picker" hidden>${app.esc(t('ocr.image'))}<select class="field-input ocr-select"></select></label>
      <div class="ocr-context"><strong class="ocr-connection"></strong><a href="/settings/ocr">${app.esc(t('ocr.settings'))}</a></div>
      <p class="ocr-target"></p>
      <p class="ocr-status" role="status" aria-live="polite"></p>
      <div class="ocr-columns"><div><img class="ocr-image" alt="${app.esc(t('ocr.image'))}"><p class="ocr-image-status"></p></div>
      <div class="ocr-result-panel">
        <div class="ocr-result-head"><strong>${app.esc(t('ocr.result_heading'))}</strong><button type="button" class="btn-export ocr-compare" aria-expanded="false">${app.esc(t('ocr.compare_original'))}</button></div>
        <p class="ocr-edit-hint">${app.esc(t('ocr.edit_hint'))}</p>
        <div class="ocr-result-grid"><textarea class="field-input ocr-text" aria-label="${app.esc(t('ocr.result'))}" rows="12" spellcheck="false"></textarea>
        <section class="ocr-original-panel" hidden><h3>${app.esc(t('ocr.original_readonly'))}</h3><pre class="ocr-original" tabindex="0"></pre></section></div>
      </div></div>

    </div><div class="ocr-footer"><p class="ocr-save-state" role="status" aria-live="polite"></p><div class="ocr-buttons"><button type="button" class="btn-export ocr-again">${app.esc(t('ocr.again'))}</button>
      <button type="button" class="btn-export ocr-save">${app.esc(t('ocr.save_edits'))}</button>
      <button type="button" class="btn-export ocr-primary ocr-copy">${app.esc(t('ocr.copy'))}</button></div></div>
    </div>`;
  document.body.appendChild(overlay);
  const select = overlay.querySelector('.ocr-select');
  const connection = overlay.querySelector('.ocr-connection');
  let defaultConnection = null, ready = false, failed = false;
  const editor = overlay.querySelector('.ocr-text');
  const status = overlay.querySelector('.ocr-status');
  const original = overlay.querySelector('.ocr-original');
  const compare = overlay.querySelector('.ocr-compare');
  const originalPanel = overlay.querySelector('.ocr-original-panel');
  const saveState = overlay.querySelector('.ocr-save-state');
  const image = overlay.querySelector('.ocr-image');
  const again = overlay.querySelector('.ocr-again');
  const save = overlay.querySelector('.ocr-save');
  const copy = overlay.querySelector('.ocr-copy');
  let records = [], busy = false, dirty = false, imageURL = '', generation = 0;
  const controller = new AbortController();
  const dialog = createRemovableModal(overlay, {
    canClose: () => !dirty || window.confirm(t('ocr.discard')),
    onClose: () => { controller.abort(); if (imageURL) URL.revokeObjectURL(imageURL); }
  });
  overlay.querySelector('.ocr-close').addEventListener('click', () => dialog.close());
  const endpoint = () => '/api/v1/items/ocr?id=' + id + '&image=' + encodeURIComponent(select.value);
  const request = async (url, init) => {
    const res = await app.authorizedItemFetch(id, url, { ...init, signal: controller.signal });
    await app.requireOK(res, t('common.action_failed'));
    return res;
  };
  const current = () => records.find(record => record.imageId === select.value);
  const controls = () => {
    overlay.querySelector('.ocr-columns').setAttribute('aria-busy', String(busy));
    overlay.classList.toggle('ocr-busy', busy);
    select.disabled = busy; editor.disabled = busy || !current();
    again.disabled = busy || (ready && (!select.value || !defaultConnection));
    again.textContent = busy ? t('common.loading') : failed ? t('common.retry') : t('ocr.again');
    save.disabled = busy || !current() || !dirty;
    compare.disabled = busy || !current();
    saveState.textContent = !current() ? '' : dirty ? t('ocr.unsaved_edits') : current().edited != null ? t('ocr.edits_saved') : t('ocr.auto_saved');
    saveState.classList.toggle('ocr-unsaved', dirty);
    copy.textContent = dirty ? t('ocr.save_and_copy') : t('ocr.copy');
    save.title = dirty ? t('ocr.save_edits') : t('ocr.no_unsaved_edits');
    copy.disabled = busy || !current() || !editor.value;
  };
  const display = () => {
    const record = current();
    editor.value = record ? (record.edited ?? record.original) : '';
    original.textContent = record?.original || '';
    status.textContent = record ? (record.empty ? t('ocr.empty') : t('ocr.done')) + ' · ' + record.model + ' · ' + new Date(record.recognizedAt).toLocaleString() : t('ocr.not_recognized');
    dirty = false; controls();
  };
  const preview = async () => {
    const version = ++generation;
    image.removeAttribute('src');
    if (imageURL) { URL.revokeObjectURL(imageURL); imageURL = ''; }
    const imageStatus = overlay.querySelector('.ocr-image-status');
    imageStatus.textContent = t('common.loading');
    try {
      const response = await request(endpoint());
      const blob = await response.blob();
      if (version !== generation || !overlay.isConnected) return;
      imageURL = URL.createObjectURL(blob); image.src = imageURL; imageStatus.textContent = '';
    } catch (error) { if (version === generation && overlay.isConnected) imageStatus.textContent = error.message; }
  };
  let previousSelection = '';
  select.addEventListener('change', () => {
    if (dirty && !window.confirm(t('ocr.discard'))) { select.value = previousSelection; return; }
    previousSelection = select.value; display(); void preview(); void recognize(false);
  });
  editor.addEventListener('input', () => { dirty = editor.value !== (current()?.edited ?? current()?.original ?? ''); controls(); });
  compare.addEventListener('click', () => {
    originalPanel.hidden = !originalPanel.hidden;
    compare.setAttribute('aria-expanded', String(!originalPanel.hidden));
    compare.textContent = t(originalPanel.hidden ? 'ocr.compare_original' : 'ocr.hide_original');
    overlay.querySelector('.ocr-result-grid').classList.toggle('ocr-comparing', !originalPanel.hidden);
  });
  const perform = async operation => {
    if (busy || controller.signal.aborted) return false;
    busy = true; failed = false; controls();
    try { await operation(); return true; }
    catch (error) { failed = true; if (overlay.isConnected) status.textContent = error.message || t('common.action_failed'); return false; }
    finally { busy = false; controls(); }
  };
  const saveText = async () => {
    await request(endpoint(), { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: editor.value }) });
    current().edited = editor.value; dirty = false;
  };
  const recognize = force => perform(async () => {
    if (dirty) await saveText();
    status.textContent = t('ocr.running');
    const p = defaultConnection;
    const response = await request(endpoint(), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ consent: p.location === 'cloud', force, connectionId: p.id, fingerprint: p.fingerprint }) });
    const data = await response.json();
    records = records.filter(record => record.imageId !== select.value).concat(data.result);
    display();
    if (data.cached) status.textContent += ' · ' + t('ocr.cached');
  });
  again.addEventListener('click', () => ready ? recognize(!failed) : initialize());
  save.addEventListener('click', () => perform(async () => { await saveText(); status.textContent = t('ocr.saved'); }));
  copy.addEventListener('click', () => perform(async () => {
    if (dirty) await saveText();
    await request(endpoint() + '&action=copy', { method: 'POST' });
    status.textContent = t('ocr.copied');
  }));
  const waitForTask = () => new Promise((resolve, reject) => {
    const onAbort = () => { clearTimeout(timer); reject(new DOMException('Aborted', 'AbortError')); };
    const timer = setTimeout(() => { controller.signal.removeEventListener('abort', onAbort); resolve(); }, 1000);
    controller.signal.addEventListener('abort', onAbort, { once: true });
    if (controller.signal.aborted) onAbort();
  });
  const initialize = async () => {
    let completedExistingTask = false;
    const loaded = await perform(async () => {
      ready = false; status.textContent = t('common.loading');
      const response = await request('/api/v1/items/ocr?id=' + id);
      let data = await response.json(); records = data.results || [];
      select.replaceChildren();
      for (const entry of data.images) {
        const option = document.createElement('option'); option.value = entry.id; option.textContent = entry.name; select.appendChild(option);
      }
      overlay.querySelector('.ocr-image-picker').hidden = data.images.length < 2;
      previousSelection = select.value;
      display();
      if (select.value) void preview();
      const connectionResponse = await request('/api/v1/ocr/connections');
      const connectionData = await connectionResponse.json();
      defaultConnection = connectionData.profiles.find(p => p.id === connectionData.activeId);
      connection.textContent = defaultConnection ? defaultConnection.name + ' · ' + t('ocr.default_connection') : t('ocr.no_connection');
      overlay.querySelector('.ocr-target').textContent = defaultConnection ? (defaultConnection.location === 'local' ? t('ocr.local_notice') : t('ocr.cloud_notice')) + ' · ' + defaultConnection.host : '';
      while (data.task?.running) {
        completedExistingTask = true; status.textContent = t('ocr.running');
        await waitForTask();
        const poll = await request('/api/v1/items/ocr?id=' + id);
        data = await poll.json();
      }
      records = data.results || [];
      ready = true;
      display();
      if (completedExistingTask && data.task?.error) throw new Error(data.task.error);
      if (!select.value) status.textContent = t('ocr.no_image');
      else if (!defaultConnection) status.textContent = t('ocr.no_connection');
    });
    if (loaded && !completedExistingTask && select.value && defaultConnection) await recognize(false);
  };
  controls();
  await initialize();
}
