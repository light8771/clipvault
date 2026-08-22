import { app } from "./app-context.js";
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
  app.cpHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg> ' + t('common.copy');
  app.copiedHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg> ' + t('common.copied');
  app.fetchSensitiveText = async function fetchSensitiveText(id) {
    const url = '/api/v1/items/sensitive/item?id=' + id;
    const res = await app.authorizedItemFetch(id, url);
    if (!res.ok) return null;
    const d = await res.json();
    return d.item ? d.item.text : null;
  };
  app.doCopy = async function doCopy(id, btn) {
    const item = app.items.get(id);
    if (!item) return;
    let textToCopy = item.text;
    if (item.sensitive) {
      try {
        textToCopy = await app.fetchSensitiveText(id);
      } catch (_) {
        textToCopy = null;
      }
      if (textToCopy === null) {
        app.toast(t('history.sensitive_copy_blocked'), 'err');
        return;
      }
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      btn.innerHTML = app.copiedHTML;
      btn.classList.add('ok');
      app.toast(t('history.copied_to_clipboard'));
      setTimeout(() => {
        btn.innerHTML = app.cpHTML;
        btn.classList.remove('ok');
      }, 1600);
    } catch (_) {
      app.toast(t('common.copy_failed'), 'err');
    }
  };
  app.doDelete = async function doDelete(id, art) {
    try {
      const res = await app.authorizedItemFetch(id, '/api/v1/items?id=' + id, {
        method: 'DELETE'
      });
      if (!res.ok) throw 0;
      art.style.transition = 'opacity .18s, transform .18s';
      art.style.opacity = '0';
      art.style.transform = 'translateX(12px)';
      setTimeout(() => art.remove(), 200);
      app.items.delete(id);
      // 同步递减计数并复用 updatePagination 统一刷新「N 条结果」、badge
      // （badge 有 license 全量转化逻辑，不能手工赋值为当前页 items.size）
      // 与分页按钮状态，避免删除后刷新前计数滞后。
      app.totalCount = Math.max(0, app.totalCount - 1);
      app.fullTotalCount = Math.max(0, app.fullTotalCount - 1);
      app.updatePagination();
      app.toast(t('history.deleted'));
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
    } catch (_) {
      app.toast(t('common.action_failed'), 'err');
    }
  };
  app.currentModalId = 0;
  app.VIEW_MODAL = document.getElementById('viewModal');
  app.MODAL_PRE = document.getElementById('modalPre');
  app.MODAL_TITLE = document.getElementById('modalTitle');
  app.doView = async function doView(id) {
    app.viewModalTrigger = document.activeElement;
    const item = app.items.get(id);
    if (!item) return;
    app.currentModalId = id;
    const kindKey = item.kind || 'text';
    let displayText = item.text;
    if (item.sensitive) {
      try {
        const url = '/api/v1/items/sensitive/item?id=' + id;
        const res = await app.authorizedItemFetch(id, url);
        if (!res.ok) throw new Error('HTTP ' + res.status);
        const d = await res.json();
        if (d.item && d.item.text) {
          displayText = d.item.text;
        }
      } catch (_) {
        app.toast(t('history.sensitive_load_failed'), 'err');
        return;
      }
    }
    if (app.isNonTextKind(kindKey)) {
      const paths = displayText.split('\n').filter(Boolean);
      app.MODAL_TITLE.textContent = t('history.file_list_title', { id: id });
      app.MODAL_PRE.style.display = 'none';
      let preview = document.getElementById('modalFilePreview');
      if (!preview) {
        preview = document.createElement('div');
        preview.id = 'modalFilePreview';
        preview.className = 'modal-file-preview';
        document.querySelector('.modal-body').appendChild(preview);
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
    requestAnimationFrame(() => document.getElementById('modalCopyBtn')?.focus());
  };
  app.closeModal = function () {
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
    const item = app.items.get(app.currentModalId);
    if (!item) return;
    const btn = document.getElementById('modalCopyBtn');
    let textToCopy = item.text;
    if (item.sensitive) {
      try {
        textToCopy = await app.fetchSensitiveText(app.currentModalId);
      } catch (_) {
        textToCopy = null;
      }
      if (textToCopy === null) {
        app.toast(t('history.sensitive_copy_blocked'), 'err');
        return;
      }
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      btn.innerHTML = app.copiedHTML;
      btn.classList.add('ok');
      app.toast(t('history.copied_to_clipboard'));
      setTimeout(() => {
        btn.innerHTML = app.cpHTML;
        btn.classList.remove('ok');
      }, 1600);
    } catch (_) {
      app.toast(t('common.copy_failed'), 'err');
    }
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
    other: 'history.kind_other'
  };
  // 内容不可用原因（event-first 记录状态）；值为 i18n key。
  app.UNAVAILABLE_LABEL_KEY = {
    superseded_before_read: 'history.unavail_superseded',
    unsupported_format: 'history.unavail_format',
    text_limit_exceeded: 'history.unavail_text_limit',
    image_limit_exceeded: 'history.unavail_image_limit',
    read_failed: 'history.unavail_read_failed'
  };
  app.sensBtnHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>';
  app.isManagedImage = function isManagedImage(it) {
    return !!it && it.format === 'CLIPVAULT_IMAGE_FILE' && it.contentAvailable !== false;
  };
  app.loadManagedImage = async function loadManagedImage(img, itemId) {
    if (!img) return;
    try {
      const res = await app.authorizedItemFetch(itemId, '/api/v1/items/image?id=' + itemId);
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const url = URL.createObjectURL(await res.blob());
      app.fileObjectURLs.add(url);
      img.src = url;
    } catch (_) {
      img.closest('.managed-image')?.classList.add('managed-image-missing');
      img.style.display = 'none';
    }
  };
  app.doViewManagedImage = async function doViewManagedImage(id) {
    const item = app.items.get(id);
    if (!item) return;
    app.currentModalId = id;
    app.viewModalTrigger = document.activeElement;
    app.MODAL_TITLE.textContent = t('history.image_title', { id: id }) + (item.sensitive ? ' · ' + t('history.sensitive_tag') : '');
    app.MODAL_PRE.style.display = 'none';
    let preview = document.getElementById('modalFilePreview');
    if (!preview) {
      preview = document.createElement('div');
      preview.id = 'modalFilePreview';
      preview.className = 'modal-file-preview';
      document.querySelector('.modal-body').appendChild(preview);
    }
    preview.style.display = 'flex';
    preview.innerHTML = '<img alt="' + t('history.image_title', { id: id }) + '">';
    await app.loadManagedImage(preview.querySelector('img'), id);
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
  app.getBaseName = function getBaseName(p) {
    const m = p.match(/[\\/]?([^\\/]+)$/);
    return m ? m[1] : p;
  };
  app.getExt = function getExt(name) {
    const m = name.match(/\.([^./\\]+)$/);
    return m ? m[1].toLowerCase() : '';
  };
  app.formatSize = function formatSize(b) {
    if (b < 1024) return b + ' B';
    if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
    if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
    return (b / 1073741824).toFixed(2) + ' GB';
  };
  app.fileIconHTML = function fileIconHTML() {
    return '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>';
  };
  app.dlHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg> ' + t('history.download');
  app.dirHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg> ' + t('history.directory');
  app.hashHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="4" y1="9" x2="20" y2="9"/><line x1="4" y1="15" x2="20" y2="15"/><line x1="10" y1="3" x2="8" y2="21"/><line x1="16" y1="3" x2="14" y2="21"/></svg> Hash';
  app.fileObjectURLs = new Set();
  app.revokeFileObjectURLs = function revokeFileObjectURLs() {
    app.fileObjectURLs.forEach(url => URL.revokeObjectURL(url));
    app.fileObjectURLs.clear();
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
  app.loadFileImage = async function loadFileImage(img, itemId, idx) {
    if (!img) return;
    try {
      const res = await app.authorizedItemFetch(itemId, '/api/v1/files/content?item_id=' + itemId + '&file_index=' + idx);
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const url = URL.createObjectURL(await res.blob());
      app.fileObjectURLs.add(url);
      img.src = url;
    } catch (_) {
      img.style.display = 'none';
    }
  };
  app.renderFileList = function renderFileList(it) {
    const paths = it.text.split('\n').filter(Boolean);
    app.filePaths.set(it.id, paths);
    return '<div class="file-list">' + paths.map((p, idx) => {
      const name = app.getBaseName(p);
      const ext = app.getExt(name);
      const isImg = app.IMAGE_EXTS.includes(ext);
      return '<div class="file-row" id="frow-' + it.id + '-' + idx + '">' + (isImg ? '<div class="file-thumb" id="fthumb-' + it.id + '-' + idx + '"></div>' : '<div class="file-icon">' + app.fileIconHTML() + '</div>') + '<div class="file-info">' + '<div class="file-name">' + app.esc(name) + '</div>' + '<div class="file-path">' + app.esc(p) + '</div>' + '<div class="file-status" id="fstat-' + it.id + '-' + idx + '"><span class="fcheck">' + t('history.checking') + '</span></div>' + '<div class="file-hash" id="fhash-' + it.id + '-' + idx + '"></div>' + '</div>' + '<div class="file-actions">' + '<button class="ab dl" data-action="file-download" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fdl-' + it.id + '-' + idx + '" disabled>' + app.dlHTML + '</button>' + '<button class="ab dir" data-action="file-open-directory" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fdir-' + it.id + '-' + idx + '" disabled>' + app.dirHTML + '</button>' + '<button class="ab hash" data-action="file-hash" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fhashbtn-' + it.id + '-' + idx + '" disabled>' + app.hashHTML + '</button>' + '</div>' + '</div>';
    }).join('') + '</div>';
  };
  app.checkFiles = async function checkFiles(itemId) {
    const paths = app.filePaths.get(itemId);
    if (!paths) return;
    for (let idx = 0; idx < paths.length; idx++) {
      const p = paths[idx];
      try {
        const fileRef = 'item_id=' + itemId + '&file_index=' + idx;
        const d = await app.authorizedItemFetch(itemId, '/api/v1/files/check?' + fileRef).then(r => r.json());
        const stat = document.getElementById('fstat-' + itemId + '-' + idx);
        const dl = document.getElementById('fdl-' + itemId + '-' + idx);
        const dir = document.getElementById('fdir-' + itemId + '-' + idx);
        const hashBtn = document.getElementById('fhashbtn-' + itemId + '-' + idx);
        if (d.exists) {
          if (stat) stat.innerHTML = '<span class="fok">\u2713 ' + app.formatSize(d.size) + '</span>';
          if (dl) dl.disabled = false;
          if (dir) dir.disabled = false;
          if (hashBtn) hashBtn.disabled = false;
          const ext = app.getExt(d.name || p);
          if (app.IMAGE_EXTS.includes(ext)) {
            const thumb = document.getElementById('fthumb-' + itemId + '-' + idx);
            if (thumb && ext !== 'svg') {
              thumb.innerHTML = '<img loading="lazy" alt="' + app.esc(d.name || p) + '" data-action="file-view" data-item-id="' + itemId + '" data-file-index="' + idx + '">';
              app.loadFileImage(thumb.querySelector('img'), itemId, idx);
            }
          }
        } else {
          if (stat) stat.innerHTML = '<span class="fmiss">\u26a0 ' + t('history.file_missing') + '</span>';
        }
      } catch (_) {
        const stat = document.getElementById('fstat-' + itemId + '-' + idx);
        if (stat) stat.innerHTML = '<span class="fmiss">\u26a0 ' + t('history.file_check_failed') + '</span>';
      }
    }
  };
  app.doDownload = async function doDownload(itemId, idx) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    try {
      const res = await app.authorizedItemFetch(itemId, '/api/v1/files/content?item_id=' + itemId + '&file_index=' + idx + '&download=1');
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement('a');
      a.href = url;
      a.download = app.getBaseName(paths[idx]);
      a.style.display = 'none';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      app.toast(t('history.download_started'));
    } catch (e) {
      app.toast(t('history.download_failed_with', { message: e.message || e }), 'err');
    }
  };
  app.doOpenDir = async function doOpenDir(itemId, idx) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const btn = document.getElementById('fdir-' + itemId + '-' + idx);
    if (btn) {
      btn.disabled = true;
    }
    try {
      const res = await app.authorizedItemFetch(itemId, '/api/v1/files/open-directory?item_id=' + itemId + '&file_index=' + idx, {
        method: 'POST'
      });
      if (!res.ok) {
        const failure = await app.readAPIError(res, t('history.open_dir_failed'));
        throw new app.APIError(failure.code, failure.message, failure.status);
      }
      app.toast(t('history.opened_dir'));
    } catch (e) {
      app.toast(t('history.open_dir_failed_with', { message: e.message || e }), 'err');
    } finally {
      if (btn) {
        btn.disabled = false;
      }
    }
  };
  app.doFileHash = async function doFileHash(itemId, idx) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const btn = document.getElementById('fhashbtn-' + itemId + '-' + idx);
    const hashDiv = document.getElementById('fhash-' + itemId + '-' + idx);
    if (btn) {
      btn.disabled = true;
    }
    if (hashDiv) hashDiv.innerHTML = '<span class="fcheck">' + t('history.hash_computing') + '</span>';
    try {
      const d = await app.authorizedItemFetch(itemId, '/api/v1/files/hash?item_id=' + itemId + '&file_index=' + idx).then(r => r.json());
      if (d.ok) {
        if (hashDiv) hashDiv.innerHTML = '<div class="fhash-line"><span class="fhash-label">MD5</span> <span class="fhash-val">' + app.esc(d.md5) + '</span></div>' + '<div class="fhash-line"><span class="fhash-label">SHA256</span> <span class="fhash-val">' + app.esc(d.sha256) + '</span></div>';
      } else {
        if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 ' + app.esc(d.error || t('history.hash_failed')) + '</span>';
      }
    } catch (e) {
      if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 ' + t('history.hash_failed') + '</span>';
    } finally {
      if (btn) {
        btn.disabled = false;
      }
    }
  };
  app.doFileView = async function doFileView(itemId, idx) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const p = paths[idx];
    const name = app.getBaseName(p);
    app.currentModalId = itemId;
    app.MODAL_TITLE.textContent = name;
    app.MODAL_PRE.style.display = 'none';
    let preview = document.getElementById('modalFilePreview');
    if (!preview) {
      preview = document.createElement('div');
      preview.id = 'modalFilePreview';
      preview.className = 'modal-file-preview';
      document.querySelector('.modal-body').appendChild(preview);
    }
    preview.style.display = 'flex';
    preview.innerHTML = app.getExt(name) === 'svg' ? '<div class="fmiss">' + t('history.svg_no_preview') + '</div>' : '<img alt="' + app.esc(name) + '">';
    if (app.getExt(name) !== 'svg') await app.loadFileImage(preview.querySelector('img'), itemId, idx);
    app.VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  };
}
