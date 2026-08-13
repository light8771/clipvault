        charText = it.charLen >= 1000 ? (it.charLen / 1000).toFixed(1) + 'k 字' : it.charLen + ' 字';
        showView = true;
      } else if (isNonText) {
        const paths = it.text.split('\n').filter(Boolean);
        bodyHTML = renderFileList(it);
        charText = paths.length + ' 个文件';
        showView = true;
      } else {
        const long = it.text.length > 500;
        bodyHTML = `<pre${long ? '' : ' class="open"'}>${highlight(it.text, q)}</pre>`;
        charText = it.charLen >= 1000 ? (it.charLen / 1000).toFixed(1) + 'k 字' : it.charLen + ' 字';
        showView = long;
      }

      return `<article data-id="${it.id}" data-kind="${kindKey}" data-starred="${it.starred ? '1' : '0'}"${sensAttr}>
        <div class="card-body">${bodyHTML}</div>
        <div class="card-foot">
          <div class="card-meta">
            <span class="mid">#${it.id}</span>
            <span class="msep">·</span>
            <span class="${kindClass}">${kindLabel}</span>
            ${sensHTML ? '<span class="msep">·</span>' + sensHTML : ''}
            <span class="msep">·</span>
            <span class="mtime" title="${fullTime}">${relTime(it.createdAt)}</span>
            <span class="msep">·</span>
            <span class="mchars">${charText}</span>
            ${srcHTML}
          </div>
          <div class="card-acts">
            ${showView ? `<button class="ab exp" data-action="view-item" data-item-id="${it.id}">${viewHTML}</button>` : ''}
            ${q ? `<button class="ab locate-btn" data-action="locate-item" data-item-id="${it.id}" title="定位到时间线">${locateHTML}</button>` : ''}
            <button class="ab cp" data-action="copy-item" data-item-id="${it.id}">${cpHTML}</button>
            <button class="ab star${it.starred ? ' on' : ''}" data-action="star-item" data-item-id="${it.id}" title="${starTitle}">${starHTML}</button>
            <button class="ab group-btn" data-action="assign-group" data-item-id="${it.id}" title="加入分组">${groupBtnHTML}</button>
            <button class="ab del" data-action="delete-item" data-item-id="${it.id}" title="删除">${delHTML}</button>
          </div>
        </div>
      </article>`;
    }).join('');

    arr.forEach(it => {
      if (isNonTextKind(it.kind || 'text')) checkFiles(it.id);
    });

    if (pendingLocateId) {
      const el = LIST.querySelector(`article[data-id="${pendingLocateId}"]`);
      if (el) {
        el.classList.add('locate-highlight');
        el.scrollIntoView({ behavior: 'smooth', block: 'center' });
        setTimeout(() => el.classList.remove('locate-highlight'), 3500);
      }
      pendingLocateId = 0;
    }
  }

  // ── Locate (jump to item in full timeline) ───────────────
  async function doLocate(id) {
    try {
      const d = await fetch('/api/v1/items/position?id=' + id).then(r => r.json());
      if (d.error) { toast(d.error, 'err'); return; }
      const pageSize = parseInt(PAGESIZE.value) || 50;
      pendingLocateId = id;
      locateActive = true;
      currentPage = Math.floor(d.position / pageSize);
      Q.value = '';
      clearTimeout(debounce);
      currentKind = '';
      currentGroupID = 0;
      filterDateFrom = '';
      filterDateTo = '';
      document.querySelectorAll('.filter-chip').forEach(c => c.classList.remove('active'));
      document.querySelector('.filter-chip[data-kind=""]').classList.add('active');
      const df = document.getElementById('dateFrom'); const dt = document.getElementById('dateTo');
      if (df) df.value = ''; if (dt) dt.value = '';
      const cdb = document.getElementById('clearDateBtn'); if (cdb) cdb.classList.add('hidden');
      await search();
      locateActive = false;
      clearTimeout(debounce);
    } catch (_) { toast('定位失败', 'err'); }
  }

  // ── Search ────────────────────────────────────────────────
  let searchRevision = 0;
  let searchController = null;
  function resetSearch() { if (locateActive) return; currentPage = 0; search(); }

  async function search() {
    const revision = ++searchRevision;
    if (searchController) searchController.abort();
    const controller = new AbortController();
    searchController = controller;
    const q = Q.value;
    const pageSize = parseInt(PAGESIZE.value) || 50;
    try {
      const offset = currentPage * pageSize;
      const p = new URLSearchParams({ q, limit: pageSize, offset });
      let url = '/api/v1/items';
      if (currentKind === '__starred__') {
        p.set('starred', '1');
      } else if (currentKind === '__sensitive__') {
        url = '/api/v1/items/sensitive';
      } else if (currentKind && currentKind !== '__group__') {
        p.set('kind', currentKind);
      }
      if (currentGroupID > 0) {
        p.set('group_id', currentGroupID);
      }
      if (filterDateFrom) p.set('date_from', filterDateFrom);
      if (filterDateTo)   p.set('date_to',   filterDateTo);
      if (url === '/api/v1/items' || url === '/api/v1/items/sensitive') {
        currentSourceExes.forEach(n => p.append('source_exe', n));
      }
      const request = url === '/api/v1/items/sensitive' ? protectedFetch : fetch;
      const response = await request(url + '?' + p, { signal: controller.signal });
      const d = await response.json();
      if (revision !== searchRevision) return;
      if (!response.ok) {
        toast(d.error || '搜索失败，请重试', 'err');
        return;
      }
      totalCount = d.total || 0;
      fullTotalCount = (d.fullTotal !== undefined) ? d.fullTotal : totalCount;
      render(d.items || [], q);
      updatePagination();
    } catch (e) {
      if (e && e.name === 'AbortError') return;
    }
  }

  // ── 来源应用（exe）下拉 ─────────────────────────────────
  async function loadSourceExes() {
    try {
      const data = await fetch('/api/v1/items/sources').then(r => r.json());
      allSourceExes = Array.isArray(data) ? data : [];
    } catch (_) {
      allSourceExes = [];
    }
    renderSrcExeDropdown();
  }

  function renderSrcExeDropdown() {
    const wrap = document.getElementById('srcExeDropdown');
    const list = document.getElementById('srcExeList');
    const counter = document.getElementById('srcExeCount');
    const filterInput = document.getElementById('srcExeFilter');
    if (!wrap || !list) return;
    const cfg = currentCfg || {};
    const recordingOn = cfg.recordSourceInfo !== false;
    if (!recordingOn) {
      wrap.classList.add('disabled');
      list.innerHTML = '<p class="src-exe-empty">来源采集已关闭</p>';
      if (counter) counter.textContent = '';
      return;
    }
    wrap.classList.remove('disabled');
    const srcCfg = cfg.source || {};
    const whitelist = srcCfg.exeWhitelist || [];
    const showAll = !whitelist.length;
    let items = showAll ? allSourceExes.slice() : allSourceExes.filter(e => whitelist.includes(e.name));
    const q = (filterInput && filterInput.value || '').trim().toLowerCase();
    if (q) {
      items = items.filter(e => {
        const label = (sourceAliases[e.name] || e.name).toLowerCase();
        return label.includes(q) || e.name.toLowerCase().includes(q);
      });
    }
    if (!items.length) {
      list.innerHTML = '<p class="src-exe-empty">无可筛选来源应用</p>';
    } else {
      list.innerHTML = items.map(e => {
        const label = sourceAliases[e.name] || e.name;
        const checked = currentSourceExes.includes(e.name);
        return `<label class="src-exe-opt"><input type="checkbox" value="${esc(e.name)}"${checked ? ' checked' : ''}><span class="src-exe-name">${esc(label)}</span><span class="src-exe-cnt">${e.count}</span></label>`;
      }).join('');
    }
    if (counter) counter.textContent = currentSourceExes.length ? '(' + currentSourceExes.length + ')' : '';
  }

  function setupSrcExeUI() {
    const btn = document.getElementById('srcExeBtn');
    const pop = document.getElementById('srcExePop');
    const list = document.getElementById('srcExeList');
    const filterInput = document.getElementById('srcExeFilter');
    if (btn && pop) {
      btn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        pop.hidden = !pop.hidden;
      });
      document.addEventListener('click', (e) => {
        if (pop.hidden) return;
        if (!pop.contains(e.target) && e.target !== btn && !btn.contains(e.target)) {
          pop.hidden = true;
        }
      });
    }
    if (filterInput) {
      filterInput.addEventListener('input', () => renderSrcExeDropdown());
    }
    if (list) {
      list.addEventListener('change', (e) => {
        const cb = e.target.closest('input[type="checkbox"]');
        if (!cb) return;
        const name = cb.value;
        if (cb.checked) {
          if (!currentSourceExes.includes(name)) currentSourceExes.push(name);
        } else {
          currentSourceExes = currentSourceExes.filter(n => n !== name);
        }
        renderSrcExeDropdown();
        search();
      });
    }
    const rsi = document.getElementById('cfg-recordSourceInfo');
    if (rsi) rsi.addEventListener('change', () => renderSrcExeDropdown());
  }

  function updatePagination() {
    const pageSize = parseInt(PAGESIZE.value) || 50;
    const PAG  = document.getElementById('pagination');
    const PREV = document.getElementById('prevBtn');
    const NEXT = document.getElementById('nextBtn');
    const PI   = document.getElementById('pageInfo');
    const JI   = document.getElementById('pageJumpInput');
    const totalPages = Math.ceil(totalCount / pageSize) || 1;
    const start = totalCount === 0 ? 0 : currentPage * pageSize + 1;
    const end   = Math.min((currentPage + 1) * pageSize, totalCount);
    PI.textContent = `${start}–${end} / 共 ${totalCount} 条`;
    PREV.disabled = currentPage === 0;
    NEXT.disabled = end >= totalCount;
    PAG.classList.toggle('hidden', totalCount === 0);
    // 未激活时 badge 始终显示全量总数（转化钩子），已激活时跟随当前结果
    BADGE.textContent = (!licenseState.licensed && fullTotalCount > 0) ? fullTotalCount : totalCount;
    // 限额未知（freeLimit 为 0）时整条提示隐藏，绝不渲染"仅显示最近 0 条"。
    const freeNote = (!licenseState.licensed && licenseState.freeLimit > 0 && fullTotalCount > totalCount)
      ? ' · 免费版仅显示最近 ' + licenseState.freeLimit + ' 条，激活后查看全部'
      : '';
    RC.textContent = totalCount + ' 条结果' + freeNote;
    if (JI) { JI.value = currentPage + 1; JI.max = totalPages; }
  }

  // ── Auto-refresh ──────────────────────────────────────────
  function setupAR() {
    clearInterval(arTimer);
    if (AR.checked) arTimer = setInterval(() => { search(); refreshState(); }, 3000);
  }

  // ── Events ────────────────────────────────────────────────
  Q.addEventListener('input', () => { clearTimeout(debounce); debounce = setTimeout(resetSearch, 180); });
  PAGESIZE.addEventListener('change', () => {
    let v = parseInt(PAGESIZE.value);
    if (isNaN(v) || v < 10) v = 10;
    if (v > 500) v = 500;
    PAGESIZE.value = v;
    resetSearch();
  });
  PAGESIZE.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); PAGESIZE.blur(); }
  });
  AR.addEventListener('change', setupAR);

  // ── Pagesize custom dropdown ─────────────────────────────
  const psDropdown = document.getElementById('pagesizeDropdown');
  const psDropdownBtn = document.getElementById('pagesizeDropdownBtn');
  psDropdownBtn.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    psDropdown.classList.toggle('open');
  });
  psDropdown.addEventListener('click', (e) => {
    const opt = e.target.closest('.pagesize-opt');
    if (!opt) return;
    PAGESIZE.value = opt.dataset.val;
    psDropdown.classList.remove('open');
    resetSearch();
  });
  document.addEventListener('click', () => { psDropdown.classList.remove('open'); });

  // ── Pagination buttons ─────────────────────────────────
  document.getElementById('prevBtn').addEventListener('click', () => {
    if (currentPage > 0) { currentPage--; search(); }
  });
  document.getElementById('nextBtn').addEventListener('click', () => {
    const pageSize = parseInt(PAGESIZE.value) || 50;
    if ((currentPage + 1) * pageSize < totalCount) { currentPage++; search(); }
  });

  // ── Page jump ────────────────────────────────────────────
  function doPageJump() {
    const pageSize = parseInt(PAGESIZE.value) || 50;
    const totalPages = Math.ceil(totalCount / pageSize) || 1;
    const ji = document.getElementById('pageJumpInput');
    let p = parseInt(ji.value);
    if (isNaN(p) || p < 1) p = 1;
    if (p > totalPages) p = totalPages;
    const newPage = p - 1;
    if (newPage === currentPage) return;
    currentPage = newPage;
    search();
  }
  document.getElementById('pageJumpBtn').addEventListener('click', doPageJump);
  document.getElementById('pageJumpInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); doPageJump(); }
  });

  // ── Date filter ───────────────────────────────────────
  const DATE_FROM  = document.getElementById('dateFrom');
  const DATE_TO    = document.getElementById('dateTo');
  const CLEAR_DATE = document.getElementById('clearDateBtn');

  function syncDateClear() {
    CLEAR_DATE.classList.toggle('hidden', !filterDateFrom && !filterDateTo);
  }
  DATE_FROM.addEventListener('change', () => {
    filterDateFrom = DATE_FROM.value;
    syncDateClear();
    resetSearch();
  });
  DATE_TO.addEventListener('change', () => {
    filterDateTo = DATE_TO.value;
    syncDateClear();
    resetSearch();
  });
  CLEAR_DATE.addEventListener('click', () => {
    DATE_FROM.value = '';
    DATE_TO.value   = '';
    filterDateFrom  = '';
    filterDateTo    = '';
    CLEAR_DATE.classList.add('hidden');
    resetSearch();
  });
  TOG.addEventListener('click', async () => {
    await fetch('/api/v1/runtime/pause', { method: 'POST' });
    await refreshState();
  });

  // ── Clear all ─────────────────────────────────────────────
  let clearTimer = 0;
  const CLEAR_BTN = document.getElementById('clearBtn');
  const CLEAR_TX  = document.getElementById('clearTx');
  CLEAR_BTN.addEventListener('click', async () => {
    if (clearTimer) {
      clearTimeout(clearTimer);
      clearTimer = 0;
      CLEAR_BTN.classList.add('loading');
      CLEAR_TX.textContent = '清除中...';
      try {
        const res = await protectedFetch('/api/v1/items/clear', { method: 'POST' });
        const d = await res.json();
        if (d.ok) {
          toast('已清除 ' + d.deleted + ' 条记录');
          items.clear();
          BADGE.textContent = '0';
          RC.textContent = '0 条结果';
          LIST.innerHTML = `<div class="empty">
            <svg width="44" height="44" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2">
              <path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/>
              <rect x="9" y="3" width="6" height="4" rx="1"/>
              <line x1="9" y1="12" x2="15" y2="12"/><line x1="9" y1="16" x2="13" y2="16"/>
            </svg>
            <span>暂无剪切板记录</span>
          </div>`;
        } else { throw 0; }
      } catch (e) {
        if (e && (e.code === 'secondary_required' || e.code === 'secondary_cancelled')) {
          toast('需要二次验证才能清除记录', 'err');
        } else {
          toast('清除失败', 'err');
        }
      }
      CLEAR_BTN.classList.remove('loading');
      CLEAR_BTN.classList.remove('confirm');
      CLEAR_TX.textContent = '清除';
      return;
    }
    CLEAR_BTN.classList.add('confirm');
    CLEAR_TX.textContent = '确认清除？';
    clearTimer = setTimeout(() => {
      clearTimer = 0;
      CLEAR_BTN.classList.remove('confirm');
      CLEAR_TX.textContent = '清除';
    }, 3000);
  });

  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      if (!document.getElementById('sourceAppsModal')?.classList.contains('hidden')) {
        closeSourceAppsDialog();
        return;
      }
      if (document.getElementById('groupModalOverlay')) {
        closeGroupModal();
        return;
      }
      if (!VIEW_MODAL.classList.contains('hidden')) {
        window.closeModal();
        return;
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.key === 'k') { e.preventDefault(); Q.focus(); Q.select(); }
    if (e.key === 'Escape' && document.activeElement === Q) { Q.value = ''; search(); Q.blur(); }
  });

  // ── Settings sidebar nav ───────────────────────────────────
  snavTo = function(sectionId, navId) {
    document.querySelectorAll('.snav-item').forEach(el => el.classList.remove('active'));
    const navEl = document.getElementById(navId);
    if (navEl) navEl.classList.add('active');
    const sec = document.getElementById(sectionId);
    if (sec) sec.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  // ── Settings ──────────────────────────────────────────────
  let currentCfg = null;
  let configRevision = 0;
  let configRequestSequence = 0;
  const assetOperationTail = { logo: Promise.resolve(), favicon: Promise.resolve(), background: Promise.resolve() };

  function replaceCurrentConfig(config) {
    configRevision++;
    currentCfg = config;
  }

  function updateCurrentConfig(update) {
    replaceCurrentConfig({ ...currentCfg, ...update });
  }

  async function reserveAssetOperation(kind) {
    const previous = assetOperationTail[kind];
    let complete;
    assetOperationTail[kind] = new Promise(resolve => { complete = resolve; });
    await previous;
    return complete;
  }

  const THEME_PALETTES = {
    mint: {
      light: { p: '#14B8A6', p2: '#0D9488', accent: '#F97316', bg: '#F0FDFB', bg2: '#E6F9F5', card: '#FFFFFF', border: 'rgba(20,184,166,.18)', tx: '#0F2825', sub: '#3A6863', muted: '#5C8C87' },
      dark:  { p: '#2DD4BF', p2: '#14B8A6', accent: '#FB923C', bg: '#070C14', bg2: '#0C1520', card: '#101820', border: 'rgba(45,212,191,.14)', tx: '#E8F6F3', sub: '#9AD3CC', muted: '#70AAA4' },
    },