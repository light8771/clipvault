import { registerHistoryFiles } from './history-files.js';
import { app } from "./app-context.js";
import { showCompositePreview } from './composite-content.js';
import { trapModalFocus } from './modal-controller.js';

const t = window.clipvaultI18n.t;

export function registerHistoryActions() {
  app.items = new Map();
  app.filePaths = new Map();
  app.NON_TEXT_KINDS = ['image', 'video', 'archive', 'office', 'files'];
  app.IMAGE_EXTS = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'svg', 'ico', 'tiff', 'tif', 'heic', 'heif', 'avif'];
  app.relTime = function relTime(s) {
    const d = Math.floor((Date.now() - new Date(s).getTime()) / 1000);
    if (d < 5) return t('history.rel_now');
    if (d < 60) return t('history.rel_seconds', { n: d });
    if (d < 3600) return t('history.rel_minutes', { n: Math.floor(d / 60) });
    if (d < 86400) return t('history.rel_hours', { n: Math.floor(d / 3600) });
    if (d < 604800) return t('history.rel_days', { n: Math.floor(d / 86400) });
    return new Date(s).toLocaleString(window.clipvaultI18n.locale());
  };
  app.cpHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg><span class="ab-label">' + app.esc(t('common.copy')) + '</span>';
  app.fetchItemCopyText = async function fetchItemCopyText(id) {
    const res = await app.authorizedItemFetch(id, '/api/v1/items/copy-text?id=' + id);
    await app.requireOK(res, t('common.copy_failed'));
    const d = await res.json();
    if (typeof d.text !== 'string') throw new app.APIError('invalid_response', t('common.copy_failed'), 500);
    return d.text;
  };
  app.copyItemToClipboard = async function copyItemToClipboard(id) {
    const item = app.items.get(id);
    if (!item) return;
    if (item.kind === 'composite') {
      try {
        const res = await app.authorizedItemFetch(id, '/api/v1/items/restore?id=' + id, { method: 'POST' });
        await app.requireOK(res, t('common.copy_failed'));
        app.toast(t('history.copied_to_clipboard'));
      } catch (_) { app.toast(t('common.copy_failed'), 'err'); }
      return;
    }
    let textToCopy;
    try {
      textToCopy = await app.fetchItemCopyText(id);
    } catch (error) {
      const authBlocked = item.sensitive && ['secondary_cancelled', 'secondary_required', 'fresh_secondary_required'].includes(error?.code);
      app.toast(authBlocked ? t('history.sensitive_copy_blocked') : t('common.copy_failed'), 'err');
      return;
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      app.toast(t('history.copied_to_clipboard'));
    } catch (_) {
      app.toast(t('common.copy_failed'), 'err');
    }
  };
  app.doCopy = app.copyItemToClipboard;
  app.doDelete = async function doDelete(id) {
    const item = app.items.get(id);
    if (!item) return;
    try {
      const res = await app.authorizedItemFetch(id, '/api/v1/items?id=' + id, {
        method: 'DELETE'
      });
      if (!res.ok) throw 0;
      // Grouped events can keep, remove, or even split their projected row
      // after one raw occurrence is deleted. Rebuild from the authoritative
      // query instead of guessing the resulting total in the browser.
      app.toast(t('history.deleted'));
      await app.search();
    } catch (_) {
      app.toast(t('common.delete_failed'), 'err');
    }
  };
  app.starOnHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="1.5"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>';
  app.starOffHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>';
  app.doStar = async function doStar(id, btn) {
    const item = app.items.get(id);
    if (!item) return;
    const newStarred = !item.starred;
    try {
      const res = await app.authorizedItemFetch(id, '/api/v1/items?id=' + id + '&starred=' + (newStarred ? '1' : '0'), {
        method: 'PATCH'
      });
      if (!res.ok) throw 0;
      item.starred = newStarred;
      const art = btn.closest('article');
      btn.innerHTML = newStarred ? app.starOnHTML : app.starOffHTML;
      btn.classList.toggle('on', newStarred);
      btn.title = newStarred ? t('history.unstar') : t('history.kind_starred');
      art.dataset.starred = newStarred ? '1' : '0';
      app.toast(newStarred ? t('history.starred') : t('history.unstarred'));
      if (app.currentKind === '__starred__') await app.search();
    } catch (_) {
      app.toast(t('common.action_failed'), 'err');
    }
  };
  let previewGeneration = 0;
  function beginPreview(id) {
    app.viewModalTrigger = document.activeElement;
    app.currentModalId = id;
    app.VIEW_MODAL.classList.add('hidden');
    document.body.style.overflow = '';
    app.MODAL_PRE.textContent = '';
    const preview = document.getElementById('modalFilePreview');
    if (preview) preview.innerHTML = '';
    return ++previewGeneration;
  }
  app.currentModalId = 0;
  app.VIEW_MODAL = document.getElementById('viewModal');
  app.MODAL_PRE = document.getElementById('modalPre');
  app.MODAL_TITLE = document.getElementById('modalTitle');
  app.doView = async function doView(id) {
    const item = app.items.get(id);
    if (!item) return;
    const request = beginPreview(id);
    if (item.kind === 'composite') {
      await showCompositePreview(id, () => request === previewGeneration);
      return;
    }
    let resolvedItem = item;
    let displayText = item.text;
    if (item.sensitive) {
      try {
        const url = '/api/v1/items/sensitive/item?id=' + id;
        const res = await app.authorizedItemFetch(id, url);
        if (!res.ok) throw new Error('HTTP ' + res.status);
        const d = await res.json();
        if (d.item) {
          resolvedItem = d.item;
          if (typeof d.item.text === 'string') displayText = d.item.text;
        }
      } catch (_) {
        if (request === previewGeneration) app.toast(t('history.sensitive_load_failed'), 'err');
        return;
      }
    }
    if (request !== previewGeneration) return;
    // The ordinary list response intentionally redacts a locked sensitive
    // item's format. Decide whether it is a managed image only after the
    // authorized item response restores the canonical format.
    if (app.isManagedImage(resolvedItem)) {
      await app.doViewManagedImage(id);
      return;
    }
    const kindKey = resolvedItem.kind || 'text';
    if (app.isNonTextKind(kindKey)) {
      const paths = displayText.split('\n').filter(Boolean);
      app.MODAL_TITLE.textContent = t('history.file_list_title', { id: id });
      app.MODAL_PRE.style.display = 'none';
      let preview = document.getElementById('modalFilePreview');
      if (!preview) {
        preview = document.createElement('div');
        preview.id = 'modalFilePreview';
        preview.className = 'modal-file-preview';
        app.VIEW_MODAL.querySelector('.modal-body').appendChild(preview);
      }
      preview.style.display = 'flex';
      preview.innerHTML = paths.map((p, idx) => {
        const name = app.getBaseName(p);
        const ext = app.getExt(name);
        const isImg = app.IMAGE_EXTS.includes(ext);
        return '<div class="modal-file-item">' + (isImg && ext !== 'svg' ? '<img data-file-index="' + idx + '" loading="lazy" alt="' + app.esc(name) + '">' : '') + '<div class="modal-file-name">' + app.esc(name) + '</div>' + '<div class="modal-file-path">' + app.esc(p) + '</div>' + '</div>';
      }).join('');
      preview.querySelectorAll('img[data-file-index]').forEach(img => {
        app.loadFileImage(img, item.id, Number(img.dataset.fileIndex));
      });
    } else {
      app.MODAL_TITLE.textContent = t('history.preview_title') + ' · #' + id + (item.sensitive ? ' · ' + t('history.sensitive_tag') : '');
      app.MODAL_PRE.style.display = 'block';
      app.MODAL_PRE.textContent = displayText;
      const preview = document.getElementById('modalFilePreview');
      if (preview) preview.style.display = 'none';
    }
    app.VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
    requestAnimationFrame(() => { if (request === previewGeneration) document.getElementById('modalCopyBtn')?.focus(); });
  };
  app.closeModal = function () {
    previewGeneration++;
    app.currentModalId = 0;
    app.MODAL_PRE.textContent = '';
    app.VIEW_MODAL.classList.add('hidden');
    document.body.style.overflow = '';
    app.MODAL_PRE.style.display = 'block';
    const preview = document.getElementById('modalFilePreview');
    if (preview) {
      preview.innerHTML = '';
      preview.style.display = 'none';
    }
    if (app.viewModalTrigger?.isConnected && typeof app.viewModalTrigger.focus === 'function') app.viewModalTrigger.focus();
    app.viewModalTrigger = null;
  };
  app.VIEW_MODAL.addEventListener('keydown', event => {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      app.closeModal();
      return;
    }
    trapModalFocus(app.VIEW_MODAL, event);
  });
  app.modalCopy = async function () {
    if (app.currentModalId) await app.copyItemToClipboard(app.currentModalId);
  };

  // ── Kind label map ────────────────────────────────
  // 值为 i18n key，渲染处统一 t() 解析（切换语言即时生效）。
  app.KIND_LABEL = {
    text: 'history.kind_text',
    image: 'history.kind_image',
    video: 'history.kind_video',
    archive: 'history.kind_archive',
    office: 'history.kind_office',
    files: 'history.kind_files',
    event: 'history.kind_events'
  };
  app.sensBtnHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>';
  app.isManagedImage = function isManagedImage(it) {
    return !!it && it.recordType === 'content' && it.format === 'CLIPVAULT_IMAGE_FILE';
  };
  app.loadManagedImage = async function loadManagedImage(img, itemId, interactive = true) {
    if (!img) return;
    try {
      const url = '/api/v1/items/image?id=' + itemId;
      const res = await (interactive ? app.authorizedItemFetch(itemId, url) : app.fetch(url));
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const blob = await res.blob();
      if (!img.isConnected) return;
      const objectURL = URL.createObjectURL(blob);
      app.fileObjectURLs.add(objectURL);
      img.src = objectURL;
    } catch (_) {
      img.closest('.managed-image')?.classList.add('managed-image-missing');
      img.style.display = 'none';
    }
  };
  app.doViewManagedImage = async function doViewManagedImage(id) {
    const item = app.items.get(id);
    if (!item) return;
    const request = beginPreview(id);
    app.MODAL_TITLE.textContent = t('history.image_title', { id: id }) + (item.sensitive ? ' · ' + t('history.sensitive_tag') : '');
    app.MODAL_PRE.style.display = 'none';
    let preview = document.getElementById('modalFilePreview');
    if (!preview) {
      preview = document.createElement('div');
      preview.id = 'modalFilePreview';
      preview.className = 'modal-file-preview';
      app.VIEW_MODAL.querySelector('.modal-body').appendChild(preview);
    }
    preview.style.display = 'flex';
    preview.innerHTML = '<img alt="' + app.esc(t('history.image_title', { id: id })) + '">';
    await app.loadManagedImage(preview.querySelector('img'), id);
    if (request !== previewGeneration) return;
    app.VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  };
  // doToggleSensitive 手动标记/取消标记敏感。取消标记需要二次认证
  // （内容将重新明文可见），标记只需登录。
  app.doToggleSensitive = async function doToggleSensitive(id) {
    const item = app.items.get(id);
    if (!item) return;
    const next = !item.sensitive;
    const url = '/api/v1/items/sensitive?id=' + id + '&sensitive=' + (next ? '1' : '0');
    try {
      const res = next ? await app.fetch(url, { method: 'PATCH' }) : await app.protectedFetch(url, { method: 'PATCH' });
      if (!res.ok) throw new Error('HTTP ' + res.status);
      item.sensitive = next;
      app.toast(next ? t('history.marked_sensitive') : t('history.unmarked_sensitive'));
      await app.search();
    } catch (e) {
      if (e && (e.code === 'secondary_required' || e.code === 'secondary_cancelled')) {
        app.toast(t('history.unmark_needs_verify'), 'err');
      } else {
        app.toast(t('common.action_failed'), 'err');
      }
    }
  };
  app.isNonTextKind = function isNonTextKind(k) {
    return app.NON_TEXT_KINDS.includes(k);
  };
  app.authorizedItemFetch = async function authorizedItemFetch(itemId, url, init) {
    const sensitive = !!app.items.get(itemId)?.sensitive;
    if (!sensitive) return app.fetch(url, init);
    const run = async () => {
      return app.protectedFetch(url, init);
    };
    const queued = app.protectedItemRequestQueue.then(run, run);
    app.protectedItemRequestQueue = queued.then(() => undefined, () => undefined);
    return queued;
  };
  registerHistoryFiles();
  app.doFileView = async function doFileView(itemId, idx) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const p = paths[idx];
    const name = app.getBaseName(p);
    const request = beginPreview(itemId);
    app.MODAL_TITLE.textContent = name;
    app.MODAL_PRE.style.display = 'none';
    let preview = document.getElementById('modalFilePreview');
    if (!preview) {
      preview = document.createElement('div');
      preview.id = 'modalFilePreview';
      preview.className = 'modal-file-preview';
      app.VIEW_MODAL.querySelector('.modal-body').appendChild(preview);
    }
    preview.style.display = 'flex';
    preview.innerHTML = app.getExt(name) === 'svg' ? '<div class="fmiss">' + app.esc(t('history.svg_no_preview')) + '</div>' : '<img alt="' + app.esc(name) + '">';
    if (app.getExt(name) !== 'svg') await app.loadFileImage(preview.querySelector('img'), itemId, idx);
    if (request !== previewGeneration) return;
    app.VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  };
}
