import { app } from "./app-context.js";
export function registerHistoryLifecycle() {
  app.setupSrcExeUI();
  app.SCROLL_TOP_BTN = document.getElementById('scrollTopBtn');
  app.scrollTicking = false;
  window.addEventListener('scroll', () => {
    if (app.scrollTicking) return;
    app.scrollTicking = true;
    requestAnimationFrame(() => {
      app.SCROLL_TOP_BTN.classList.toggle('visible', window.scrollY > 300);
      app.scrollTicking = false;
    });
  }, {
    passive: true
  });
  app.SCROLL_TOP_BTN.addEventListener('click', () => {
    window.scrollTo({
      top: 0,
      behavior: app.scrollBehavior()
    });
  });
  app.init = async function init() {
    await app.loadConfigAppearance();
    await app.loadLicenseStatus();
    const recordType = app.currentKind === '__events__' ? 'event' : 'content';
    const loaded = await app.loadSourceExesForHistory(recordType, { refresh: true });
    if (!loaded) return;
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
        try {
          history.replaceState(null, '', window.location.pathname);
        } catch (_) {}
        app.doLocate(id);
        app.loadGroups();
        return;
      }
    }
    if (qParam !== null) {
      app.Q.value = qParam;
      try {
        history.replaceState(null, '', window.location.pathname);
      } catch (_) {}
    }
    app.search();
    app.loadGroups();
  };
  // ── Boot ─────────────────────────────────────────────────
  (async function boot() {
    await app.loadPublicAppearance();
    const ok = await app.checkAuthStatus();
    if (ok) {
      app.init();
    }

    // 标签页切回前台时，刷新认证状态和数据
    document.addEventListener('visibilitychange', async () => {
      if (document.visibilityState !== 'visible') return;
      const wasSecondaryAuthed = app.authState.secondaryAuthed;
      if (!await app.checkAuthStatus()) return;
      await app.loadLicenseStatus();
      // 如果二次认证缓存已过期，重新搜索以遮蔽敏感内容
      if (wasSecondaryAuthed && !app.authState.secondaryAuthed) {
        app.resetSubtypeFilter?.();
        const kind = app.currentKind;
        const recordType = kind === '__events__' ? 'event' : 'content';
        const loaded = await app.loadSourceExesForHistory(recordType);
        if (loaded && app.currentKind === kind) app.search();
      }
    });
  })();
}
