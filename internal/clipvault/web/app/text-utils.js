import { app } from "./app-context.js";
export function registerTextUtils() {
  app.esc = function esc(v) {
    return String(v).replace(/[&<>"']/g, c => ({
      '&': '&amp;',
      '<': '&lt;',
      '>': '&gt;',
      '"': '&quot;',
      "'": '&#39;'
    })[c]);
  };
  app.highlight = function highlight(text, q) {
    const e = app.esc(text);
    if (!q.trim()) return e;
    const p = app.esc(q).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    try {
      return e.replace(new RegExp(p, 'gi'), m => '<mark>' + m + '</mark>');
    } catch (_) {
      return e;
    }
  };
  // 文本预览按「内容预览行数」配置裁剪（app.previewLines 由 applyTheme 写入，0 为不限制）。
  // 裁剪模式下先一律按可溢出渲染，渲染后由 releaseFittingPreviews 实测放开未溢出的卡片。
  app.textPreviewBody = function textPreviewBody(it, q) {
    const previewLines = Number.isFinite(app.previewLines) ? app.previewLines : 2;
    if (previewLines > 0) {
      return { html: `<pre data-clamp="1">${app.highlight(it.text, q)}</pre>`, showView: true };
    }
    const long = it.text.length > 500;
    return { html: `<pre${long ? '' : ' class="open"'}>${app.highlight(it.text, q)}</pre>`, showView: long };
  };
  // 渲染后实测：未溢出的文本预览去掉裁剪渐隐，并移除多余的「查看」按钮。
  app.releaseFittingPreviews = function releaseFittingPreviews() {
    app.LIST.querySelectorAll('pre[data-clamp="1"]').forEach(pre => {
      if (pre.scrollHeight > pre.clientHeight + 2) return;
      pre.classList.add('open');
      pre.removeAttribute('data-clamp');
      const btn = pre.closest('article')?.querySelector('[data-action="view-item"]');
      if (btn) btn.remove();
    });
  };
}
