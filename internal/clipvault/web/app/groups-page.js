import { app } from "./app-context.js";

// 分组管理页：页内树形交互（无弹窗）。树节点操作（重命名输入框、
// 新建输入行、两步确认删除）全部在页面内完成。
export function registerGroupsPage() {
  // 内联编辑状态：非 null 表示正在 parentId（0=根）下创建新分组。
  app.groupCreatingUnder = null;

  // refreshGroupViews 统一刷新弹窗（若打开）与分组管理页（若存在）。
  app.refreshGroupViews = async function refreshGroupViews(reload) {
    if (reload) await app.loadGroups();
    if (app.groupModalMode) await app.renderGroupModalContent();
    if (document.getElementById('groupsPageTree')) await app.renderGroupsPage();
  };

  function groupCreateRowHTML() {
    return '<div class="gm-row gm-create-row">' +
      '<span class="gm-toggle no-children">' + app.CHEVRON_SVG + '</span>' +
      '<span class="gm-icon">' + app.FOLDER_SVG + '</span>' +
      '<input type="text" class="gm-name-input" id="groupCreateInput" placeholder="分组名称，Enter 确认 / Esc 取消" maxlength="100">' +
      '</div>';
  }

  app.renderPageNode = function renderPageNode(node) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = app.GROUP_EXPAND.has(node.id);
    const creatingHere = app.groupCreatingUnder === node.id;
    let html = '<div class="gm-node">';
    html += '<div class="gm-row">';
    html += '<span class="gm-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + app.CHEVRON_SVG + '</span>';
    html += '<span class="gm-icon">' + app.FOLDER_SVG + '</span>';
    html += '<span class="gm-name" data-id="' + node.id + '">' + app.esc(node.name) + '</span>';
    html += '<div class="gm-actions">';
    html += '<button class="gm-act" data-action="add-child" data-id="' + node.id + '" title="添加子分组">' + app.PLUS_SVG + '</button>';
    html += '<button class="gm-act" data-action="rename" data-id="' + node.id + '" title="重命名">' + app.EDIT_SVG + '</button>';
    html += '<button class="gm-act danger" data-action="delete" data-id="' + node.id + '" title="删除">' + app.DEL_SVG + '</button>';
    html += '</div></div>';
    if (hasChildren || creatingHere) {
      html += '<div class="gm-children' + (expanded || creatingHere ? ' expanded' : '') + '">';
      if (creatingHere) html += groupCreateRowHTML();
      if (hasChildren) html += node.children.map(app.renderPageNode).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  };

  app.renderGroupsPage = async function renderGroupsPage() {
    const tree = document.getElementById('groupsPageTree');
    if (!tree) return;
    await app.loadGroups();
    const total = document.getElementById('groupsPageTotal');
    if (total) total.textContent = '共 ' + app.allGroups.length + ' 个分组';
    let html = '';
    if (app.groupCreatingUnder === 0) html += groupCreateRowHTML();
    if (app.allGroups.length === 0) {
      html += '<div class="group-modal-empty">暂无分组，点击「新建分组」创建</div>';
    } else {
      html += app.buildGroupTree(0).map(app.renderPageNode).join('');
    }
    tree.innerHTML = html;
    bindGroupsPageEvents(tree);
    bindCreateInput();
  };

  function bindGroupsPageEvents(tree) {
    tree.querySelectorAll('.gm-toggle').forEach(function (toggle) {
      toggle.addEventListener('click', function (e) {
        e.stopPropagation();
        const id = parseInt(toggle.dataset.id);
        if (app.GROUP_EXPAND.has(id)) {
          app.GROUP_EXPAND.delete(id);
        } else {
          app.GROUP_EXPAND.add(id);
        }
        void app.renderGroupsPage();
      });
    });
    tree.querySelectorAll('.gm-act[data-action]').forEach(function (btn) {
      btn.addEventListener('click', function (e) {
        e.stopPropagation();
        const id = parseInt(btn.dataset.id);
        const action = btn.dataset.action;
        if (action === 'rename') {
          app.startRenameGroup(id);
        } else if (action === 'delete') {
          app.armGroupDelete(btn, id);
        } else if (action === 'add-child') {
          app.groupCreatingUnder = id;
          void app.renderGroupsPage();
        }
      });
    });
  }

  function bindCreateInput() {
    const input = document.getElementById('groupCreateInput');
    if (!input) return;
    const parentId = app.groupCreatingUnder || 0;
    input.focus();
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        void app.commitInlineCreate(input, parentId);
      }
      if (e.key === 'Escape') {
        app.groupCreatingUnder = null;
        void app.renderGroupsPage();
      }
    });
    input.addEventListener('blur', function () {
      if (app.groupCreatingUnder !== null) void app.commitInlineCreate(input, parentId);
    });
  }

  app.commitInlineCreate = async function commitInlineCreate(input, parentId) {
    const name = input.value.trim();
    app.groupCreatingUnder = null;
    if (name) {
      try {
        const res = await app.fetch('/api/v1/groups', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({
            name: name,
            parentId: parentId
          })
        });
        const d = await res.json();
        if (d.ok) {
          app.toast(parentId ? '子分组已创建' : '分组已创建');
          if (parentId) app.GROUP_EXPAND.add(parentId);
        } else {
          app.toast(d.error || '创建失败', 'err');
        }
      } catch (_) {
        app.toast('创建失败', 'err');
      }
    }
    await app.refreshGroupViews(true);
  };

  // 删除为破坏性操作：第一次点击进入确认态，3 秒内再次点击才执行。
  app.armGroupDelete = function armGroupDelete(btn, id) {
    if (btn.dataset.armed === '1') {
      btn.dataset.armed = '';
      void app.deleteGroup(id);
      return;
    }
    btn.dataset.armed = '1';
    btn.dataset.icon = btn.innerHTML;
    btn.classList.add('armed');
    btn.textContent = '确认删除';
    setTimeout(function () {
      if (btn.dataset.armed === '1') {
        btn.dataset.armed = '';
        btn.classList.remove('armed');
        btn.innerHTML = btn.dataset.icon;
      }
    }, 3000);
  };

  app.deleteGroup = async function deleteGroup(id) {
    try {
      const res = await app.fetch('/api/v1/groups?id=' + id, {
        method: 'DELETE'
      });
      const d = await res.json();
      if (d.ok) {
        app.toast('分组已删除');
      } else {
        app.toast('删除失败', 'err');
      }
    } catch (_) {
      app.toast('删除失败', 'err');
    }
    await app.refreshGroupViews(true);
  };

  const pageTree = document.getElementById('groupsPageTree');
  if (pageTree) {
    document.getElementById('groupsPageAddRoot')?.addEventListener('click', function () {
      app.groupCreatingUnder = 0;
      void app.renderGroupsPage();
    });
    void app.renderGroupsPage();
  }
}
