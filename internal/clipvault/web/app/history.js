  }

  function relTime(s) {
    const d = Math.floor((Date.now() - new Date(s).getTime()) / 1000);
    if (d < 5)      return '刚刚';
    if (d < 60)     return d + '秒前';
    if (d < 3600)   return Math.floor(d / 60) + '分钟前';
    if (d < 86400)  return Math.floor(d / 3600) + '小时前';
    if (d < 604800) return Math.floor(d / 86400) + '天前';
    return new Date(s).toLocaleString();
  }

  // ── State ─────────────────────────────────────────────────
  async function refreshState() {
    try {
      const d = await fetch('/api/v1/runtime').then(r => r.json());
      DOT.className = 'dot' + (d.paused ? ' paused' : '');
      ST.textContent = d.paused ? '已暂停' : '记录中';
      TOG.className = 'btn-toggle' + (d.paused ? ' off' : '');
      TOG.disabled = false;
      TOGTX.textContent = d.paused ? '恢复' : '暂停';
      const togIcon = document.getElementById('togIcon');
      togIcon.innerHTML = d.paused
        ? '<polygon points="5 3 19 12 5 21 5 3"/>'
        : '<circle cx="12" cy="12" r="10"/><line x1="10" y1="15" x2="10" y2="9"/><line x1="14" y1="15" x2="14" y2="9"/>';
    } catch (_) {}
  }

  // ── Copy ──────────────────────────────────────────────────
  const cpHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg> 复制';

  async function fetchSensitiveText(id) {
    const url = '/api/v1/items/sensitive/item?id=' + id;
    const res = await authorizedItemFetch(id, url);
    if (!res.ok) return null;
    const d = await res.json();
    return d.item ? d.item.text : null;
  }

  async function doCopy(id, btn) {
    const item = items.get(id);
    if (!item) return;
    let textToCopy = item.text;
    if (item.sensitive) {
      try {
        textToCopy = await fetchSensitiveText(id);
      } catch (_) {
        textToCopy = null;
      }
      if (textToCopy === null) { toast('需要二次验证才能复制敏感内容', 'err'); return; }
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg> 已复制';
      btn.classList.add('ok');
      toast('已复制到剪切板');
      setTimeout(() => { btn.innerHTML = cpHTML; btn.classList.remove('ok'); }, 1600);
    } catch (_) { toast('复制失败', 'err'); }
  }

  // ── Delete ────────────────────────────────────────────────
  async function doDelete(id, art) {
    try {
      const res = await authorizedItemFetch(id, '/api/v1/items?id=' + id, { method: 'DELETE' });
      if (!res.ok) throw 0;
      art.style.transition = 'opacity .18s, transform .18s';
      art.style.opacity = '0';
      art.style.transform = 'translateX(12px)';
      setTimeout(() => art.remove(), 200);
      items.delete(id);
      BADGE.textContent = items.size;
      toast('已删除');
    } catch (_) { toast('删除失败', 'err'); }
  }

  // ── Star ──────────────────────────────────────────────────
  const starOnHTML  = '<svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="1.5"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>';
  const starOffHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>';

  async function doStar(id, btn) {
    const item = items.get(id);
    if (!item) return;
    const newStarred = !item.starred;
    try {
      const res = await authorizedItemFetch(id, '/api/v1/items?id=' + id + '&starred=' + (newStarred ? '1' : '0'), { method: 'PATCH' });
      if (!res.ok) throw 0;
      item.starred = newStarred;
      const art = btn.closest('article');
      btn.innerHTML = newStarred ? starOnHTML : starOffHTML;
      btn.classList.toggle('on', newStarred);
      btn.title = newStarred ? '取消收藏' : '收藏';
      art.dataset.starred = newStarred ? '1' : '0';
      toast(newStarred ? '已收藏' : '已取消收藏');
    } catch (_) { toast('操作失败', 'err'); }
  }

  // ── Expand ────────────────────────────────────────────────
  function doExpand(btn) {} // 已在此版本废弃，功能由 doView 弹窗替代

  // ── View modal ────────────────────────────────────────
  let currentModalId = 0;
  const VIEW_MODAL  = document.getElementById('viewModal');
  const MODAL_PRE   = document.getElementById('modalPre');
  const MODAL_TITLE = document.getElementById('modalTitle');

  async function doView(id) {
    const item = items.get(id);
    if (!item) return;
    currentModalId = id;
    const kindKey = item.kind || 'text';

    let displayText = item.text;
    if (item.sensitive) {
      try {
        const url = '/api/v1/items/sensitive/item?id=' + id;
        const res = await authorizedItemFetch(id, url);
        if (!res.ok) throw new Error('HTTP ' + res.status);
        const d = await res.json();
        if (d.item && d.item.text) {
          displayText = d.item.text;
        }
      } catch (_) { toast('获取敏感内容失败', 'err'); return; }
    }

    if (isNonTextKind(kindKey)) {
      const paths = displayText.split('\n').filter(Boolean);
      MODAL_TITLE.textContent = '文件列表 · #' + id;
      MODAL_PRE.style.display = 'none';
      let preview = document.getElementById('modalFilePreview');
      if (!preview) {
        preview = document.createElement('div');
        preview.id = 'modalFilePreview';
        preview.className = 'modal-file-preview';
        document.querySelector('.modal-body').appendChild(preview);
      }
      preview.style.display = 'flex';
      preview.innerHTML = paths.map((p, idx) => {
        const name = getBaseName(p);
        const ext = getExt(name);
        const isImg = IMAGE_EXTS.includes(ext);
        return '<div class="modal-file-item">' +
          (isImg && ext !== 'svg' ? '<img data-file-index="' + idx + '" loading="lazy" alt="' + esc(name) + '">' : '') +
          '<div class="modal-file-name">' + esc(name) + '</div>' +
          '<div class="modal-file-path">' + esc(p) + '</div>' +
        '</div>';
      }).join('');
      preview.querySelectorAll('img[data-file-index]').forEach(img => {
        loadFileImage(img, item.id, Number(img.dataset.fileIndex));
      });
    } else {
      MODAL_TITLE.textContent = '内容预览 · #' + id + (item.sensitive ? ' · 敏感' : '');
      MODAL_PRE.style.display = 'block';
      MODAL_PRE.textContent = displayText;
      const preview = document.getElementById('modalFilePreview');
      if (preview) preview.style.display = 'none';
    }
    VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  }

  closeModal = function() {
    VIEW_MODAL.classList.add('hidden');
    document.body.style.overflow = '';
    MODAL_PRE.style.display = 'block';
    const preview = document.getElementById('modalFilePreview');
    if (preview) { preview.innerHTML = ''; preview.style.display = 'none'; }
  };

  modalCopy = async function() {
    const item = items.get(currentModalId);
    if (!item) return;
    const btn = document.getElementById('modalCopyBtn');
    let textToCopy = item.text;
    if (item.sensitive) {
      try {
        textToCopy = await fetchSensitiveText(currentModalId);
      } catch (_) {
        textToCopy = null;
      }
      if (textToCopy === null) { toast('需要二次验证才能复制敏感内容', 'err'); return; }
    }
    try {
      await navigator.clipboard.writeText(textToCopy);
      btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg> 已复制';
      btn.classList.add('ok');
      toast('已复制到剪切板');
      setTimeout(() => {
        btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg> 复制';
        btn.classList.remove('ok');
      }, 1600);
    } catch (_) { toast('复制失败', 'err'); }
  };

  // ── Kind label map ────────────────────────────────
  const KIND_LABEL = {
    text:    '文本',
    image:   '图片',
    video:   '视频',
    archive: '压缩包',
    office:  'Office',
    files:   '其他文件',
  };

  // ── File helpers ──────────────────────────────────────────
  function isNonTextKind(k) { return NON_TEXT_KINDS.includes(k); }
  function getBaseName(p) { const m = p.match(/[\\/]?([^\\/]+)$/); return m ? m[1] : p; }
  function getExt(name) { const m = name.match(/\.([^./\\]+)$/); return m ? m[1].toLowerCase() : ''; }
  function formatSize(b) {
    if (b < 1024) return b + ' B';
    if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
    if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
    return (b / 1073741824).toFixed(2) + ' GB';
  }
  function fileIconHTML() {
    return '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>';
  }

  const dlHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg> 下载';
  const dirHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg> 目录';
  const hashHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="4" y1="9" x2="20" y2="9"/><line x1="4" y1="15" x2="20" y2="15"/><line x1="10" y1="3" x2="8" y2="21"/><line x1="16" y1="3" x2="14" y2="21"/></svg> Hash';
  const fileObjectURLs = new Set();

  function revokeFileObjectURLs() {
    fileObjectURLs.forEach(url => URL.revokeObjectURL(url));
    fileObjectURLs.clear();
  }

  async function authorizedItemFetch(itemId, url, init) {
    const sensitive = !!items.get(itemId)?.sensitive;
    if (!sensitive) return fetch(url, init);

    const run = async () => {
      for (let attempt = 0; attempt < 2; attempt++) {
        const ok = await requestSecondaryAuth();
        if (!ok) throw new Error('需要二次验证');
        const res = await secondaryFetch(url, init);
        if (res.status !== 403) return res;
        const failure = await res.clone().json().catch(() => ({}));
        if (failure.code !== 'secondary_required') return res;
        authState.secondaryAuthed = false;
        appStore.update({ secondaryAuthenticated: false });
      }
      throw new Error('需要二次验证');
    };
    const queued = protectedItemRequestQueue.then(run, run);
    protectedItemRequestQueue = queued.then(() => undefined, () => undefined);
    return queued;
  }

  async function loadFileImage(img, itemId, idx) {
    if (!img) return;
    try {
      const res = await authorizedItemFetch(itemId, '/api/v1/files/content?item_id=' + itemId + '&file_index=' + idx);
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const url = URL.createObjectURL(await res.blob());
      fileObjectURLs.add(url);
      img.src = url;
    } catch (_) {
      img.style.display = 'none';
    }
  }

  function renderFileList(it) {
    const paths = it.text.split('\n').filter(Boolean);
    filePaths.set(it.id, paths);
    return '<div class="file-list">' + paths.map((p, idx) => {
      const name = getBaseName(p);
      const ext = getExt(name);
      const isImg = IMAGE_EXTS.includes(ext);
      return '<div class="file-row" id="frow-' + it.id + '-' + idx + '">' +
        (isImg ? '<div class="file-thumb" id="fthumb-' + it.id + '-' + idx + '"></div>' : '<div class="file-icon">' + fileIconHTML() + '</div>') +
        '<div class="file-info">' +
          '<div class="file-name">' + esc(name) + '</div>' +
          '<div class="file-path">' + esc(p) + '</div>' +
          '<div class="file-status" id="fstat-' + it.id + '-' + idx + '"><span class="fcheck">检查中...</span></div>' +
          '<div class="file-hash" id="fhash-' + it.id + '-' + idx + '"></div>' +
        '</div>' +
        '<div class="file-actions">' +
          '<button class="ab dl" data-action="file-download" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fdl-' + it.id + '-' + idx + '" disabled>' + dlHTML + '</button>' +
          '<button class="ab dir" data-action="file-open-directory" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fdir-' + it.id + '-' + idx + '" disabled>' + dirHTML + '</button>' +
          '<button class="ab hash" data-action="file-hash" data-item-id="' + it.id + '" data-file-index="' + idx + '" id="fhashbtn-' + it.id + '-' + idx + '" disabled>' + hashHTML + '</button>' +
        '</div>' +
      '</div>';
    }).join('') + '</div>';
  }

  async function checkFiles(itemId) {
    const paths = filePaths.get(itemId);
    if (!paths) return;
    for (let idx = 0; idx < paths.length; idx++) {
      const p = paths[idx];
      try {
        const fileRef = 'item_id=' + itemId + '&file_index=' + idx;
        const d = await authorizedItemFetch(itemId, '/api/v1/files/check?' + fileRef).then(r => r.json());
        const stat = document.getElementById('fstat-' + itemId + '-' + idx);
        const dl = document.getElementById('fdl-' + itemId + '-' + idx);
        const dir = document.getElementById('fdir-' + itemId + '-' + idx);
        const hashBtn = document.getElementById('fhashbtn-' + itemId + '-' + idx);
        if (d.exists) {
          if (stat) stat.innerHTML = '<span class="fok">\u2713 ' + formatSize(d.size) + '</span>';
          if (dl) dl.disabled = false;
          if (dir) dir.disabled = false;
          if (hashBtn) hashBtn.disabled = false;
          const ext = getExt(d.name || p);
          if (IMAGE_EXTS.includes(ext)) {
            const thumb = document.getElementById('fthumb-' + itemId + '-' + idx);
            if (thumb && ext !== 'svg') {
              thumb.innerHTML = '<img loading="lazy" data-action="file-view" data-item-id="' + itemId + '" data-file-index="' + idx + '">';
              loadFileImage(thumb.querySelector('img'), itemId, idx);
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
  }

  async function doDownload(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    try {
      const res = await authorizedItemFetch(itemId, '/api/v1/files/content?item_id=' + itemId + '&file_index=' + idx + '&download=1');
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement('a');
      a.href = url;
      a.download = getBaseName(paths[idx]);
      a.style.display = 'none';
      document.body.appendChild(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      toast('开始下载');
    } catch (e) { toast('下载失败: ' + (e.message || e), 'err'); }
  }

  async function doOpenDir(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const btn = document.getElementById('fdir-' + itemId + '-' + idx);
    if (btn) { btn.disabled = true; }
    try {
      const res = await authorizedItemFetch(itemId, '/api/v1/files/open-directory?item_id=' + itemId + '&file_index=' + idx, { method: 'POST' });
      if (!res.ok) {
        const msg = await res.text().catch(() => '');
        throw new Error(msg || ('HTTP ' + res.status));
      }
      toast('已打开所在目录并选中文件');
    } catch (e) {
      toast('打开目录失败: ' + (e.message || e), 'err');
    } finally {
      if (btn) { btn.disabled = false; }
    }
  }

  async function doFileHash(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const btn = document.getElementById('fhashbtn-' + itemId + '-' + idx);
    const hashDiv = document.getElementById('fhash-' + itemId + '-' + idx);
    if (btn) { btn.disabled = true; }
    if (hashDiv) hashDiv.innerHTML = '<span class="fcheck">计算中...</span>';
    try {
      const d = await authorizedItemFetch(itemId, '/api/v1/files/hash?item_id=' + itemId + '&file_index=' + idx).then(r => r.json());
      if (d.ok) {
        if (hashDiv) hashDiv.innerHTML =
          '<div class="fhash-line"><span class="fhash-label">MD5</span> <span class="fhash-val">' + esc(d.md5) + '</span></div>' +
          '<div class="fhash-line"><span class="fhash-label">SHA256</span> <span class="fhash-val">' + esc(d.sha256) + '</span></div>';
      } else {
        if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 ' + esc(d.error || '计算失败') + '</span>';
      }
    } catch (e) {
      if (hashDiv) hashDiv.innerHTML = '<span class="fmiss">\u26a0 计算失败</span>';
    } finally {
      if (btn) { btn.disabled = false; }
    }
  }

  async function doFileView(itemId, idx) {
    const paths = filePaths.get(itemId);
    if (!paths || !paths[idx]) return;
    const p = paths[idx];
    const name = getBaseName(p);
    currentModalId = itemId;
    MODAL_TITLE.textContent = name;
    MODAL_PRE.style.display = 'none';
    let preview = document.getElementById('modalFilePreview');
    if (!preview) {
      preview = document.createElement('div');
      preview.id = 'modalFilePreview';
      preview.className = 'modal-file-preview';
      document.querySelector('.modal-body').appendChild(preview);
    }
    preview.style.display = 'flex';
    preview.innerHTML = getExt(name) === 'svg' ? '<div class="fmiss">SVG 文件仅支持下载，不提供内联预览</div>' : '<img alt="' + esc(name) + '">';
    if (getExt(name) !== 'svg') await loadFileImage(preview.querySelector('img'), itemId, idx);
    VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  }

  // ── Render ────────────────────────────────────────────────
  const viewHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg> 查看';
  const delHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';
  const groupBtnHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><path d="M9 12h6M9 16h4"/></svg>';
  const locateHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3"/></svg>';

  function render(arr, q) {
    revokeFileObjectURLs();
    items.clear();
    filePaths.clear();
    arr.forEach(i => items.set(i.id, i));
    // BADGE and RC updated by updatePagination()

    if (!arr.length) {
      LIST.innerHTML = `<div class="empty">
        <svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2">
          <path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/>
          <rect x="9" y="3" width="6" height="4" rx="1"/>
          <line x1="9" y1="12" x2="15" y2="12"/>
          <line x1="9" y1="16" x2="13" y2="16"/>
        </svg>
        <span>暂无剪切板记录</span>
      </div>`;
      return;
    }

    LIST.innerHTML = arr.map(it => {
      const kindKey   = it.kind || 'text';
      const isNonText = isNonTextKind(kindKey);
      const kindClass = 'mkind mkind-' + kindKey;
      const kindLabel = KIND_LABEL[kindKey] || kindKey;
      const fullTime  = esc(new Date(it.createdAt).toLocaleString());
      const starHTML  = it.starred ? starOnHTML : starOffHTML;
      const starTitle = it.starred ? '取消收藏' : '收藏';
      const sensHTML  = it.sensitive ? '<span class="msens">敏感</span>' : '';
      const sensAttr  = it.sensitive ? ' data-sensitive="1"' : '';
      // 来源应用（前台窗口法）：ShowExe 缺省视为 true；标签用别名（若有），悬停看窗口标题与完整路径
      let srcHTML = '';
      if (currentCfg?.source?.showExe !== false && it.sourceExeName) {
        const srcLabel = sourceAliases[it.sourceExeName] || it.sourceExeName;
        const srcTip = esc([it.sourceWindowTitle, it.sourceExePath].filter(Boolean).join('\n'));
        srcHTML = `<span class="msep">·</span><span class="msource" title="${srcTip}">${esc(srcLabel)}</span>`;
      }

      let bodyHTML, charText, showView;
      if (it.sensitive && !authState.secondaryAuthed) {
        // 敏感内容未通过二次认证时显示遮蔽占位
        bodyHTML = `<pre class="open sensitive-mask"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="u-inline-21"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>敏感内容已隐藏，点击查看按钮查看明文</pre>`;