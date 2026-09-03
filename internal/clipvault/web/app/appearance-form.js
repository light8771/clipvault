// ── 配置表单 ↔ 作用域模型转换 ─────────────────────────────────
// 配置按作用域拆分后，表单构建也按域组织：
//   appearanceFromForm / applyAppearanceFormState → 全局视觉（外观页）
//   navigationFromForm / applyNavigationFormState → 导航栏（外观页）
//   historyPageFromForm / applyHistoryPageFormState → 历史页布局（历史页面页）
// 页面缺少对应控件时回退 currentCfg 中的值，保证全量 PUT /api/v1/config
// 不丢字段、也不会把未展示的设置重置为默认。
import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

function readInt(id, { min = -Infinity, max = Infinity, fallback } = {}) {
  const raw = Number(document.getElementById(id)?.value);
  if (!Number.isFinite(raw)) return fallback;
  return Math.max(min, Math.min(max, Math.round(raw)));
}

function readBool(id, fallback) {
  const el = document.getElementById(id);
  if (!el) return fallback;
  return el.type === 'checkbox' ? el.checked !== false : fallback;
}

// ── 全局外观 ──────────────────────────────────────────────────
export function registerAppearanceForm() {
  app.appearanceFromForm = function appearanceFromForm(current) {
    const currentAppearance = current || app.currentCfg?.appearance || {};
    // 非外观页没有主题控件：保留当前配置，避免保存时重置主题
    if (!document.getElementById('cfg-themeMode')) {
      return { ...currentAppearance };
    }
    const selected = document.querySelector('[data-theme-preset].active');
    return {
      preset: selected ? selected.dataset.themePreset : currentAppearance.preset || 'mint',
      mode: document.getElementById('cfg-themeMode')?.value || currentAppearance.mode || 'system',
      primary: document.getElementById('cfg-themePrimary')?.value || currentAppearance.primary || '#14B8A6',
      accent: document.getElementById('cfg-themeAccent')?.value || currentAppearance.accent || '#F97316',
      fontScale: readInt('cfg-themeFontScale', { min: 100, max: 150, fallback: currentAppearance.fontScale ?? 100 }),
      surfaceOpacity: readInt('cfg-themeSurfaceOpacity', { min: 0, max: 100, fallback: currentAppearance.surfaceOpacity ?? 100 })
    };
  };
  app.applyAppearanceFormState = function applyAppearanceFormState(cfg) {
    const value = cfg?.appearance || { preset: 'mint', mode: 'system' };
    document.querySelectorAll('[data-theme-preset]').forEach(button => {
      const selected = button.dataset.themePreset === value.preset;
      button.classList.toggle('active', selected);
      button.setAttribute('aria-checked', selected ? 'true' : 'false');
    });
    const modeEl = document.getElementById('cfg-themeMode');
    if (modeEl) modeEl.value = value.mode || 'system';
    const primaryEl = document.getElementById('cfg-themePrimary');
    const accentEl = document.getElementById('cfg-themeAccent');
    if (primaryEl) primaryEl.value = app.validHex(value.primary, '#14B8A6');
    if (accentEl) accentEl.value = app.validHex(value.accent, '#F97316');
    document.getElementById('customThemeColors')?.classList.toggle('hidden', value.preset !== 'custom');
    const fontScaleEl = document.getElementById('cfg-themeFontScale');
    if (fontScaleEl) {
      fontScaleEl.value = Math.max(100, Math.min(150, Number(value.fontScale) || 100));
      const output = document.getElementById('cfg-themeFontScaleOutput');
      if (output) output.value = `${fontScaleEl.value || 100}%`;
    }
    const surfaceOpacityEl = document.getElementById('cfg-themeSurfaceOpacity');
    const surfaceOpacityRaw = Number(value.surfaceOpacity);
    const surfaceOpacityValue = Number.isFinite(surfaceOpacityRaw) ? Math.max(0, Math.min(100, surfaceOpacityRaw)) : 100;
    if (surfaceOpacityEl) surfaceOpacityEl.value = surfaceOpacityValue;
    const surfaceOpacityOutput = document.getElementById('cfg-themeSurfaceOpacityOutput');
    if (surfaceOpacityOutput) surfaceOpacityOutput.value = `${surfaceOpacityValue}%`;
  };

  // ── 导航栏 ─────────────────────────────────────────────────
  app.navigationFromForm = function navigationFromForm(current) {
    const currentNavigation = current || app.currentCfg?.navigation || {};
    // 不含导航栏控件的页面保留当前配置
    if (!document.getElementById('cfg-navSticky')) {
      return { ...currentNavigation };
    }
    const navFollow = document.getElementById('cfg-navOpacityFollow');
    const opacityRaw = Number(document.getElementById('cfg-navOpacity')?.value);
    return {
      sticky: readBool('cfg-navSticky', currentNavigation.sticky ?? true),
      blur: readInt('cfg-navBlur', { min: 0, max: 32, fallback: currentNavigation.blur ?? 16 }),
      opacity: navFollow && !navFollow.checked
        ? (Number.isFinite(opacityRaw) ? Math.max(0, Math.min(100, Math.round(opacityRaw))) : 100)
        : -1,
      divider: readBool('cfg-navDivider', currentNavigation.divider ?? true)
    };
  };
  app.applyNavigationFormState = function applyNavigationFormState(cfg) {
    const value = cfg?.navigation || {};
    const stickyEl = document.getElementById('cfg-navSticky');
    if (stickyEl) stickyEl.checked = value.sticky !== false;
    const blurEl = document.getElementById('cfg-navBlur');
    const blurOutput = document.getElementById('cfg-navBlurOutput');
    const blurValue = Number.isFinite(Number(value.blur)) ? Math.max(0, Math.min(32, Number(value.blur))) : 16;
    if (blurEl) blurEl.value = blurValue;
    if (blurOutput) blurOutput.value = `${blurValue}px`;
    const opacityEl = document.getElementById('cfg-navOpacity');
    const opacityOutput = document.getElementById('cfg-navOpacityOutput');
    const followEl = document.getElementById('cfg-navOpacityFollow');
    const opacityRaw = Number(value.opacity);
    const following = !Number.isFinite(opacityRaw) || opacityRaw < 0;
    if (followEl) followEl.checked = following;
    if (opacityEl) {
      opacityEl.value = following ? 100 : Math.min(100, Math.max(0, opacityRaw));
      opacityEl.disabled = following;
    }
    if (opacityOutput) {
      opacityOutput.value = following ? t('navigation.follow') : `${opacityEl?.value ?? 100}%`;
    }
    const dividerEl = document.getElementById('cfg-navDivider');
    if (dividerEl) dividerEl.checked = value.divider !== false;
  };

  // ── 历史页面 ───────────────────────────────────────────────
  // 预览行数滑块输出：0 显示「不限制」，N 显示「N 行」。
  app.updatePreviewLinesOutput = function updatePreviewLinesOutput() {
    const control = document.getElementById('cfg-previewLines');
    const output = document.getElementById('cfg-previewLinesOutput');
    if (!control || !output) return;
    const lines = Math.round(Number(control.value));
    output.value = lines === 0
      ? t('history_page.preview_lines_unlimited')
      : `${lines} ${t('history_page.preview_lines_unit')}`;
  };
  // 固定占位开关的可用状态：不限制行数（0）时禁用并取消勾选
  //（与「强制固定列数」在自动列数下的行为一致）。
  app.syncFixedPreviewState = function syncFixedPreviewState() {
    const control = document.getElementById('cfg-previewLines');
    const toggle = document.getElementById('cfg-historyFixedPreview');
    if (!control || !toggle) return;
    const unlimited = Math.round(Number(control.value)) === 0;
    toggle.disabled = unlimited;
    if (unlimited) toggle.checked = false;
  };
  app.historyPageFromForm = function historyPageFromForm(current) {
    const currentHistory = current || app.currentCfg?.historyPage || {};
    // 非历史页面设置页保留当前配置
    if (!document.getElementById('cfg-historyColumns')) {
      return { ...currentHistory };
    }
    const columnsRaw = Number(document.getElementById('cfg-historyColumns')?.value);
    const columns = Number.isFinite(columnsRaw) ? Math.max(0, Math.min(4, Math.round(columnsRaw))) : currentHistory.columns ?? 0;
    const selectedLayout = document.querySelector('[data-history-layout-option].active');
    const selectedWidth = document.querySelector('[data-history-width-option].active');
    const previewLines = readInt('cfg-previewLines', { min: 0, max: 20, fallback: currentHistory.previewLines ?? 3 });
    return {
      layout: selectedLayout ? selectedLayout.dataset.historyLayoutOption : currentHistory.layout || 'top-bottom',
      columns,
      forceColumns: columns > 0 && readBool('cfg-historyForceColumns', currentHistory.forceColumns === true),
      contentWidthMode: selectedWidth ? selectedWidth.dataset.historyWidthOption : currentHistory.contentWidthMode || 'auto',
      contentWidth: readInt('cfg-historyContentWidth', { min: 720, max: 3200, fallback: currentHistory.contentWidth ?? 1600 }),
      horizontalPadding: readInt('cfg-historyHorizontalPadding', { min: 0, max: 80, fallback: currentHistory.horizontalPadding ?? 24 }),
      cardGap: readInt('cfg-historyCardGap', { min: 0, max: 32, fallback: currentHistory.cardGap ?? 12 }),
      previewLines,
      fixedPreview: previewLines > 0 && readBool('cfg-historyFixedPreview', currentHistory.fixedPreview === true)
    };
  };
  app.applyHistoryPageFormState = function applyHistoryPageFormState(cfg) {
    const value = cfg?.historyPage || {};
    document.querySelectorAll('[data-history-layout-option]').forEach(button => {
      const selected = button.dataset.historyLayoutOption === (value.layout === 'left-right' ? 'left-right' : 'top-bottom');
      button.classList.toggle('active', selected);
      button.setAttribute('aria-checked', selected ? 'true' : 'false');
    });
    document.querySelectorAll('[data-history-width-option]').forEach(button => {
      const selected = button.dataset.historyWidthOption === (value.contentWidthMode === 'custom' ? 'custom' : 'auto');
      button.classList.toggle('active', selected);
      button.setAttribute('aria-checked', selected ? 'true' : 'false');
    });
    const columnsRaw = Number(value.columns);
    const columns = Number.isFinite(columnsRaw) ? Math.max(0, Math.min(4, Math.round(columnsRaw))) : 0;
    const columnsEl = document.getElementById('cfg-historyColumns');
    if (columnsEl) columnsEl.value = String(columns);
    const forceEl = document.getElementById('cfg-historyForceColumns');
    if (forceEl) {
      forceEl.checked = columns > 0 && value.forceColumns === true;
      forceEl.disabled = columns === 0;
    }
    const contentWidthEl = document.getElementById('cfg-historyContentWidth');
    if (contentWidthEl) {
      const widthRaw = Number(value.contentWidth);
      contentWidthEl.value = Number.isFinite(widthRaw) ? Math.max(720, Math.min(3200, Math.round(widthRaw))) : 1600;
      contentWidthEl.disabled = value.contentWidthMode !== 'custom';
    }
    const paddingEl = document.getElementById('cfg-historyHorizontalPadding');
    if (paddingEl) {
      const paddingRaw = Number(value.horizontalPadding);
      paddingEl.value = Number.isFinite(paddingRaw) ? Math.max(0, Math.min(80, Math.round(paddingRaw))) : 24;
      const output = document.getElementById('cfg-historyHorizontalPaddingOutput');
      if (output) output.value = `${paddingEl.value}px`;
    }
    const gapEl = document.getElementById('cfg-historyCardGap');
    if (gapEl) {
      const gapRaw = Number(value.cardGap);
      gapEl.value = Number.isFinite(gapRaw) ? Math.max(0, Math.min(32, Math.round(gapRaw))) : 12;
      const output = document.getElementById('cfg-historyCardGapOutput');
      if (output) output.value = `${gapEl.value}px`;
    }
    const previewLinesEl = document.getElementById('cfg-previewLines');
    if (previewLinesEl) {
      const previewRaw = Number(value.previewLines);
      previewLinesEl.value = Number.isFinite(previewRaw) ? Math.max(0, Math.min(20, Math.round(previewRaw))) : 3;
      app.updatePreviewLinesOutput();
    }
    const fixedPreviewEl = document.getElementById('cfg-historyFixedPreview');
    if (fixedPreviewEl) {
      const previewLines = Math.round(Number(previewLinesEl?.value));
      fixedPreviewEl.checked = previewLines > 0 && value.fixedPreview === true;
    }
    app.syncFixedPreviewState();
  };
}
