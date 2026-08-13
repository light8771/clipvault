import { app } from "./app-context.js";
export function registerAppearanceForm() {
  app.themeFromForm = function themeFromForm() {
    const selected = document.querySelector('.theme-preset.active');
    return {
      preset: selected ? selected.dataset.themePreset : app.currentCfg?.theme?.preset || 'mint',
      mode: document.getElementById('cfg-themeMode')?.value || 'system',
      primary: document.getElementById('cfg-themePrimary')?.value || '#14B8A6',
      accent: document.getElementById('cfg-themeAccent')?.value || '#F97316',
      fontScale: Number(document.getElementById('cfg-themeFontScale')?.value) || 100,
      surfaceOpacity: Number(document.getElementById('cfg-themeSurfaceOpacity')?.value)
    };
  };
  app.applyThemeFormState = function applyThemeFormState(theme) {
    const value = theme || {
      preset: 'mint',
      mode: 'system'
    };
    document.querySelectorAll('.theme-preset').forEach(button => {
      const selected = button.dataset.themePreset === value.preset;
      button.classList.toggle('active', selected);
      button.setAttribute('aria-checked', selected ? 'true' : 'false');
    });
    const mode = document.getElementById('cfg-themeMode');
    if (mode) mode.value = value.mode || 'system';
    const primary = document.getElementById('cfg-themePrimary');
    const accent = document.getElementById('cfg-themeAccent');
    if (primary) primary.value = app.validHex(value.primary, '#14B8A6');
    if (accent) accent.value = app.validHex(value.accent, '#F97316');
    const fontScale = document.getElementById('cfg-themeFontScale');
    const fontScaleOutput = document.getElementById('cfg-themeFontScaleOutput');
    if (fontScale) fontScale.value = Math.max(100, Math.min(150, Number(value.fontScale) || 100));
    if (fontScaleOutput) fontScaleOutput.value = `${fontScale?.value || 100}%`;
    const surfaceOpacityEl = document.getElementById('cfg-themeSurfaceOpacity');
    const surfaceOpacityOutput = document.getElementById('cfg-themeSurfaceOpacityOutput');
    const surfaceOpacityRaw = Number(value.surfaceOpacity);
    const surfaceOpacityValue = Number.isFinite(surfaceOpacityRaw) ? Math.max(0, Math.min(100, surfaceOpacityRaw)) : 100;
    if (surfaceOpacityEl) surfaceOpacityEl.value = surfaceOpacityValue;
    if (surfaceOpacityOutput) surfaceOpacityOutput.value = `${surfaceOpacityValue}%`;
    document.getElementById('customThemeColors')?.classList.toggle('hidden', value.preset !== 'custom');
    app.applyTheme(value.preset === 'custom' ? app.themeFromForm() : {
      preset: value.preset || 'mint',
      mode: value.mode || 'system',
      fontScale: Number(value.fontScale) || 100,
      surfaceOpacity: surfaceOpacityValue
    });
  };
  app.loadTheme = async function loadTheme() {
    const request = ++app.configRequestSequence;
    const revision = app.configRevision;
    try {
      const cfg = await app.fetch('/api/v1/config').then(r => r.json());
      if (request !== app.configRequestSequence || revision !== app.configRevision) return;
      app.replaceCurrentConfig(cfg);
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
  if (window.matchMedia) {
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
      if (app.currentCfg?.theme?.mode === 'system') app.applyTheme(app.currentCfg.theme);
    });
  }
}
