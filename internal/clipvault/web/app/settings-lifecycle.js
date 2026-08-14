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
    const secondaryOK = await app.requestSecondaryAuth();
    if (!secondaryOK) {
      window.location.replace('/');
      return;
    }
    await Promise.all([app.loadSettings(), app.loadLicenseStatus(), app.loadGroups()]);
    const sectionID = window.location.hash.slice(1);
    if (sectionID) {
      requestAnimationFrame(() => app.navigateToSettingsSection(sectionID, false));
    }
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
