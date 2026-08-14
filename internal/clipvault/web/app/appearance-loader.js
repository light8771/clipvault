import { app } from './app-context.js';

export function registerAppearanceLoader() {
  app.loadConfigAppearance = async function loadConfigAppearance() {
    const request = ++app.configRequestSequence;
    const revision = app.configRevision;
    try {
      const cfg = await app.fetch('/api/v1/config').then(response => response.json());
      if (request !== app.configRequestSequence || revision !== app.configRevision) return;
      app.replaceCurrentConfig(cfg);
      app.sourceAliases = cfg.source.exeAliases;
      app.sourceWhitelist = cfg.source.exeWhitelist?.length ? cfg.source.exeWhitelist.slice() : null;
      app.applyTheme(cfg.theme);
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
    if (app.currentCfg?.theme.mode === 'system') app.applyTheme(app.currentCfg.theme);
  });
}
