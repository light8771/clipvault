import { app } from "./app-context.js";
export function registerAppearanceForm() {
  app.themeFromForm = function themeFromForm() {
    const selected = document.querySelector('.theme-preset.active');
    // 非外观页没有主题控件：保留当前配置，避免保存时重置主题
    if (!selected && !document.getElementById('cfg-themeMode')) {
      return { ...(app.currentCfg?.theme || { preset: 'mint', mode: 'system' }) };
    }
    const current = app.currentCfg?.theme || {};
    const navFollow = document.getElementById('cfg-navOpacityFollow');
    const previewLinesRaw = Number(document.getElementById('cfg-previewLines')?.value);
    const navBlurRaw = Number(document.getElementById('cfg-navBlur')?.value);
    const navOpacityRaw = Number(document.getElementById('cfg-navOpacity')?.value);
    return {
      preset: selected ? selected.dataset.themePreset : app.currentCfg?.theme?.preset || 'mint',
      mode: document.getElementById('cfg-themeMode')?.value || 'system',
      primary: document.getElementById('cfg-themePrimary')?.value || '#14B8A6',
      accent: document.getElementById('cfg-themeAccent')?.value || '#F97316',
      fontScale: Number(document.getElementById('cfg-themeFontScale')?.value) || 100,
      surfaceOpacity: Number(document.getElementById('cfg-themeSurfaceOpacity')?.value),
      layout: document.getElementById('cfg-historyLayout')?.value || current.layout || 'vertical',
      previewLines: Number.isFinite(previewLinesRaw) ? Math.max(0, Math.min(20, previewLinesRaw)) : (current.previewLines ?? 2),
      navOpacity: navFollow && !navFollow.checked
        ? (Number.isFinite(navOpacityRaw) ? Math.max(0, Math.min(100, navOpacityRaw)) : 100)
        : -1,
      navBlur: Number.isFinite(navBlurRaw) ? Math.max(0, Math.min(32, navBlurRaw)) : (current.navBlur ?? 16),
      navBorder: document.getElementById('cfg-navBorder')?.checked !== false,
      navSticky: document.getElementById('cfg-navSticky')?.checked !== false
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
    // 布局与导航栏
    const layoutEl = document.getElementById('cfg-historyLayout');
    if (layoutEl) layoutEl.value = value.layout === 'sidebar' ? 'sidebar' : 'vertical';
    const previewLinesEl = document.getElementById('cfg-previewLines');
    const previewLinesRaw = Number(value.previewLines);
    if (previewLinesEl) previewLinesEl.value = Number.isFinite(previewLinesRaw) ? Math.max(0, Math.min(20, previewLinesRaw)) : 2;
    const navOpacityEl = document.getElementById('cfg-navOpacity');
    const navOpacityOutput = document.getElementById('cfg-navOpacityOutput');
    const navFollowEl = document.getElementById('cfg-navOpacityFollow');
    const navOpacityRaw = Number(value.navOpacity);
    const navFollowing = !Number.isFinite(navOpacityRaw) || navOpacityRaw < 0;
    if (navFollowEl) navFollowEl.checked = navFollowing;
    if (navOpacityEl) {
      navOpacityEl.value = navFollowing ? surfaceOpacityValue : Math.min(100, Math.max(0, navOpacityRaw));
      navOpacityEl.disabled = navFollowing;
    }
    if (navOpacityOutput) navOpacityOutput.value = navFollowing ? '跟随' : `${navOpacityEl?.value ?? 100}%`;
    const navBlurEl = document.getElementById('cfg-navBlur');
    const navBlurOutput = document.getElementById('cfg-navBlurOutput');
    const navBlurRaw = Number(value.navBlur);
    const navBlurValue = Number.isFinite(navBlurRaw) ? Math.max(0, Math.min(32, navBlurRaw)) : 16;
    if (navBlurEl) navBlurEl.value = navBlurValue;
    if (navBlurOutput) navBlurOutput.value = `${navBlurValue}px`;
    const navBorderEl = document.getElementById('cfg-navBorder');
    if (navBorderEl) navBorderEl.checked = value.navBorder !== false;
    const navStickyEl = document.getElementById('cfg-navSticky');
    if (navStickyEl) navStickyEl.checked = value.navSticky !== false;
    document.getElementById('customThemeColors')?.classList.toggle('hidden', value.preset !== 'custom');
    app.applyTheme(value.preset === 'custom' ? app.themeFromForm() : {
      ...value,
      preset: value.preset || 'mint',
      mode: value.mode || 'system',
      fontScale: Number(value.fontScale) || 100,
      surfaceOpacity: surfaceOpacityValue
    });
  };
}
