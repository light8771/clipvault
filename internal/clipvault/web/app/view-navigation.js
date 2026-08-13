import { app } from "./app-context.js";
export function registerViewNavigation() {
  // ── Settings sidebar nav ───────────────────────────────────
  app.snavTo = function (sectionId, navId) {
    document.querySelectorAll('.snav-item').forEach(el => el.classList.remove('active'));
    const navEl = document.getElementById(navId);
    if (navEl) navEl.classList.add('active');
    const sec = document.getElementById(sectionId);
    if (sec) sec.scrollIntoView({
      behavior: 'smooth',
      block: 'start'
    });
  };

  // ── Settings ──────────────────────────────────────────────
}
