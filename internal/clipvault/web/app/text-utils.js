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
}
