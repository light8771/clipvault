import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

export function registerHistoryFiles() {
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
  app.dlHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg> ' + app.esc(t('history.download'));
  app.dirHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg> ' + app.esc(t('history.directory'));
  app.hashHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="4" y1="9" x2="20" y2="9"/><line x1="4" y1="15" x2="20" y2="15"/><line x1="10" y1="3" x2="8" y2="21"/><line x1="16" y1="3" x2="14" y2="21"/></svg> ' + app.esc(t('history.hash'));
  app.fileObjectURLs = new Set();
  app.revokeFileObjectURLs = function revokeFileObjectURLs() {
    app.fileObjectURLs.forEach(url => URL.revokeObjectURL(url));
    app.fileObjectURLs.clear();
  };
  app.loadFileImage = async function loadFileImage(img, itemId, idx, interactive = true) {
    if (!img) return;
    try {
      const url = '/api/v1/files/content?item_id=' + itemId + '&file_index=' + idx;
      const res = await (interactive ? app.authorizedItemFetch(itemId, url) : app.fetch(url));
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const blob = await res.blob();
      if (!img.isConnected) return;
      const objectURL = URL.createObjectURL(blob);
      app.fileObjectURLs.add(objectURL);
      img.src = objectURL;
    } catch (_) {
      img.style.display = 'none';
    }
  };
  app.renderFileList = function renderFileList(it, paths = it.text.split('\n').filter(Boolean)) {
    const previous = app.filePaths.get(it.id);
    if (!previous || previous.length !== paths.length || paths.some((path, idx) => path !== previous[idx])) {
      app.filePaths.set(it.id, paths);
    }
    return '<div class="file-list" data-file-list-id="' + it.id + '">' + paths.map((p, idx) => {
      const name = app.getBaseName(p);
      const ext = app.getExt(name);
      const isImg = app.IMAGE_EXTS.includes(ext) && ext !== 'svg';
      const button = (action, className, label) => '<button class="ab ' + className + '" data-action="' + action + '" data-item-id="' + it.id + '" data-file-index="' + idx + '" disabled>' + label + '</button>';
      return '<div class="file-row" data-file-row-index="' + idx + '">' +
        (isImg ? '<div class="file-thumb"></div>' : '<div class="file-icon">' + app.fileIconHTML() + '</div>') +
        '<div class="file-info"><div class="file-name" title="' + app.esc(name) + '">' + app.esc(name) + '</div>' +
        '<div class="file-path" title="' + app.esc(p) + '">' + app.esc(p) + '</div>' +
        '<span class="file-type">' + app.esc(ext.toUpperCase() || t('history.kind_files')) + '</span>' +
        '<div class="file-status"><span class="fcheck">' + app.esc(t('history.checking')) + '</span></div>' +
        '<div class="file-hash"></div></div><div class="file-actions">' +
        (isImg ? button('file-view', 'view', app.esc(t('history.view'))) : '') +
        button('file-download', 'dl', app.dlHTML) + button('file-open-directory', 'dir', app.dirHTML) +
        button('file-hash', 'hash', app.hashHTML) + '</div></div>';
    }).join('') + '</div>';
  };
  function setFileAvailability(row, missing) {
    row.dataset.fileMissing = String(missing);
    row.querySelectorAll('.file-actions button').forEach(button => {
      button.disabled = missing || button.dataset.fileBusy === 'true';
    });
  }
  app.checkFiles = async function checkFiles(itemId, root = app.LIST, { signal } = {}) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !root) return;
    const lists = root.querySelectorAll('[data-file-list-id="' + itemId + '"]');
    for (const list of lists) for (let idx = 0; idx < paths.length; idx++) {
      const row = list.querySelector('[data-file-row-index="' + idx + '"]');
      const current = () => row?.isConnected && !signal?.aborted && app.filePaths.get(itemId) === paths;
      if (!current()) return;
      const stat = row.querySelector('.file-status');
      const p = paths[idx];
      try {
        const fileRef = 'item_id=' + itemId + '&file_index=' + idx;
        const response = await app.fetch('/api/v1/files/check?' + fileRef, { signal });
        if (!response.ok) throw new Error('HTTP ' + response.status);
        const d = await response.json();
        if (!current()) return;
        if (typeof d.exists !== 'boolean') throw new Error('Invalid file status');
        setFileAvailability(row, !d.exists);
        if (d.exists) {
          if (stat) stat.innerHTML = '<span class="fok">\u2713 ' + app.formatSize(d.size) + '</span>';
          const ext = app.getExt(d.name || p);
          if (app.IMAGE_EXTS.includes(ext)) {
            const thumb = row.querySelector('.file-thumb');
            if (thumb && ext !== 'svg') {
              thumb.innerHTML = '<img loading="lazy" alt="' + app.esc(d.name || p) + '" data-action="file-view" data-item-id="' + itemId + '" data-file-index="' + idx + '">';
              app.loadFileImage(thumb.querySelector('img'), itemId, idx, false);
            }
          }
        } else {
          if (stat) stat.innerHTML = '<span class="fmiss">\u26a0 ' + app.esc(t('history.file_missing')) + '</span>';
        }
      } catch (_) {
        if (!current()) return;
        // A quiet check can lack a one-time authorization. Explicit actions
        // must remain usable so they can request their own authorization.
        setFileAvailability(row, false);
        if (stat) stat.innerHTML = '<span class="fmiss">\u26a0 ' + app.esc(t('history.file_check_failed')) + '</span>';
      }
    }
  };
  app.doDownload = async function doDownload(itemId, idx) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    try {
      const res = await app.authorizedItemFetch(itemId, '/api/v1/files/content?item_id=' + itemId + '&file_index=' + idx + '&download=1');
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
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
  function setFileBusy(button, busy) {
    if (!button) return;
    button.dataset.fileBusy = String(busy);
    button.disabled = busy || button.closest('.file-row')?.dataset.fileMissing === 'true';
  }
  app.doOpenDir = async function doOpenDir(itemId, idx, btn) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    if (btn?.disabled) return;
    setFileBusy(btn, true);
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
      setFileBusy(btn, false);
    }
  };
  app.doFileHash = async function doFileHash(itemId, idx, btn) {
    const paths = app.filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    if (btn?.disabled) return;
    const hashDiv = btn?.closest('.file-row')?.querySelector('.file-hash');
    setFileBusy(btn, true);
    if (hashDiv) hashDiv.innerHTML = '<span class="fcheck">' + app.esc(t('history.hash_computing')) + '</span>';
    try {
      const response = await app.authorizedItemFetch(itemId, '/api/v1/files/hash?item_id=' + itemId + '&file_index=' + idx);
      if (!response.ok) throw new Error('HTTP ' + response.status);
      const d = await response.json();
      if (hashDiv && !hashDiv.isConnected) return;
      if (d.ok) {
        if (hashDiv) hashDiv.innerHTML = '<div class="fhash-line"><span class="fhash-label">MD5</span> <span class="fhash-val">' + app.esc(d.md5) + '</span></div>' + '<div class="fhash-line"><span class="fhash-label">SHA256</span> <span class="fhash-val">' + app.esc(d.sha256) + '</span></div>';
      } else {
        if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 ' + app.esc(d.error || t('history.hash_failed')) + '</span>';
      }
    } catch (e) {
      if (hashDiv?.isConnected) hashDiv.innerHTML = '<span class="fmiss">\u26a0 ' + app.esc(t('history.hash_failed')) + '</span>';
    } finally {
      setFileBusy(btn, false);
    }
  };
}
