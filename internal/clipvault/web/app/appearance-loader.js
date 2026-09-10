import { app } from './app-context.js';

export function registerAppearanceLoader() {
  app.loadConfigAppearance = async function loadConfigAppearance() {
    const request = ++app.configRequestSequence;
    const revision = app.configRevision;
    try {
      const response = await app.fetch('/api/v1/config');
      if (!response.ok) throw new Error('config load failed');
      const cfg = await response.json();
      if (request !== app.configRequestSequence || revision !== app.configRevision) return;
      app.replaceCurrentConfig(cfg, response.headers.get('X-ClipVault-Config-Version'));
      app.sourceAliases = cfg.source.exeAliases;
      app.sourceWhitelist = cfg.source.exeWhitelist?.length ? cfg.source.exeWhitelist.slice() : null;
      app.applyTheme({ appearance: cfg.appearance, navigation: cfg.navigation });
      app.applyHistoryPageLayout?.(cfg.historyPage);
      app.applyBrand(cfg.brand);
      app.applyBackground(cfg.background);
    } catch (_) {
      if (request !== app.configRequestSequence || revision !== app.configRevision) return;
      app.applyTheme();
      app.applyBrand();
      app.applyBackground();
    }
  };
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (app.currentCfg?.appearance.mode === 'system') {
      app.applyTheme({ appearance: app.currentCfg.appearance, navigation: app.currentCfg.navigation });
    }
  });
}
