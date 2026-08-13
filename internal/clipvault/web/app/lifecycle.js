  function renderFilterNode(node) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = GROUP_EXPAND.has(node.id);
    const active = currentGroupID === node.id;
    let html = '<div class="gt-node">';
    html += '<div class="gt-row' + (active ? ' active' : '') + '" data-id="' + node.id + '">';
    html += '<span class="gt-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + CHEVRON_SVG + '</span>';
    html += '<span class="gt-icon">' + FOLDER_SVG + '</span>';
    html += '<span class="gt-label">' + esc(node.name) + '</span>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gt-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(renderFilterNode).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  }

  // ── Assign mode node (multi-select with checkboxes) ────────────────────
  function renderAssignNode(node, itemGroupIds) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = GROUP_EXPAND.has(node.id);
    const checked = itemGroupIds.includes(node.id);
    let html = '<div class="gt-node">';
    html += '<div class="gt-row" data-id="' + node.id + '">';
    html += '<span class="gt-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + CHEVRON_SVG + '</span>';
    html += '<span class="gt-check' + (checked ? ' checked' : '') + '" data-id="' + node.id + '">' + CHECK_SVG + '</span>';
    html += '<span class="gt-icon">' + FOLDER_SVG + '</span>';
    html += '<span class="gt-label">' + esc(node.name) + '</span>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gt-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(function(n) { return renderAssignNode(n, itemGroupIds); }).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  }

  // ── Manage mode node (CRUD actions) ─────────────────────────────
  function renderManageNode(node) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = GROUP_EXPAND.has(node.id);
    let html = '<div class="gm-node">';
    html += '<div class="gm-row">';
    html += '<span class="gm-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + CHEVRON_SVG + '</span>';
    html += '<span class="gm-icon">' + FOLDER_SVG + '</span>';
    html += '<span class="gm-name" data-id="' + node.id + '">' + esc(node.name) + '</span>';
    html += '<div class="gm-actions">';
    html += '<button class="gm-act" data-action="add-child" data-id="' + node.id + '" title="添加子分组">' + PLUS_SVG + '</button>';
    html += '<button class="gm-act" data-action="rename" data-id="' + node.id + '" title="重命名">' + EDIT_SVG + '</button>';
    html += '<button class="gm-act danger" data-action="delete" data-id="' + node.id + '" title="删除">' + DEL_SVG + '</button>';
    html += '</div>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gm-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(renderManageNode).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  }

  // ── Bind modal events ────────────────────────────────────────
  function bindGroupModalEvents(mode, opts, itemGroupIds) {
    const body = document.getElementById('groupModalBody');
    if (!body) return;

    // Toggle expand/collapse for all modes
    body.querySelectorAll('.gt-toggle, .gm-toggle').forEach(function(toggle) {
      toggle.addEventListener('click', function(e) {
        e.stopPropagation();
        const id = parseInt(toggle.dataset.id);
        if (GROUP_EXPAND.has(id)) { GROUP_EXPAND.delete(id); } else { GROUP_EXPAND.add(id); }
        renderGroupModalContent();
      });
    });

    if (mode === 'filter') {
      // Single-select: clicking a row selects the group
      body.querySelectorAll('.gt-row').forEach(function(row) {
        row.addEventListener('click', function(e) {
          if (e.target.closest('.gt-toggle')) return;
          const id = parseInt(row.dataset.id);
          if (currentGroupID === id) {
            currentGroupID = 0;
          } else {
            currentGroupID = id;
          }
          currentKind = '__group__';
          document.querySelectorAll('.filter-chip').forEach(function(c) { c.classList.remove('active'); });
          GROUP_CHIP.classList.add('active');
          closeGroupModal();
          resetSearch();
        });
      });
    } else if (mode === 'assign') {
      // Multi-select: clicking a row or checkbox toggles membership
      body.querySelectorAll('.gt-row').forEach(function(row) {
        row.addEventListener('click', async function(e) {
          if (e.target.closest('.gt-toggle')) return;
          const id = parseInt(row.dataset.id);
          const checkEl = row.querySelector('.gt-check');
          const isChecked = checkEl.classList.contains('checked');
          try {
            if (isChecked) {
              const res = await authorizedItemFetch(opts.itemId, '/api/v1/items/group?item_id=' + opts.itemId + '&group_id=' + id, { method: 'DELETE' });
              if (!res.ok) throw new Error('HTTP ' + res.status);
              checkEl.classList.remove('checked');
              toast('已移出分组');
            } else {
              const res = await authorizedItemFetch(opts.itemId, '/api/v1/items/group', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ itemId: opts.itemId, groupId: id }),
              });
              if (!res.ok) throw new Error('HTTP ' + res.status);
              checkEl.classList.add('checked');
              toast('已加入分组');
            }
          } catch (_) { toast('操作失败', 'err'); }
        });
      });
    } else if (mode === 'manage') {
      // Add root group
      const addRootBtn = document.getElementById('gmAddRootBtn');
      if (addRootBtn) {
        addRootBtn.addEventListener('click', function() {
          showGroupEditDialog('create', 0, '', async function(name) {
            try {
              const res = await fetch('/api/v1/groups', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: name, parentId: 0 }),
              });
              const d = await res.json();
              if (d.ok) {
                toast('分组已创建');
                await loadGroups();
                renderGroupModalContent();
              } else { toast(d.error || '创建失败', 'err'); }
            } catch (_) { toast('创建失败', 'err'); }
          });
        });
      }
      // Action buttons
      body.querySelectorAll('.gm-act[data-action]').forEach(function(btn) {
        btn.addEventListener('click', function(e) {
          e.stopPropagation();
          const action = btn.dataset.action;
          const id = parseInt(btn.dataset.id);
          if (action === 'rename') startRenameGroup(id);
          else if (action === 'delete') confirmDeleteGroup(id);
          else if (action === 'add-child') quickAddChild(id);
        });
      });
    }
  }

  // ── Group CRUD helpers ───────────────────────────────────────
  function getParentId(id) {
    const g = allGroups.find(function(g) { return g.id === id; });
    return g ? g.parentId : 0;
  }

  function quickAddChild(parentId) {
    showGroupEditDialog('create', parentId, '', async function(name) {
      try {
        const res = await fetch('/api/v1/groups', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: name, parentId: parentId }),
        });
        const d = await res.json();
        if (d.ok) {
          toast('子分组已创建');
          GROUP_EXPAND.add(parentId);
          await loadGroups();
          renderGroupModalContent();
        } else { toast(d.error || '创建失败', 'err'); }
      } catch (_) { toast('创建失败', 'err'); }
    });
  }

  function startRenameGroup(id) {
    const nameEl = document.querySelector('.gm-name[data-id="' + id + '"]');
    if (!nameEl) return;
    const oldName = nameEl.textContent;
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'gm-name-input';
    input.value = oldName;
    nameEl.replaceWith(input);
    input.focus();
    input.select();

    async function save() {
      const newName = input.value.trim();
      if (!newName || newName === oldName) {
        renderGroupModalContent();
        return;
      }
      try {
        const res = await fetch('/api/v1/groups?id=' + id, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: newName, parentId: getParentId(id) }),
        });
        const d = await res.json();
        if (d.ok) {
          toast('已重命名');
          await loadGroups();
          renderGroupModalContent();
        } else { toast(d.error || '重命名失败', 'err'); renderGroupModalContent(); }
      } catch (_) { toast('重命名失败', 'err'); renderGroupModalContent(); }
    }

    input.addEventListener('blur', save);
    input.addEventListener('keydown', function(e) {
      if (e.key === 'Enter') { e.preventDefault(); input.blur(); }
      if (e.key === 'Escape') { renderGroupModalContent(); }
    });
  }

  function confirmDeleteGroup(id) {
    const g = allGroups.find(function(g) { return g.id === id; });
    if (!g) return;
    showConfirmDialog('删除分组', '确定删除分组「' + g.name + '」及其子分组吗？此操作不可撤销。', async function() {
      try {
        const res = await fetch('/api/v1/groups?id=' + id, { method: 'DELETE' });
        const d = await res.json();
        if (d.ok) {
          toast('分组已删除');
          await loadGroups();
          renderGroupModalContent();
        } else { toast('删除失败', 'err'); }
      } catch (_) { toast('删除失败', 'err'); }
    });
  }

  // ── Generic group edit dialog (create/rename) ─────────────────────────────
  function showGroupEditDialog(mode, parentId, defaultName, onConfirm) {
    const title = mode === 'create' ? '新建分组' : '重命名分组';
    const html =
      '<div class="modal-overlay" id="groupEditOverlay">' +
        '<div class="modal-box secondary-box" data-modal-panel>' +
          '<div class="modal-head">' +
            '<span class="modal-title">' + title + '</span>' +
            '<button class="modal-close" data-action="close-group-edit">' +
              '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>' +
            '</button>' +
          '</div>' +
          '<div class="modal-body u-inline-16">' +
            '<form id="groupEditForm" class="lock-form">' +
              '<input type="text" id="groupEditInput" class="lock-input u-inline-33" placeholder="分组名称" value="' + esc(defaultName) + '" autofocus>' +
              '<button type="submit" class="lock-btn">确定</button>' +
            '</form>' +
          '</div>' +
        '</div>' +
      '</div>';
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('groupEditOverlay');
    const input = document.getElementById('groupEditInput');
    const form = document.getElementById('groupEditForm');
    setTimeout(function() { input.focus(); input.select(); }, 50);

    closeGroupEditDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', function(e) { if (e.target === overlay) overlay.remove(); });

    form.addEventListener('submit', function(e) {
      e.preventDefault();
      const name = input.value.trim();
      if (!name) return;
      overlay.remove();
      onConfirm(name);
    });
  }

  // ── Generic confirm dialog ───────────────────────────────────────
  function showConfirmDialog(title, message, onConfirm) {
    const html =
      '<div class="modal-overlay" id="confirmDialogOverlay">' +
        '<div class="modal-box secondary-box" data-modal-panel>' +
          '<div class="modal-head"><span class="modal-title">' + title + '</span></div>' +
          '<div class="modal-body u-inline-16">' +
            '<p class="u-inline-34">' + esc(message) + '</p>' +
            '<div class="u-inline-19">' +
              '<button class="btn-cancel" data-action="close-confirm">取消</button>' +
              '<button class="lock-btn u-inline-35" id="confirmDialogBtn">删除</button>' +
            '</div>' +
          '</div>' +
        '</div>' +
      '</div>';
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('confirmDialogOverlay');
    closeConfirmDialog = function() { overlay.remove(); };
    overlay.addEventListener('click', function(e) { if (e.target === overlay) overlay.remove(); });
    document.getElementById('confirmDialogBtn').addEventListener('click', function() {
      overlay.remove();
      onConfirm();
    });
  }


  // ── Init ─────────────────────────────────────────────────
  // -- Scroll-to-top ------------------------------------------
  const SCROLL_TOP_BTN = document.getElementById('scrollTopBtn');
  let scrollTicking = false;

  window.addEventListener('scroll', () => {
    if (scrollTicking) return;
    scrollTicking = true;
    requestAnimationFrame(() => {
      SCROLL_TOP_BTN.classList.toggle('visible', window.scrollY > 300);
      scrollTicking = false;
    });
  }, { passive: true });

  SCROLL_TOP_BTN.addEventListener('click', () => {
    window.scrollTo({ top: 0, behavior: 'smooth' });
  });

  async function init() {
    setupAutoSave();
    await loadTheme();
    await loadLicenseStatus();
    setupSrcExeUI();
    await loadSourceExes();
    // 支持 URL 参数快速跳转：
    //   ?q=<text>      直接以文本搜索
    //   ?locate=<id>   定位到时间线中的指定词条（复用 doLocate 逻辑）
    const params = new URLSearchParams(window.location.search);
    const qParam = params.get('q');
    const locateParam = params.get('locate');
    if (locateParam) {
      const id = parseInt(locateParam, 10);
      if (id > 0) {
        // 清掉 URL 上的 locate 参数，避免刷新时重复定位
        try { history.replaceState(null, '', window.location.pathname); } catch (_) {}
        doLocate(id);
        loadGroups();
        return;
      }
    }
    if (qParam !== null) {
      Q.value = qParam;
      try { history.replaceState(null, '', window.location.pathname); } catch (_) {}
    }
    search();
    loadGroups();
  }

  // ── Boot ─────────────────────────────────────────────────
  (async function boot() {
	await loadPublicAppearance();
    const ok = await checkAuthStatus();
    if (ok) {
      init();
    }

    // 标签页切回前台时，刷新认证状态和数据
    document.addEventListener('visibilitychange', async () => {
      if (document.visibilityState !== 'visible') return;
      const wasSecondaryAuthed = authState.secondaryAuthed;
      await checkAuthStatus();
      await loadLicenseStatus();
      // 如果二次认证缓存已过期，重新搜索以遮蔽敏感内容
      if (wasSecondaryAuthed && !authState.secondaryAuthed) {
        search();
      }
    });
  })();
}());
