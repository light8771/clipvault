import { app } from "./app-context.js";

const t = window.clipvaultI18n.t;

// 清理历史弹窗（设置页「清理」区块）：精确范围清理（preview/delete 与后端共用条件）。
// 全部 / 时间范围（秒级）两种模式，可选保留收藏。
export function registerCleanupModal() {
  app.CLEANUP_MODAL = document.getElementById('cleanupModal');
  app.CLEANUP_PREVIEW = document.getElementById('cleanupPreview');
  app.CLEANUP_CONFIRM = document.getElementById('cleanupConfirmBtn');
  app.cleanupMatched = 0;
  app.cleanupPayload = function cleanupPayload() {
    const mode = document.querySelector('input[name="cleanupMode"]:checked')?.value || 'all';
    const body = {
      mode,
      preserveStarred: document.getElementById('cleanupPreserveStarred').checked
    };
    if (mode === 'range') {
      const from = document.getElementById('cleanupFrom').value;
      const to = document.getElementById('cleanupTo').value;
      if (!from || !to) return null;
      body.from = new Date(from).toISOString();
      body.to = new Date(to).toISOString();
    }
    return body;
  };
  app.refreshCleanupPreview = async function refreshCleanupPreview() {
    const body = app.cleanupPayload();
    if (!body) {
      app.cleanupMatched = 0;
      app.CLEANUP_PREVIEW.textContent = t('cleanup.select_range');
      app.CLEANUP_CONFIRM.disabled = true;
      return;
    }
    app.CLEANUP_CONFIRM.disabled = true;
    app.CLEANUP_PREVIEW.textContent = t('common.loading');
    try {
      const res = await app.protectedFetch('/api/v1/items/cleanup-preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      const d = await res.json();
      if (!res.ok) throw new Error(d.error || t('cleanup.preview_failed'));
      app.cleanupMatched = d.matched || 0;
      app.CLEANUP_PREVIEW.textContent = body.preserveStarred
        ? t('cleanup.preview_count_keep_starred', { count: app.cleanupMatched })
        : t('cleanup.preview_count', { count: app.cleanupMatched });
      app.CLEANUP_CONFIRM.disabled = app.cleanupMatched === 0;
    } catch (e) {
      if (e && (e.code === 'secondary_required' || e.code === 'secondary_cancelled')) {
        app.CLEANUP_PREVIEW.textContent = t('security.secondary_required');
      } else {
        app.CLEANUP_PREVIEW.textContent = e.message || t('cleanup.preview_failed');
      }
    }
  };
  app.openCleanupModal = function openCleanupModal() {
    app.CLEANUP_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
    app.refreshCleanupPreview();
  };
  app.closeCleanupModal = function closeCleanupModal() {
    app.CLEANUP_MODAL.classList.add('hidden');
    document.body.style.overflow = '';
  };
  app.doCleanup = async function doCleanup() {
    const body = app.cleanupPayload();
    if (!body) return;
    app.CLEANUP_CONFIRM.disabled = true;
    app.CLEANUP_CONFIRM.textContent = t('cleanup.deleting');
    try {
      const res = await app.protectedFetch('/api/v1/items/cleanup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      const d = await res.json();
      if (!res.ok || !d.ok) throw new Error(d.error || t('cleanup.failed'));
      app.toast(t('cleanup.deleted', { count: d.deleted }));
      app.closeCleanupModal();
      if (typeof app.search === 'function') {
        app.currentPage = 0;
        await app.search();
      }
    } catch (e) {
      if (e && (e.code === 'secondary_required' || e.code === 'secondary_cancelled')) {
        app.toast(t('cleanup.secondary_required'), 'err');
      } else {
        app.toast(e.message || t('cleanup.failed'), 'err');
      }
    } finally {
      app.CLEANUP_CONFIRM.textContent = t('common.delete');
      app.CLEANUP_CONFIRM.disabled = false;
    }
  };
  document.querySelectorAll('input[name="cleanupMode"]').forEach(radio => {
    radio.addEventListener('change', () => {
      const range = radio.value === 'range' && radio.checked;
      document.getElementById('cleanupRangeFields').classList.toggle('hidden', !range);
      app.refreshCleanupPreview();
    });
  });
  document.getElementById('cleanupFrom').addEventListener('change', () => app.refreshCleanupPreview());
  document.getElementById('cleanupTo').addEventListener('change', () => app.refreshCleanupPreview());
  document.getElementById('cleanupPreserveStarred').addEventListener('change', () => app.refreshCleanupPreview());
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && app.CLEANUP_MODAL && !app.CLEANUP_MODAL.classList.contains('hidden')) {
      app.closeCleanupModal();
    }
  });
}
