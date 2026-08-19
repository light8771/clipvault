import { app } from "./app-context.js";
export function registerHistoryList() {
  app.Q = document.getElementById('q');
  app.PAGESIZE = document.getElementById('pagesize');
  app.LIST = document.getElementById('list');
  app.BADGE = document.getElementById('badge');
  app.RC = document.getElementById('rc');
  app.AR = document.getElementById('ar');
  app.debounce = 0;
  app.arTimer = 0;
  app.currentKind = '';
  app.currentPage = 0;
  app.totalCount = 0;
  app.fullTotalCount = 0;
  app.pendingLocateId = 0;
  app.locateActive = false;
  app.filterDateFrom = '';
  app.filterDateTo = '';
  app.currentGroupID = 0;
  app.viewHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg> 查看';
  app.delHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';
  app.groupBtnHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><path d="M9 12h6M9 16h4"/></svg>';
  app.locateHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3"/></svg>';
  app.render = function render(arr, q) {
    app.revokeFileObjectURLs();
    app.items.clear();
    app.filePaths.clear();
    arr.forEach(i => app.items.set(i.id, i));
    // BADGE and RC updated by updatePagination()

    if (!arr.length) {
      app.LIST.innerHTML = `<div class="empty">
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
    app.LIST.innerHTML = arr.map(it => {
      const kindKey = it.kind || 'text';
      const isNonText = app.isNonTextKind(kindKey);
      const kindClass = 'mkind mkind-' + kindKey;
      const kindLabel = app.KIND_LABEL[kindKey] || kindKey;
      const fullTime = app.esc(new Date(it.createdAt).toLocaleString());
      const starHTML = it.starred ? app.starOnHTML : app.starOffHTML;
      const starTitle = it.starred ? '取消收藏' : '收藏';
      const sensHTML = it.sensitive ? '<span class="msens">敏感</span>' : '';
      const sensAttr = it.sensitive ? ' data-sensitive="1"' : '';
      const dupHTML = it.isDuplicate ? '<span class="msep">·</span><span class="mdup">重复</span>' : '';
      // 来源应用（前台窗口法）：ShowExe 缺省视为 true；标签用别名（若有），悬停看窗口标题与完整路径
      let srcHTML = '';
      if (app.currentCfg?.source?.showExe !== false && it.sourceExeName) {
        const srcLabel = app.sourceAliases[it.sourceExeName] || it.sourceExeName;
        const srcTip = app.esc([it.sourceWindowTitle, it.sourceExePath].filter(Boolean).join('\n'));
        srcHTML = `<span class="msep">·</span><span class="msource" title="${srcTip}">${app.esc(srcLabel)}</span>`;
      }
      const managedImage = app.isManagedImage(it);
      let bodyHTML, charText, showView;
      let viewAction = 'view-item';
      if (it.sensitive && !app.authState.secondaryAuthed) {
        // 敏感内容未通过二次认证时显示遮蔽占位
        bodyHTML = `<pre class="open sensitive-mask"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="u-inline-21"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>敏感内容已隐藏，点击查看按钮查看明文</pre>`;
        charText = it.charLen >= 1000 ? (it.charLen / 1000).toFixed(1) + 'k 字' : it.charLen + ' 字';
        showView = true;
      } else if (it.contentAvailable === false) {
        // 事件优先：事件已落库但内容不可用（被覆盖/格式不支持/超限等）
        const reasonLabel = app.UNAVAILABLE_LABEL[it.unavailableReason] || '无可用内容';
        bodyHTML = `<pre class="open event-only">${app.esc(reasonLabel)}</pre>`;
        charText = '事件';
        showView = false;
      } else if (managedImage) {
        // 托管剪贴板图片（CLIPVAULT_IMAGE_FILE）：内联缩略图，点击查看大图
        bodyHTML = `<div class="managed-image"><img data-action="view-managed-image" data-item-id="${it.id}" loading="lazy" alt="剪贴板图片"></div>`;
        charText = '图片';
        try {
          const meta = JSON.parse(it.metaJson || '{}');
          if (meta.width && meta.height) charText = meta.width + '×' + meta.height;
        } catch (_) { /* 尺寸缺失时保持默认 */ }
        showView = true;
        viewAction = 'view-managed-image';
      } else if (isNonText) {
        const paths = it.text.split('\n').filter(Boolean);
        bodyHTML = app.renderFileList(it);
        charText = paths.length + ' 个文件';
        showView = true;
      } else {
        const preview = app.textPreviewBody(it, q);
        bodyHTML = preview.html;
        showView = preview.showView;
        charText = it.charLen >= 1000 ? (it.charLen / 1000).toFixed(1) + 'k 字' : it.charLen + ' 字';
      }
      const canCopy = it.contentAvailable !== false && !managedImage;
      return `<article data-id="${it.id}" data-kind="${kindKey}" data-starred="${it.starred ? '1' : '0'}"${sensAttr}>
        <div class="card-body">${bodyHTML}</div>
        <div class="card-foot">
          <div class="card-meta">
            <span class="mid">#${it.id}</span>
            <span class="msep">·</span>
            <span class="${kindClass}">${kindLabel}</span>
            ${sensHTML ? '<span class="msep">·</span>' + sensHTML : ''}
            ${dupHTML}
            <span class="msep">·</span>
            <span class="mtime" title="${fullTime}">${app.relTime(it.createdAt)}</span>
            <span class="msep">·</span>
            <span class="mchars">${charText}</span>
            ${srcHTML}
          </div>
          <div class="card-acts">
            ${showView ? `<button class="ab exp" data-action="${viewAction}" data-item-id="${it.id}">${app.viewHTML}</button>` : ''}
            ${q ? `<button class="ab locate-btn" data-action="locate-item" data-item-id="${it.id}" title="定位到时间线">${app.locateHTML}</button>` : ''}
            ${canCopy ? `<button class="ab cp" data-action="copy-item" data-item-id="${it.id}">${app.cpHTML}</button>` : ''}
            <button class="ab star${it.starred ? ' on' : ''}" data-action="star-item" data-item-id="${it.id}" title="${starTitle}">${starHTML}</button>
            <button class="ab sens${it.sensitive ? ' on' : ''}" data-action="toggle-sensitive" data-item-id="${it.id}" title="${it.sensitive ? '取消敏感标记' : '标记为敏感'}">${app.sensBtnHTML}</button>
            <button class="ab group-btn" data-action="assign-group" data-item-id="${it.id}" title="加入分组">${app.groupBtnHTML}</button>
            <button class="ab del" data-action="delete-item" data-item-id="${it.id}" title="删除">${app.delHTML}</button>
          </div>
        </div>
      </article>`;
    }).join('');
    arr.forEach(it => {
      if (app.isNonTextKind(it.kind || 'text') && it.format !== 'CLIPVAULT_IMAGE_FILE') app.checkFiles(it.id);
      if (app.isManagedImage(it)) {
        const img = app.LIST.querySelector(`article[data-id="${it.id}"] .managed-image img`);
        if (img) app.loadManagedImage(img, it.id);
      }
    });
    // 实测放开未溢出的文本预览卡片（去掉渐隐与多余「查看」按钮）
    app.releaseFittingPreviews();
    if (app.pendingLocateId) {
      const el = app.LIST.querySelector(`article[data-id="${app.pendingLocateId}"]`);
      if (el) {
        el.classList.add('locate-highlight');
        el.scrollIntoView({
          behavior: app.scrollBehavior(),
          block: 'center'
        });
        setTimeout(() => el.classList.remove('locate-highlight'), 3500);
      }
      app.pendingLocateId = 0;
    }
  };
  app.doLocate = async function doLocate(id) {
    try {
      const d = await app.fetch('/api/v1/items/position?id=' + id).then(r => r.json());
      if (d.error) {
        app.toast(d.error, 'err');
        return;
      }
      const pageSize = parseInt(app.PAGESIZE.value) || 50;
      app.pendingLocateId = id;
      app.locateActive = true;
      app.currentPage = Math.floor(d.position / pageSize);
      app.Q.value = '';
      clearTimeout(app.debounce);
      app.currentKind = '';
      app.currentGroupID = 0;
      app.filterDateFrom = '';
      app.filterDateTo = '';
      document.querySelectorAll('.filter-chip').forEach(c => c.classList.remove('active'));
      document.querySelector('.filter-chip[data-kind=""]').classList.add('active');
      const df = document.getElementById('dateFrom');
      const dt = document.getElementById('dateTo');
      if (df) df.value = '';
      if (dt) dt.value = '';
      const cdb = document.getElementById('clearDateBtn');
      if (cdb) cdb.classList.add('hidden');
      await app.search();
      app.locateActive = false;
      clearTimeout(app.debounce);
    } catch (_) {
      app.toast('定位失败', 'err');
    }
  };
  app.searchRevision = 0;
  app.searchController = null;
  app.resetSearch = function resetSearch() {
    if (app.locateActive) return;
    app.currentPage = 0;
    app.search();
  };
  app.search = async function search() {
    const revision = ++app.searchRevision;
    if (app.searchController) app.searchController.abort();
    const controller = new AbortController();
    app.searchController = controller;
    const q = app.Q.value;
    const pageSize = parseInt(app.PAGESIZE.value) || 50;
    try {
      const offset = app.currentPage * pageSize;
      const p = new URLSearchParams({
        q,
        limit: pageSize,
        offset
      });
      let url = '/api/v1/items';
      if (app.currentKind === '__starred__') {
        p.set('starred', '1');
      } else if (app.currentKind === '__sensitive__') {
        url = '/api/v1/items/sensitive';
      } else if (app.currentKind && app.currentKind !== '__group__') {
        p.set('kind', app.currentKind);
      }
      if (app.currentGroupID > 0) {
        p.set('group_id', app.currentGroupID);
      }
      if (app.filterDateFrom) p.set('date_from', app.filterDateFrom);
      if (app.filterDateTo) p.set('date_to', app.filterDateTo);
      if (url === '/api/v1/items' || url === '/api/v1/items/sensitive') {
        app.currentSourceExes.forEach(n => p.append('source_exe', n));
      }
      const request = url === '/api/v1/items/sensitive' ? app.protectedFetch : app.fetch;
      const response = await request(url + '?' + p, {
        signal: controller.signal
      });
      const d = await response.json();
      if (revision !== app.searchRevision) return;
      if (!response.ok) {
        app.toast(d.error || '搜索失败，请重试', 'err');
        return;
      }
      app.totalCount = d.total || 0;
      app.fullTotalCount = d.fullTotal !== undefined ? d.fullTotal : app.totalCount;
      app.render(d.items || [], q);
      app.updatePagination();
    } catch (e) {
      if (e && e.name === 'AbortError') return;
    }
  };
  app.renderSrcExeDropdown = function renderSrcExeDropdown() {
    const wrap = document.getElementById('srcExeDropdown');
    const btn = document.getElementById('srcExeBtn');
    const list = document.getElementById('srcExeList');
    const counter = document.getElementById('srcExeCount');
    const footInfo = document.getElementById('srcExeFootInfo');
    const resetBtn = document.getElementById('srcExeReset');
    const filterInput = document.getElementById('srcExeFilter');
    if (!wrap || !list) return;
    const selected = app.currentSourceExes.length;
    if (counter) {
      counter.textContent = selected || '';
      counter.classList.toggle('hidden', !selected);
    }
    if (btn) btn.classList.toggle('active', selected > 0);
    if (footInfo) footInfo.textContent = selected ? '已筛选 ' + selected + ' 个应用' : '显示全部来源';
    if (resetBtn) resetBtn.classList.toggle('hidden', !selected);
    const cfg = app.currentCfg || {};
    const recordingOn = cfg.recordSourceInfo !== false;
    if (!recordingOn) {
      wrap.classList.add('disabled');
      list.innerHTML = '<p class="src-exe-empty">来源采集已关闭</p>';
      return;
    }
    wrap.classList.remove('disabled');
    const srcCfg = cfg.source || {};
    const whitelist = srcCfg.exeWhitelist || [];
    const showAll = !whitelist.length;
    let items = showAll ? app.allSourceExes.slice() : app.allSourceExes.filter(e => whitelist.includes(e.name));
    const q = (filterInput && filterInput.value || '').trim().toLowerCase();
    if (q) {
      items = items.filter(e => {
        const label = (app.sourceAliases[e.name] || e.name).toLowerCase();
        return label.includes(q) || e.name.toLowerCase().includes(q);
      });
    }
    if (!items.length) {
      list.innerHTML = '<p class="src-exe-empty">没有匹配的应用</p>';
    } else {
      list.innerHTML = items.map(e => {
        const label = app.sourceAliases[e.name] || e.name;
        const checked = app.currentSourceExes.includes(e.name);
        return `<label class="src-exe-opt"><input type="checkbox" value="${app.esc(e.name)}"${checked ? ' checked' : ''}><span class="src-exe-check"><svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2"><polyline points="20 6 9 17 4 12"/></svg></span><span class="src-exe-name">${app.esc(label)}</span><span class="src-exe-cnt">${e.count}</span></label>`;
      }).join('');
    }
  };
  app.setupSrcExeUI = function setupSrcExeUI() {
    const btn = document.getElementById('srcExeBtn');
    const pop = document.getElementById('srcExePop');
    const list = document.getElementById('srcExeList');
    const filterInput = document.getElementById('srcExeFilter');
    const resetBtn = document.getElementById('srcExeReset');
    if (btn && pop) {
      btn.addEventListener('click', e => {
        e.preventDefault();
        e.stopPropagation();
        pop.hidden = !pop.hidden;
        btn.classList.toggle('open', !pop.hidden);
        if (!pop.hidden && filterInput) {
          filterInput.value = '';
          app.renderSrcExeDropdown();
          filterInput.focus();
        }
      });
      document.addEventListener('click', e => {
        if (pop.hidden) return;
        if (!pop.contains(e.target) && e.target !== btn && !btn.contains(e.target)) {
          pop.hidden = true;
          btn.classList.remove('open');
        }
      });
    }
    if (filterInput) {
      filterInput.addEventListener('input', () => app.renderSrcExeDropdown());
    }
    if (resetBtn) {
      resetBtn.addEventListener('click', () => {
        if (!app.currentSourceExes.length) return;
        app.currentSourceExes = [];
        app.renderSrcExeDropdown();
        app.search();
      });
    }
    if (list) {
      list.addEventListener('change', e => {
        const cb = e.target.closest('input[type="checkbox"]');
        if (!cb) return;
        const name = cb.value;
        if (cb.checked) {
          if (!app.currentSourceExes.includes(name)) app.currentSourceExes.push(name);
        } else {
          app.currentSourceExes = app.currentSourceExes.filter(n => n !== name);
        }
        app.renderSrcExeDropdown();
        app.search();
      });
    }
    const rsi = document.getElementById('cfg-recordSourceInfo');
    if (rsi) rsi.addEventListener('change', () => app.renderSrcExeDropdown());
  };
  app.updatePagination = function updatePagination() {
    const pageSize = parseInt(app.PAGESIZE.value) || 50;
    const PAG = document.getElementById('pagination');
    const PREV = document.getElementById('prevBtn');
    const NEXT = document.getElementById('nextBtn');
    const PI = document.getElementById('pageInfo');
    const JI = document.getElementById('pageJumpInput');
    const totalPages = Math.ceil(app.totalCount / pageSize) || 1;
    const start = app.totalCount === 0 ? 0 : app.currentPage * pageSize + 1;
    const end = Math.min((app.currentPage + 1) * pageSize, app.totalCount);
    PI.textContent = `${start}–${end} / 共 ${app.totalCount} 条`;
    PREV.disabled = app.currentPage === 0;
    NEXT.disabled = end >= app.totalCount;
    PAG.classList.toggle('hidden', app.totalCount === 0);
    // 未激活时 badge 始终显示全量总数（转化钩子），已激活时跟随当前结果
    app.BADGE.textContent = !app.licenseState.licensed && app.fullTotalCount > 0 ? app.fullTotalCount : app.totalCount;
    // 限额未知（freeLimit 为 0）时整条提示隐藏，绝不渲染"仅显示最近 0 条"。
    const freeNote = !app.licenseState.licensed && app.licenseState.freeLimit > 0 && app.fullTotalCount > app.totalCount ? ' · 免费版仅显示最近 ' + app.licenseState.freeLimit + ' 条，激活后查看全部' : '';
    app.RC.textContent = app.totalCount + ' 条结果' + freeNote;
    if (JI) {
      JI.value = app.currentPage + 1;
      JI.max = totalPages;
    }
  };
  app.setupAR = function setupAR() {
    clearInterval(app.arTimer);
    if (app.AR.checked) app.arTimer = setInterval(() => {
      app.search();
      app.refreshState();
    }, 3000);
  };
  // ── Events ────────────────────────────────────────────────
  app.Q.addEventListener('input', () => {
    clearTimeout(app.debounce);
    app.debounce = setTimeout(app.resetSearch, 180);
  });
  app.PAGESIZE.addEventListener('change', () => {
    let v = parseInt(app.PAGESIZE.value);
    if (isNaN(v) || v < 10) v = 10;
    if (v > 500) v = 500;
    app.PAGESIZE.value = v;
    app.resetSearch();
  });
  app.PAGESIZE.addEventListener('keydown', e => {
    if (e.key === 'Enter') {
      e.preventDefault();
      app.PAGESIZE.blur();
    }
  });
  app.AR.addEventListener('change', app.setupAR);

  // ── Pagesize custom dropdown ─────────────────────────────
  app.psDropdown = document.getElementById('pagesizeDropdown');
  app.psDropdownBtn = document.getElementById('pagesizeDropdownBtn');
  app.psDropdownBtn.addEventListener('click', e => {
    e.preventDefault();
    e.stopPropagation();
    app.psDropdown.classList.toggle('open');
  });
  app.psDropdown.addEventListener('click', e => {
    const opt = e.target.closest('.pagesize-opt');
    if (!opt) return;
    app.PAGESIZE.value = opt.dataset.val;
    app.psDropdown.classList.remove('open');
    app.resetSearch();
  });
  document.addEventListener('click', () => {
    app.psDropdown.classList.remove('open');
  });

  // ── Pagination buttons ─────────────────────────────────
  document.getElementById('prevBtn').addEventListener('click', () => {
    if (app.currentPage > 0) {
      app.currentPage--;
      app.search();
    }
  });
  document.getElementById('nextBtn').addEventListener('click', () => {
    const pageSize = parseInt(app.PAGESIZE.value) || 50;
    if ((app.currentPage + 1) * pageSize < app.totalCount) {
      app.currentPage++;
      app.search();
    }
  });

  // ── Page jump ────────────────────────────────────────────
  app.doPageJump = function doPageJump() {
    const pageSize = parseInt(app.PAGESIZE.value) || 50;
    const totalPages = Math.ceil(app.totalCount / pageSize) || 1;
    const ji = document.getElementById('pageJumpInput');
    let p = parseInt(ji.value);
    if (isNaN(p) || p < 1) p = 1;
    if (p > totalPages) p = totalPages;
    const newPage = p - 1;
    if (newPage === app.currentPage) return;
    app.currentPage = newPage;
    app.search();
  };
  document.getElementById('pageJumpBtn').addEventListener('click', app.doPageJump);
  document.getElementById('pageJumpInput').addEventListener('keydown', e => {
    if (e.key === 'Enter') {
      e.preventDefault();
      app.doPageJump();
    }
  });

  // ── Date filter ───────────────────────────────────────
  app.DATE_FROM = document.getElementById('dateFrom');
  app.DATE_TO = document.getElementById('dateTo');
  app.CLEAR_DATE = document.getElementById('clearDateBtn');
  app.syncDateClear = function syncDateClear() {
    app.CLEAR_DATE.classList.toggle('hidden', !app.filterDateFrom && !app.filterDateTo);
  };
  app.DATE_FROM.addEventListener('change', () => {
    app.filterDateFrom = app.DATE_FROM.value;
    app.syncDateClear();
    app.resetSearch();
  });
  app.DATE_TO.addEventListener('change', () => {
    app.filterDateTo = app.DATE_TO.value;
    app.syncDateClear();
    app.resetSearch();
  });
  app.CLEAR_DATE.addEventListener('click', () => {
    app.DATE_FROM.value = '';
    app.DATE_TO.value = '';
    app.filterDateFrom = '';
    app.filterDateTo = '';
    app.CLEAR_DATE.classList.add('hidden');
    app.resetSearch();
  });
  app.TOG.addEventListener('click', async () => {
    await app.fetch('/api/v1/runtime/pause', {
      method: 'POST'
    });
    await app.refreshState();
  });

  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      if (!document.getElementById('sourceAppsModal')?.classList.contains('hidden')) {
        app.closeSourceAppsDialog();
        return;
      }
      if (document.getElementById('groupModalOverlay')) {
        app.closeGroupModal();
        return;
      }
      if (!app.VIEW_MODAL.classList.contains('hidden')) {
        app.closeModal();
        return;
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
      e.preventDefault();
      app.Q.focus();
      app.Q.select();
    }
    if (e.key === 'Escape' && document.activeElement === app.Q) {
      app.Q.value = '';
      app.search();
      app.Q.blur();
    }
  });

  // ── Settings sidebar nav ───────────────────────────────────
}
