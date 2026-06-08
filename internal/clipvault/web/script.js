(function(){
  const Q    = document.getElementById('q'),
        PAGESIZE = document.getElementById('pagesize'),
        LIST = document.getElementById('list'),
        DOT  = document.getElementById('dot'),
        ST   = document.getElementById('st'),
        BADGE= document.getElementById('badge'),
        TOG  = document.getElementById('tog'),
        TOGTX= document.getElementById('togTx'),
        RC   = document.getElementById('rc'),
        AR   = document.getElementById('ar'),
        TC   = document.getElementById('tc');

  let debounce = 0, arTimer = 0, currentKind = '';
  let currentPage = 0, totalCount = 0;
  let filterDateFrom = '', filterDateTo = '';
  const items = new Map();

  // ── View switch ───────────────────────────────────────────
  window.switchView = function(view) {
    const isHistory = view === 'history';
    document.getElementById('viewHistory').classList.toggle('hidden', !isHistory);
    document.getElementById('viewSettings').classList.toggle('hidden', isHistory);
    document.getElementById('tabHistory').classList.toggle('active', isHistory);
    document.getElementById('tabSettings').classList.toggle('active', !isHistory);
    if (!isHistory) loadSettings();
  };

  // ── Filter chips ──────────────────────────────────────────
  document.getElementById('kindFilters').addEventListener('click', e => {
    const chip = e.target.closest('.filter-chip');
    if (!chip) return;
    document.querySelectorAll('.filter-chip').forEach(c => c.classList.remove('active'));
    chip.classList.add('active');
    currentKind = chip.dataset.kind;
    resetSearch();
  });

  // ── Toast ─────────────────────────────────────────────────
  function toast(msg, type = 'ok') {
    const el = document.createElement('div');
    el.className = 'tk ' + type;
    el.textContent = msg;
    TC.appendChild(el);
    requestAnimationFrame(() => el.classList.add('show'));
    setTimeout(() => {
      el.classList.remove('show');
      setTimeout(() => el.remove(), 260);
    }, 2200);
  }

  // ── Utils ─────────────────────────────────────────────────
  function esc(v) {
    return String(v).replace(/[&<>"']/g, c =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])
    );
  }

  function highlight(text, q) {
    const e = esc(text);
    if (!q.trim()) return e;
    const p = esc(q).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    try { return e.replace(new RegExp(p, 'gi'), m => '<mark>' + m + '</mark>'); }
    catch (_) { return e; }
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
      const d = await fetch('/api/state').then(r => r.json());
      DOT.className = 'dot' + (d.paused ? ' paused' : '');
      ST.textContent = d.paused ? '已暂停' : '记录中';
      TOG.className = 'btn-toggle' + (d.paused ? ' off' : '');
      TOGTX.textContent = d.paused ? '恢复' : '暂停';
      const togIcon = document.getElementById('togIcon');
      togIcon.innerHTML = d.paused
        ? '<polygon points="5 3 19 12 5 21 5 3"/>'
        : '<circle cx="12" cy="12" r="10"/><line x1="10" y1="15" x2="10" y2="9"/><line x1="14" y1="15" x2="14" y2="9"/>';
    } catch (_) {}
  }

  // ── Copy ──────────────────────────────────────────────────
  const cpHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg> 复制';

  async function doCopy(id, btn) {
    const item = items.get(id);
    if (!item) return;
    try {
      await navigator.clipboard.writeText(item.text);
      btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg> 已复制';
      btn.classList.add('ok');
      toast('已复制到剪切板');
      setTimeout(() => { btn.innerHTML = cpHTML; btn.classList.remove('ok'); }, 1600);
    } catch (_) { toast('复制失败', 'err'); }
  }

  // ── Delete ────────────────────────────────────────────────
  async function doDelete(id, art) {
    try {
      const res = await fetch('/api/items?id=' + id, { method: 'DELETE' });
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
      const res = await fetch('/api/items?id=' + id + '&starred=' + (newStarred ? '1' : '0'), { method: 'PATCH' });
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

  function doView(id) {
    const item = items.get(id);
    if (!item) return;
    currentModalId = id;
    MODAL_PRE.textContent = item.text;
    MODAL_TITLE.textContent = '内容预览 · #' + id;
    VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  }

  window.closeModal = function() {
    VIEW_MODAL.classList.add('hidden');
    document.body.style.overflow = '';
  };

  window.modalCopy = async function() {
    const item = items.get(currentModalId);
    if (!item) return;
    const btn = document.getElementById('modalCopyBtn');
    try {
      await navigator.clipboard.writeText(item.text);
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

  // ── Render ────────────────────────────────────────────────
  const viewHTML = '<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg> 查看';
  const delHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';

  function render(arr, q) {
    items.clear();
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
      const long      = it.text.length > 500;
      const kindKey   = it.kind || 'text';
      const kindClass = 'mkind mkind-' + kindKey;
      const kindLabel = KIND_LABEL[kindKey] || kindKey;
      const fullTime  = esc(new Date(it.createdAt).toLocaleString());
      const charText  = it.charLen >= 1000
        ? (it.charLen / 1000).toFixed(1) + 'k 字'
        : it.charLen + ' 字';
      const starHTML  = it.starred ? starOnHTML : starOffHTML;
      const starTitle = it.starred ? '取消收藏' : '收藏';

      return `<article data-id="${it.id}" data-kind="${kindKey}" data-starred="${it.starred ? '1' : '0'}">
        <div class="card-body">
          <pre${long ? '' : ' class="open"'}>${highlight(it.text, q)}</pre>
        </div>
        <div class="card-foot">
          <div class="card-meta">
            <span class="mid">#${it.id}</span>
            <span class="msep">·</span>
            <span class="${kindClass}">${kindLabel}</span>
            <span class="msep">·</span>
            <span class="mtime" title="${fullTime}">${relTime(it.createdAt)}</span>
            <span class="msep">·</span>
            <span class="mchars">${charText}</span>
          </div>
          <div class="card-acts">
            ${long ? `<button class="ab exp" onclick="doView(${it.id})">${viewHTML}</button>` : ''}
            <button class="ab cp" onclick="doCopy(${it.id},this)">${cpHTML}</button>
            <button class="ab star${it.starred ? ' on' : ''}" onclick="doStar(${it.id},this)" title="${starTitle}">${starHTML}</button>
            <button class="ab del" onclick="doDelete(${it.id},this.closest('article'))" title="删除">${delHTML}</button>
          </div>
        </div>
      </article>`;
    }).join('');
  }

  // ── Search ────────────────────────────────────────────────
    function resetSearch() { currentPage = 0; search(); }

  async function search() {
    const q = Q.value;
    const pageSize = parseInt(PAGESIZE.value) || 50;
    try {
      const p = new URLSearchParams({ q, limit: pageSize, offset: currentPage * pageSize });
      if (currentKind === '__starred__') {
        p.set('starred', '1');
      } else if (currentKind) {
        p.set('kind', currentKind);
      }
      if (filterDateFrom) p.set('date_from', filterDateFrom);
      if (filterDateTo)   p.set('date_to',   filterDateTo);
      const d = await fetch('/api/search?' + p).then(r => r.json());
      totalCount = d.total || 0;
      render(d.items || [], q);
      updatePagination();
    } catch (_) {}
  }

  function updatePagination() {
    const pageSize = parseInt(PAGESIZE.value) || 50;
    const PAG  = document.getElementById('pagination');
    const PREV = document.getElementById('prevBtn');
    const NEXT = document.getElementById('nextBtn');
    const PI   = document.getElementById('pageInfo');
    const start = totalCount === 0 ? 0 : currentPage * pageSize + 1;
    const end   = Math.min((currentPage + 1) * pageSize, totalCount);
    PI.textContent = `${start}–${end} / 共 ${totalCount} 条`;
    PREV.disabled = currentPage === 0;
    NEXT.disabled = end >= totalCount;
    PAG.classList.toggle('hidden', totalCount === 0);
    BADGE.textContent = totalCount;
    RC.textContent = totalCount + ' 条结果';
  }

  // ── Auto-refresh ──────────────────────────────────────────
  function setupAR() {
    clearInterval(arTimer);
    if (AR.checked) arTimer = setInterval(() => { search(); refreshState(); }, 3000);
  }

  // ── Events ────────────────────────────────────────────────
  Q.addEventListener('input', () => { clearTimeout(debounce); debounce = setTimeout(resetSearch, 180); });
  PAGESIZE.addEventListener('change', resetSearch);
  AR.addEventListener('change', setupAR);

  // ── Pagination buttons ─────────────────────────────────
  document.getElementById('prevBtn').addEventListener('click', () => {
    if (currentPage > 0) { currentPage--; search(); }
  });
  document.getElementById('nextBtn').addEventListener('click', () => {
    const pageSize = parseInt(PAGESIZE.value) || 50;
    if ((currentPage + 1) * pageSize < totalCount) { currentPage++; search(); }
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
    await fetch('/api/pause', { method: 'POST' });
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
        const res = await fetch('/api/clear', { method: 'POST' });
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
      } catch (_) { toast('清除失败', 'err'); }
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
      if (!VIEW_MODAL.classList.contains('hidden')) {
        window.closeModal();
        return;
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.key === 'k') { e.preventDefault(); Q.focus(); Q.select(); }
    if (e.key === 'Escape' && document.activeElement === Q) { Q.value = ''; search(); Q.blur(); }
  });

  // ── Settings sidebar nav ───────────────────────────────────
  window.snavTo = function(sectionId, navId) {
    document.querySelectorAll('.snav-item').forEach(el => el.classList.remove('active'));
    const navEl = document.getElementById(navId);
    if (navEl) navEl.classList.add('active');
    const sec = document.getElementById(sectionId);
    if (sec) sec.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  // ── Settings ──────────────────────────────────────────────
  let currentCfg = null;

  async function loadSettings() {
    try {
      const cfg = await fetch('/api/config').then(r => r.json());
      currentCfg = cfg;
      applyConfigToForm(cfg);
    } catch (_) { toast('加载配置失败', 'err'); }
  }

  function applyConfigToForm(cfg) {
    document.getElementById('cfg-maxTextChars').value = cfg.maxTextChars ?? 0;
    document.getElementById('cfg-maxFileDropPaths').value = cfg.maxFileDropPaths ?? 0;

    const hkEnabled = document.getElementById('cfg-hotkey-enabled');
    hkEnabled.checked = cfg.hotkey && cfg.hotkey.enabled;
    toggleHotkeyDetail(hkEnabled.checked);

    // 修饰键
    const mods = (cfg.hotkey && cfg.hotkey.modifiers) || [];
    document.querySelectorAll('.mod-btn input').forEach(cb => {
      cb.checked = mods.includes(cb.value);
    });
    document.getElementById('cfg-hotkey-key').value = (cfg.hotkey && cfg.hotkey.key) || 'P';

    document.getElementById('cfg-listenAddr').value = (cfg.search && cfg.search.listenAddr) || '127.0.0.1:18765';

    // 自定义过滤规则
    const patterns = (cfg.sensitive && cfg.sensitive.customPatterns) || [];
    renderPatterns(patterns);
  }

  function toggleHotkeyDetail(enabled) {
    document.getElementById('hotkeyDetail').style.opacity = enabled ? '1' : '0.45';
    document.getElementById('hotkeyKey').style.opacity = enabled ? '1' : '0.45';
  }

  document.getElementById('cfg-hotkey-enabled').addEventListener('change', function() {
    toggleHotkeyDetail(this.checked);
  });

  // ── Pattern list ──────────────────────────────────────────
  function renderPatterns(patterns) {
    const list = document.getElementById('patternList');
    if (!patterns.length) {
      list.innerHTML = '<p class="no-patterns">暂无过滤规则，所有内容均会被记录</p>';
      return;
    }
    list.innerHTML = patterns.map((p, i) => `
      <div class="pattern-row" data-idx="${i}">
        <input type="text" class="pattern-input" value="${esc(p)}" placeholder="正则表达式">
        <button class="pattern-del" onclick="removePattern(${i})" title="删除">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
    `).join('');
  }

  window.removePattern = function(idx) {
    const patterns = getPatterns();
    patterns.splice(idx, 1);
    renderPatterns(patterns);
  };

  function getPatterns() {
    return Array.from(document.querySelectorAll('.pattern-input'))
      .map(i => i.value.trim())
      .filter(Boolean);
  }

  document.getElementById('addPatternBtn').addEventListener('click', () => {
    const patterns = getPatterns();
    patterns.push('');
    renderPatterns(patterns);
    // 聚焦最后一个输入框
    const inputs = document.querySelectorAll('.pattern-input');
    if (inputs.length) inputs[inputs.length - 1].focus();
  });

  // ── Save settings ──────────────────────────────────────────
  const SAVE_BTN = document.getElementById('saveBtn');
  const SAVE_TX  = document.getElementById('saveTx');

  SAVE_BTN.addEventListener('click', async () => {
    if (!currentCfg) return;
    const mods = [];
    document.querySelectorAll('.mod-btn input:checked').forEach(cb => mods.push(cb.value));

    const patterns = getPatterns();
    // 验证正则合法性
    for (const p of patterns) {
      try { new RegExp(p); } catch (_) {
        toast(`正则无效：${p}`, 'err');
        return;
      }
    }

    const newCfg = {
      ...currentCfg,
      maxTextChars:     parseInt(document.getElementById('cfg-maxTextChars').value) || 0,
      maxFileDropPaths: parseInt(document.getElementById('cfg-maxFileDropPaths').value) || 0,
      hotkey: {
        enabled:   document.getElementById('cfg-hotkey-enabled').checked,
        modifiers: mods,
        key:       document.getElementById('cfg-hotkey-key').value.trim() || 'P',
      },
      sensitive: { customPatterns: patterns },
      search: {
        ...currentCfg.search,
        listenAddr: document.getElementById('cfg-listenAddr').value.trim() || '127.0.0.1:18765',
      },
    };

    SAVE_BTN.disabled = true;
    SAVE_TX.textContent = '保存中...';
    try {
      const res = await fetch('/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newCfg),
      });
      if (!res.ok) {
        const txt = await res.text();
        throw new Error(txt);
      }
      const d = await res.json();
      currentCfg = d.config;
      toast('设置已保存');
      SAVE_TX.textContent = '已保存 ✓';
      setTimeout(() => { SAVE_TX.textContent = '保存设置'; }, 2000);
    } catch (e) {
      toast('保存失败：' + (e.message || e), 'err');
      SAVE_TX.textContent = '保存设置';
    }
    SAVE_BTN.disabled = false;
  });

  // expose for inline handlers
  window.doCopy = doCopy;
  window.doDelete = doDelete;
  window.doView = doView;
  window.doStar = doStar;
  window.switchView = window.switchView;
  window.removePattern = window.removePattern;

  // ── Export ─────────────────────────────────────────────────────────
  window.doExport = function(type) {
    const url = type === 'config'
      ? '/api/export/config'
      : '/api/export/data?format=' + (type === 'data-csv' ? 'csv' : 'json');
    // 使用临时链接触发浏览器下载
    const a = document.createElement('a');
    a.href = url;
    a.style.display = 'none';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    const label = type === 'config' ? '配置' : ('剪切板记录（' + (type === 'data-csv' ? 'CSV' : 'JSON') + '）');
    toast('开始导出 ' + label);
  };

  refreshState();
  search();
}());
