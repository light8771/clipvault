import { app } from "./app-context.js";
import { createRemovableModal } from './modal-controller.js';

const t = window.clipvaultI18n.t;

export function registerGroups() {
  app.allGroups = [];
  app.GROUP_EXPAND = new Set();
  app.groupModalMode = null;
  app.groupModalOpts = {};
  app.loadGroups = async function loadGroups() {
    try {
      const d = await app.fetch('/api/v1/groups').then(r => r.json());
      app.allGroups = d.groups || [];
    } catch (_) {
      app.allGroups = [];
    }
  };
  app.buildGroupTree = function buildGroupTree(parentID) {
    return app.allGroups.filter(g => g.parentId === parentID).map(g => ({
      ...g,
      children: app.buildGroupTree(g.id)
    }));
  };
  app.flattenGroups = function flattenGroups(nodes, prefix) {
    let result = [];
    for (const n of nodes) {
      const label = prefix ? prefix + ' / ' + n.name : n.name;
      result.push({
        id: n.id,
        label
      });
      if (n.children && n.children.length) {
        result = result.concat(app.flattenGroups(n.children, label));
      }
    }
    return result;
  };
  app.CHEVRON_SVG = '<svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="9 18 15 12 9 6"/></svg>';
  app.FOLDER_SVG = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>';
  app.CHECK_SVG = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><polyline points="20 6 9 17 4 12"/></svg>';
  app.EDIT_SVG = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>';
  app.DEL_SVG = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>';
  app.PLUS_SVG = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>';
  app.openGroupModal = function openGroupModal(mode, opts) {
    opts = opts || {};
    app.closeGroupModal();
    const titles = {
      filter: t('groups.modal_filter'),
      assign: t('groups.modal_assign'),
      manage: t('groups.modal_manage')
    };
    const boxClass = mode === 'manage' ? 'group-modal-box-wide' : 'group-modal-box';
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.id = 'groupModalOverlay';
    overlay.innerHTML = '<div class="modal-box ' + boxClass + '" data-modal-panel>' + '<div class="modal-head">' + '<span class="modal-title">' + app.esc(titles[mode]) + '</span>' + '<button class="modal-close" id="groupModalCloseBtn" type="button" aria-label="' + app.esc(t('groups.close_label')) + '">' + '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>' + '</button>' + '</div>' + '<div class="modal-body group-modal-body" id="groupModalBody"></div>' + '</div>';
    document.body.appendChild(overlay);
    document.body.style.overflow = 'hidden';
    app.groupModalController = createRemovableModal(overlay, {
      initialFocus: () => document.getElementById('groupModalCloseBtn'),
      onClose: () => {
        app.groupModalController = null;
        document.body.style.overflow = '';
        app.groupModalMode = null;
        app.groupModalOpts = {};
      }
    });
    document.getElementById('groupModalCloseBtn').addEventListener('click', app.closeGroupModal);
    app.groupModalMode = mode;
    app.groupModalOpts = opts;
    app.renderGroupModalContent();
  };
  app.closeGroupModal = function closeGroupModal() {
    if (app.groupModalController) {
      app.groupModalController.close();
      return;
    }
    document.getElementById('groupModalOverlay')?.remove();
    document.body.style.overflow = '';
    app.groupModalMode = null;
    app.groupModalOpts = {};
  };
  app.renderGroupModalContent = async function renderGroupModalContent() {
    const body = document.getElementById('groupModalBody');
    if (!body) return;
    const mode = app.groupModalMode;
    const opts = app.groupModalOpts;
    if (app.allGroups.length === 0) {
      await app.loadGroups();
    }

    // For assign mode, load item's current groups
    let itemGroupIds = [];
    if (mode === 'assign' && opts.itemId) {
      try {
        const res = await app.authorizedItemFetch(opts.itemId, '/api/v1/items/groups?item_id=' + opts.itemId);
        if (!res.ok) throw new Error('HTTP ' + res.status);
        const d = await res.json();
        itemGroupIds = d.groupIds || [];
      } catch (_) {
        app.toast(t('groups.read_item_failed'), 'err');
        app.closeGroupModal();
        return;
      }
    }
    let html = '';
    // Toolbar for manage mode
    if (mode === 'manage') {
      html += '<div class="group-modal-toolbar">' + '<button class="btn-add-group" id="gmAddRootBtn">' + app.PLUS_SVG + ' ' + app.esc(t('groups.create')) + '</button>' + '</div>';
    }
    if (app.allGroups.length === 0) {
      const msg = mode === 'manage' ? t('groups.empty_manage') : t('groups.empty_other');
      html += '<div class="group-modal-empty">' + app.esc(msg) + '</div>';
    } else {
      const rootNodes = app.buildGroupTree(0);
      if (mode === 'filter') {
        html += '<div class="group-modal-tree">' + rootNodes.map(function (n) {
          return app.renderFilterNode(n);
        }).join('') + '</div>';
      } else if (mode === 'assign') {
        html += '<div class="group-modal-tree">' + rootNodes.map(function (n) {
          return app.renderAssignNode(n, itemGroupIds);
        }).join('') + '</div>';
      } else if (mode === 'manage') {
        html += '<div class="group-modal-tree">' + rootNodes.map(function (n) {
          return app.renderManageNode(n);
        }).join('') + '</div>';
      }
    }
    body.innerHTML = html;
    app.bindGroupModalEvents(mode, opts, itemGroupIds);
  };
  app.renderFilterNode = function renderFilterNode(node) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = app.GROUP_EXPAND.has(node.id);
    const active = app.currentGroupID === node.id;
    let html = '<div class="gt-node">';
    html += '<div class="gt-row' + (active ? ' active' : '') + '" data-id="' + node.id + '">';
    html += '<span class="gt-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + app.CHEVRON_SVG + '</span>';
    html += '<span class="gt-icon">' + app.FOLDER_SVG + '</span>';
    html += '<span class="gt-label">' + app.esc(node.name) + '</span>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gt-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(app.renderFilterNode).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  };
  app.renderAssignNode = function renderAssignNode(node, itemGroupIds) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = app.GROUP_EXPAND.has(node.id);
    const checked = itemGroupIds.includes(node.id);
    let html = '<div class="gt-node">';
    html += '<div class="gt-row" data-id="' + node.id + '">';
    html += '<span class="gt-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + app.CHEVRON_SVG + '</span>';
    html += '<span class="gt-check' + (checked ? ' checked' : '') + '" data-id="' + node.id + '">' + app.CHECK_SVG + '</span>';
    html += '<span class="gt-icon">' + app.FOLDER_SVG + '</span>';
    html += '<span class="gt-label">' + app.esc(node.name) + '</span>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gt-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(function (n) {
        return app.renderAssignNode(n, itemGroupIds);
      }).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  };
  app.renderManageNode = function renderManageNode(node) {
    const hasChildren = node.children && node.children.length > 0;
    const expanded = app.GROUP_EXPAND.has(node.id);
    let html = '<div class="gm-node">';
    html += '<div class="gm-row">';
    html += '<span class="gm-toggle' + (hasChildren ? '' : ' no-children') + (expanded ? ' expanded' : '') + '" data-id="' + node.id + '">' + app.CHEVRON_SVG + '</span>';
    html += '<span class="gm-icon">' + app.FOLDER_SVG + '</span>';
    html += '<span class="gm-name" data-id="' + node.id + '">' + app.esc(node.name) + '</span>';
    html += '<div class="gm-actions">';
    html += '<button class="gm-act" data-action="add-child" data-id="' + node.id + '" title="' + app.esc(t('groups.add_child')) + '">' + app.PLUS_SVG + '</button>';
    html += '<button class="gm-act" data-action="rename" data-id="' + node.id + '" title="' + app.esc(t('groups.rename')) + '">' + app.EDIT_SVG + '</button>';
    html += '<button class="gm-act danger" data-action="delete" data-id="' + node.id + '" title="' + app.esc(t('common.delete')) + '">' + app.DEL_SVG + '</button>';
    html += '</div>';
    html += '</div>';
    if (hasChildren) {
      html += '<div class="gm-children' + (expanded ? ' expanded' : '') + '">';
      html += node.children.map(app.renderManageNode).join('');
      html += '</div>';
    }
    html += '</div>';
    return html;
  };
  app.activateGroupFilter = async function activateGroupFilter(id) {
    app.currentGroupID = app.currentGroupID === id ? 0 : id;
    const selectedGroupID = app.currentGroupID;
    app.currentKind = '__group__';
    document.querySelectorAll('.filter-chip').forEach(function (chip) {
      chip.classList.remove('active');
    });
    app.GROUP_CHIP.classList.add('active');
    app.closeGroupModal();
    const loaded = await app.loadSourceExesForHistory('content');
    if (!loaded || app.currentKind !== '__group__' || app.currentGroupID !== selectedGroupID) return;
    app.resetSearch();
  };
  app.bindGroupModalEvents = function bindGroupModalEvents(mode, opts, itemGroupIds) {
    const body = document.getElementById('groupModalBody');
    if (!body) return;

    // Toggle expand/collapse for all modes
    body.querySelectorAll('.gt-toggle, .gm-toggle').forEach(function (toggle) {
      toggle.addEventListener('click', function (e) {
        e.stopPropagation();
        const id = parseInt(toggle.dataset.id);
        if (app.GROUP_EXPAND.has(id)) {
          app.GROUP_EXPAND.delete(id);
        } else {
          app.GROUP_EXPAND.add(id);
        }
        app.renderGroupModalContent();
      });
    });
    if (mode === 'filter') {
      // Single-select: clicking a row selects the group
      body.querySelectorAll('.gt-row').forEach(function (row) {
        row.addEventListener('click', async function (e) {
          if (e.target.closest('.gt-toggle')) return;
          await app.activateGroupFilter(parseInt(row.dataset.id));
        });
      });
    } else if (mode === 'assign') {
      // Multi-select: clicking a row or checkbox toggles membership
      body.querySelectorAll('.gt-row').forEach(function (row) {
        row.addEventListener('click', async function (e) {
          if (e.target.closest('.gt-toggle')) return;
          const id = parseInt(row.dataset.id);
          const checkEl = row.querySelector('.gt-check');
          const isChecked = checkEl.classList.contains('checked');
          try {
            if (isChecked) {
              const res = await app.authorizedItemFetch(opts.itemId, '/api/v1/items/group?item_id=' + opts.itemId + '&group_id=' + id, {
                method: 'DELETE'
              });
              if (!res.ok) throw new Error('HTTP ' + res.status);
              checkEl.classList.remove('checked');
              app.toast(t('groups.removed'));
            } else {
              const res = await app.authorizedItemFetch(opts.itemId, '/api/v1/items/group', {
                method: 'POST',
                headers: {
                  'Content-Type': 'application/json'
                },
                body: JSON.stringify({
                  itemId: opts.itemId,
                  groupId: id
                })
              });
              if (!res.ok) throw new Error('HTTP ' + res.status);
              checkEl.classList.add('checked');
              app.toast(t('groups.added'));
            }
          } catch (_) {
            app.toast(t('common.action_failed'), 'err');
          }
        });
      });
    } else if (mode === 'manage') {
      // Add root group
      const addRootBtn = document.getElementById('gmAddRootBtn');
      if (addRootBtn) {
        addRootBtn.addEventListener('click', function () {
          app.showGroupEditDialog('create', 0, '', async function (name) {
            try {
              const res = await app.fetch('/api/v1/groups', {
                method: 'POST',
                headers: {
                  'Content-Type': 'application/json'
                },
                body: JSON.stringify({
                  name: name,
                  parentId: 0
                })
              });
              const d = await res.json();
              if (d.ok) {
                app.toast(t('groups.created'));
                await app.refreshGroupViews(true);
              } else {
                app.toast(d.error || t('groups.create_failed'), 'err');
              }
            } catch (_) {
              app.toast(t('groups.create_failed'), 'err');
            }
          });
        });
      }
      // Action buttons
      body.querySelectorAll('.gm-act[data-action]').forEach(function (btn) {
        btn.addEventListener('click', function (e) {
          e.stopPropagation();
          const action = btn.dataset.action;
          const id = parseInt(btn.dataset.id);
          if (action === 'rename') app.startRenameGroup(id);else if (action === 'delete') app.confirmDeleteGroup(id);else if (action === 'add-child') app.quickAddChild(id);
        });
      });
    }
  };
  app.getParentId = function getParentId(id) {
    const g = app.allGroups.find(function (g) {
      return g.id === id;
    });
    return g ? g.parentId : 0;
  };
  app.quickAddChild = function quickAddChild(parentId) {
    app.showGroupEditDialog('create', parentId, '', async function (name) {
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
          app.toast(t('groups.child_created'));
          app.GROUP_EXPAND.add(parentId);
          await app.refreshGroupViews(true);
        } else {
          app.toast(d.error || t('groups.create_failed'), 'err');
        }
      } catch (_) {
        app.toast(t('groups.create_failed'), 'err');
      }
    });
  };
  app.startRenameGroup = function startRenameGroup(id) {
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
        await app.refreshGroupViews(false);
        return;
      }
      try {
        const res = await app.fetch('/api/v1/groups?id=' + id, {
          method: 'PUT',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({
            name: newName,
            parentId: app.getParentId(id)
          })
        });
        const d = await res.json();
        if (d.ok) {
          app.toast(t('groups.renamed'));
          await app.refreshGroupViews(true);
        } else {
          app.toast(d.error || t('groups.rename_failed'), 'err');
          await app.refreshGroupViews(false);
        }
      } catch (_) {
        app.toast(t('groups.rename_failed'), 'err');
        await app.refreshGroupViews(false);
      }
    }
    input.addEventListener('blur', save);
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        input.blur();
      }
      if (e.key === 'Escape') {
        void app.refreshGroupViews(false);
      }
    });
  };
  app.confirmDeleteGroup = function confirmDeleteGroup(id) {
    const g = app.allGroups.find(function (g) {
      return g.id === id;
    });
    if (!g) return;
    app.showConfirmDialog(t('groups.delete_title'), t('groups.delete_confirm', { name: g.name }), async function () {
      try {
        const res = await app.fetch('/api/v1/groups?id=' + id, {
          method: 'DELETE'
        });
        const d = await res.json();
        if (d.ok) {
          app.toast(t('groups.deleted'));
          await app.refreshGroupViews(true);
        } else {
          app.toast(t('common.delete_failed'), 'err');
        }
      } catch (_) {
        app.toast(t('common.delete_failed'), 'err');
      }
    });
  };
  app.showGroupEditDialog = function showGroupEditDialog(mode, parentId, defaultName, onConfirm) {
    const title = mode === 'create' ? t('groups.create') : t('groups.rename_group');
    const html = '<div class="modal-overlay" id="groupEditOverlay">' + '<div class="modal-box secondary-box" data-modal-panel>' + '<div class="modal-head">' + '<span class="modal-title">' + app.esc(title) + '</span>' + '<button class="modal-close" type="button" aria-label="' + app.esc(t('groups.close_edit_label')) + '" data-action="close-group-edit">' + '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>' + '</button>' + '</div>' + '<div class="modal-body u-inline-16">' + '<form id="groupEditForm" class="lock-form">' + '<input type="text" id="groupEditInput" class="lock-input u-inline-33" placeholder="' + app.esc(t('groups.name_placeholder')) + '" value="' + app.esc(defaultName) + '" autofocus>' + '<button type="submit" class="lock-btn">' + app.esc(t('common.confirm')) + '</button>' + '</form>' + '</div>' + '</div>' + '</div>';
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('groupEditOverlay');
    const input = document.getElementById('groupEditInput');
    const form = document.getElementById('groupEditForm');
    const modal = createRemovableModal(overlay, { initialFocus: input });
    app.closeGroupEditDialog = modal.close;
    requestAnimationFrame(() => input.select());
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      const name = input.value.trim();
      if (!name) return;
      modal.close();
      onConfirm(name);
    });
  };
  app.showConfirmDialog = function showConfirmDialog(title, message, onConfirm) {
    const html = '<div class="modal-overlay" id="confirmDialogOverlay">' + '<div class="modal-box secondary-box" data-modal-panel>' + '<div class="modal-head"><span class="modal-title">' + app.esc(title) + '</span></div>' + '<div class="modal-body u-inline-16">' + '<p class="u-inline-34">' + app.esc(message) + '</p>' + '<div class="u-inline-19">' + '<button class="btn-cancel" data-action="close-confirm">' + app.esc(t('common.cancel')) + '</button>' + '<button class="lock-btn u-inline-35" id="confirmDialogBtn">' + app.esc(t('common.delete')) + '</button>' + '</div>' + '</div>' + '</div>' + '</div>';
    const wrapper = document.createElement('div');
    wrapper.innerHTML = html;
    document.body.appendChild(wrapper.firstElementChild);
    const overlay = document.getElementById('confirmDialogOverlay');
    const confirmButton = document.getElementById('confirmDialogBtn');
    const cancelButton = overlay.querySelector('[data-action="close-confirm"]');
    const modal = createRemovableModal(overlay, { initialFocus: cancelButton });
    app.closeConfirmDialog = modal.close;
    confirmButton.addEventListener('click', function () {
      modal.close();
      onConfirm();
    });
  };
}
