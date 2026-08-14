import { app } from "./app-context.js";
import { trapModalFocus } from './modal-controller.js';
export function registerHistoryActions() {
  app.items = new Map();
  app.filePaths = new Map();
  app.NON_TEXT_KINDS = ['image', 'video', 'archive', 'office', 'files'];
  app.IMAGE_EXTS = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'svg', 'ico', 'tiff', 'tif', 'heic', 'heif', 'avif'];
  app.relTime = function relTime(s) {
    const d = Math.floor((Date.now() - new Date(s).getTime()) / 1000);
    if (d < 5) return '刚刚';
    if (d < 60) return d + '秒前';
    if (d < 3600) return Math.floor(d / 60) + '分钟前';
    if (d < 86400) return Math.floor(d / 3600) + '小时前';
    if (d < 604800) return Math.floor(d / 86400) + '天前';
    return new Date(s).toLocaleString();
  };
  app.cpHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg> 复制';
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
        app.toast('需要二次验证才能复制敏感内容', 'err');
        return;
      }
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg> 已复制';
      btn.classList.add('ok');
      app.toast('已复制到剪切板');
      setTimeout(() => {
        btn.innerHTML = app.cpHTML;
        btn.classList.remove('ok');
      }, 1600);
    } catch (_) {
      app.toast('复制失败', 'err');
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
      app.BADGE.textContent = app.items.size;
      app.toast('已删除');
    } catch (_) {
      app.toast('删除失败', 'err');
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
      btn.title = newStarred ? '取消收藏' : '收藏';
      art.dataset.starred = newStarred ? '1' : '0';
      app.toast(newStarred ? '已收藏' : '已取消收藏');
    } catch (_) {
      app.toast('操作失败', 'err');
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
        app.toast('获取敏感内容失败', 'err');
        return;
      }
    }
    if (app.isNonTextKind(kindKey)) {
      const paths = displayText.split('\n').filter(Boolean);
      app.MODAL_TITLE.textContent = '文件列表 · #' + id;
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
      app.MODAL_TITLE.textContent = '内容预览 · #' + id + (item.sensitive ? ' · 敏感' : '');
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
        app.toast('需要二次验证才能复制敏感内容', 'err');
        return;
      }
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg> 已复制';
      btn.classList.add('ok');
      app.toast('已复制到剪切板');
      setTimeout(() => {
        btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg> 复制';
        btn.classList.remove('ok');
      }, 1600);
    } catch (_) {
      app.toast('复制失败', 'err');
    }
  };

  // ── Kind label map ────────────────────────────────
  app.KIND_LABEL = {
    text: '文本',
    image: '图片',
    video: '视频',
    archive: '压缩包',
    office: 'Office',
    files: '其他文件'
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
  app.dlHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg> 下载';
  app.dirHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg> 目录';
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
      return '<div class="file-row" id="frow-' + it.id + '-' + idx + '">' + (isImg ? '<div class="file-thumb" id="fthumb-' + it.id + '-' + idx + '"></div>' : '<div class="file-icon">' + app.fileIconHTML() + '</div>') + '<div class="file-info">' + '<div class="file-name">' + app.esc(name) + '</div>' + '<div class="file-path">' + app.esc(p) + '</div>' + '<div class="file-status" id="fstat-' + it.id + '-' + idx + '"><span class="fcheck">检查中...</span></div>' + '<div class="file-hash" id="fhash-' + it.id + '-' + idx + '"></div>' + '</div>' + '<div class="file-actions">' + '<button class="ab dl" data-action="file-download" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fdl-' + it.id + '-' + idx + '" disabled>' + app.dlHTML + '</button>' + '<button class="ab dir" data-action="file-open-directory" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fdir-' + it.id + '-' + idx + '" disabled>' + app.dirHTML + '</button>' + '<button class="ab hash" data-action="file-hash" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fhashbtn-' + it.id + '-' + idx + '" disabled>' + app.hashHTML + '</button>' + '</div>' + '</div>';
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
              thumb.innerHTML = '<img loading="lazy" data-action="file-view" data-item-id="' + itemId + '" data-file-index="' + idx + '">';
              app.loadFileImage(thumb.querySelector('img'), itemId, idx);
            }
          }
        } else {
          if (stat) stat.innerHTML = '<span class="fmiss">\u26a0 \u6587\u4ef6\u4e0d\u5b58\u5728</span>';
        }
      } catch (_) {
        const stat = document.getElementById('fstat-' + itemId + '-' + idx);
        if (stat) stat.innerHTML = '<span class="fmiss">\u26a0 \u68c0\u67e5\u5931\u8d25</span>';
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
      app.toast('开始下载');
    } catch (e) {
      app.toast('下载失败: ' + (e.message || e), 'err');
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
        const failure = await app.readAPIError(res, '无法打开文件目录');
        throw new app.APIError(failure.code, failure.message, failure.status);
      }
      app.toast('已打开所在目录并选中文件');
    } catch (e) {
      app.toast('打开目录失败: ' + (e.message || e), 'err');
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
    if (hashDiv) hashDiv.innerHTML = '<span class="fcheck">计算中...</span>';
    try {
      const d = await app.authorizedItemFetch(itemId, '/api/v1/files/hash?item_id=' + itemId + '&file_index=' + idx).then(r => r.json());
      if (d.ok) {
        if (hashDiv) hashDiv.innerHTML = '<div class="fhash-line"><span class="fhash-label">MD5</span> <span class="fhash-val">' + app.esc(d.md5) + '</span></div>' + '<div class="fhash-line"><span class="fhash-label">SHA256</span> <span class="fhash-val">' + app.esc(d.sha256) + '</span></div>';
      } else {
        if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 ' + app.esc(d.error || '计算失败') + '</span>';
      }
    } catch (e) {
      if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 计算失败</span>';
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
    preview.innerHTML = app.getExt(name) === 'svg' ? '<div class="fmiss">SVG 文件仅支持下载，不提供内联预览</div>' : '<img alt="' + app.esc(name) + '">';
    if (app.getExt(name) !== 'svg') await app.loadFileImage(preview.querySelector('img'), itemId, idx);
    app.VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  };
}
