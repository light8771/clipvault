import { app } from './app-context.js';

export function registerSettingsLifecycle() {
  app.SCROLL_TOP_BTN = document.getElementById('scrollTopBtn');
  let scrollTicking = false;
  window.addEventListener('scroll', () => {
    if (scrollTicking) return;
    scrollTicking = true;
    requestAnimationFrame(() => {
      app.SCROLL_TOP_BTN.classList.toggle('visible', window.scrollY > 300);
      scrollTicking = false;
    });
  }, { passive: true });
  app.SCROLL_TOP_BTN.addEventListener('click', () => window.scrollTo({ top: 0, behavior: app.scrollBehavior() }));

  app.init = async function initSettings() {
    app.setupAutoSave();
    await app.loadConfigAppearance();
    // 展示安全态势的页面（安全/存储/备份/服务/授权）要求二次验证；
    // 其余设置页只需登录，避免在分组页之间跳转时反复验证。
    const view = document.getElementById('viewSettings');
    if (view?.dataset.requireSecondary === '1') {
      const secondaryOK = await app.requestSecondaryAuth();
      if (!secondaryOK) {
        window.location.replace('/');
        return;
      }
    }
    await Promise.all([app.loadSettings(), app.loadLicenseStatus(), app.loadGroups()]);
    if (app.loadSystemInfo) app.loadSystemInfo();
  };

  (async function bootSettings() {
    await app.loadPublicAppearance();
    if (await app.checkAuthStatus()) await app.init();

    document.addEventListener('visibilitychange', async () => {
      if (document.visibilityState !== 'visible') return;
      await app.checkAuthStatus();
      await app.loadLicenseStatus();
    });
  })();
}
