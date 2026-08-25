import { app } from "./app-context.js";
export function registerAppearanceTheme() {
  app.currentCfg = null;
  app.configRevision = 0;
  app.configRequestSequence = 0;
  app.replaceCurrentConfig = function replaceCurrentConfig(config) {
    app.configRevision++;
    app.currentCfg = config;
  };
  app.THEME_PALETTES = {
    mint: {
      light: {
        p: '#14B8A6',
        p2: '#0D9488',
        accent: '#F97316',
        bg: '#F0FDFB',
        bg2: '#E6F9F5',
        card: '#FFFFFF',
        border: 'rgba(20,184,166,.18)',
        tx: '#0F2825',
        sub: '#3A6863',
        muted: '#5C8C87'
      },
      dark: {
        p: '#2DD4BF',
        p2: '#14B8A6',
        accent: '#FB923C',
        bg: '#070C14',
        bg2: '#0C1520',
        card: '#101820',
        border: 'rgba(45,212,191,.14)',
        tx: '#E8F6F3',
        sub: '#9AD3CC',
        muted: '#70AAA4'
      }
    },
    ocean: {
      light: {
        p: '#0284C7',
        p2: '#0369A1',
        accent: '#2563EB',
        bg: '#F0F9FF',
        bg2: '#E0F2FE',
        card: '#FFFFFF',
        border: 'rgba(2,132,199,.18)',
        tx: '#102A43',
        sub: '#3D5A75',
        muted: '#6B849C'
      },
      dark: {
        p: '#38BDF8',
        p2: '#0EA5E9',
        accent: '#60A5FA',
        bg: '#07111F',
        bg2: '#0B1B30',
        card: '#102038',
        border: 'rgba(56,189,248,.16)',
        tx: '#E8F4FF',
        sub: '#A7C7E7',
        muted: '#7FA5CB'
      }
    },
    sunrise: {
      light: {
        p: '#EA580C',
        p2: '#C2410C',
        accent: '#D97706',
        bg: '#FFF7ED',
        bg2: '#FFEDD5',
        card: '#FFFFFF',
        border: 'rgba(234,88,12,.18)',
        tx: '#342012',
        sub: '#72513B',
        muted: '#99735B'
      },
      dark: {
        p: '#FB923C',
        p2: '#F97316',
        accent: '#FBBF24',
        bg: '#16100B',
        bg2: '#24170E',
        card: '#2B1C10',
        border: 'rgba(251,146,60,.16)',
        tx: '#FFF2E5',
        sub: '#E6B58A',
        muted: '#B98D6B'
      }
    },
    graphite: {
      light: {
        p: '#475569',
        p2: '#334155',
        accent: '#0F766E',
        bg: '#F8FAFC',
        bg2: '#EEF2F6',
        card: '#FFFFFF',
        border: 'rgba(71,85,105,.18)',
        tx: '#1E293B',
        sub: '#475569',
        muted: '#748196'
      },
      dark: {
        p: '#94A3B8',
        p2: '#64748B',
        accent: '#2DD4BF',
        bg: '#0B1018',
        bg2: '#151D28',
        card: '#182230',
        border: 'rgba(148,163,184,.16)',
        tx: '#EDF2F7',
        sub: '#B5C0CF',
        muted: '#8A9AAD'
      }
    }
  };
  app.resolvedThemeMode = function resolvedThemeMode(theme) {
    if (theme && theme.mode === 'light') return 'light';
    if (theme && theme.mode === 'dark') return 'dark';
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  };
  app.readablePalette = function readablePalette(palette, mode) {
    const isDark = mode === 'dark';
    const surfaces = [palette.card, palette.bg, palette.bg2];
    const primary = app.ensureContrastOnSurfaces(palette.p, surfaces, 4.5, [.12]);
    const primaryHover = app.ensureContrastOnSurfaces(palette.p2, surfaces, 4.5, [.12]);
    const accent = app.ensureContrastOnSurfaces(palette.accent, surfaces, 4.5, [.1, .16]);
    return {
      ...palette,
      p: primary,
      p2: primaryHover,
      accent,
      border: app.hexToRgba(primary, isDark ? .18 : .2)
    };
  };
  app.customPalette = function customPalette(theme, mode) {
    const isDark = mode === 'dark';
    const neutral = app.THEME_PALETTES.graphite[mode];
    const surfaces = [neutral.card, neutral.bg, neutral.bg2];
    const primary = app.ensureContrastOnSurfaces(app.validHex(theme.primary, '#14B8A6'), surfaces, 4.5, [.12]);
    const accent = app.ensureContrastOnSurfaces(app.validHex(theme.accent, '#F97316'), surfaces, 4.5, [.1, .16]);
    // Keep neutral surfaces fixed for each mode. A user-provided background
    // cannot guarantee text contrast, so custom skins only alter brand colors.
    return {
      ...neutral,
      p: primary,
      p2: app.ensureContrastOnSurfaces(app.blendHex(primary, isDark ? '#FFFFFF' : '#000000', .12), surfaces, 4.5, [.12]),
      accent,
      border: app.hexToRgba(primary, isDark ? .18 : .2)
    };
  };
  app.applyTheme = function applyTheme(theme) {
    const normalized = theme || {
      preset: 'mint',
      mode: 'system'
    };
    const mode = app.resolvedThemeMode(normalized);
    const preset = app.THEME_PALETTES[normalized.preset];
    const palette = normalized.preset === 'custom' ? app.customPalette(normalized, mode) : (preset || app.THEME_PALETTES.mint)[mode];
    const colors = normalized.preset === 'custom' ? palette : app.readablePalette(palette, mode);
    const root = document.documentElement;
    root.dataset.themeMode = mode;
    root.style.setProperty('--p', colors.p);
    root.style.setProperty('--p2', colors.p2);
    root.style.setProperty('--on-p', app.textColorFor(colors.p));
    root.style.setProperty('--on-p2', app.textColorFor(colors.p2));
    root.style.setProperty('--accent', colors.accent);
    root.style.setProperty('--on-accent', app.textColorFor(colors.accent));
    root.style.setProperty('--bg', colors.bg);
    root.style.setProperty('--bg2', colors.bg2);
    root.style.setProperty('--card', colors.card);
    root.style.setProperty('--card-border', colors.border);
    const surfaceOpacity = Number(normalized.surfaceOpacity);
    const surfaceAlpha = (Number.isFinite(surfaceOpacity) ? Math.max(0, Math.min(100, surfaceOpacity)) : 100) / 100;
    root.style.setProperty('--card-rgba', app.hexToRgba(colors.card, surfaceAlpha));
    // 导航栏：不透明度（-1 跟随界面不透明度）、毛玻璃模糊、底部分隔线
    const navOpacity = Number(normalized.navOpacity);
    const navAlpha = Number.isFinite(navOpacity) && navOpacity >= 0 ? Math.min(100, navOpacity) / 100 : surfaceAlpha;
    root.style.setProperty('--nav-bg', app.hexToRgba(colors.bg, navAlpha));
    const navBlur = Number(normalized.navBlur);
    const navBlurPx = Number.isFinite(navBlur) ? Math.max(0, Math.min(32, navBlur)) : 16;
    root.style.setProperty('--nav-backdrop', navBlurPx > 0 ? `blur(${navBlurPx}px) saturate(180%)` : 'none');
    root.style.setProperty('--nav-border-bottom', normalized.navBorder === false ? 'none' : `1px solid ${colors.border}`);
    // 导航栏是否吸附跟随滚动（sticky）还是随页面滚动（static）
    root.style.setProperty('--nav-position', normalized.navSticky === false ? 'static' : 'sticky');
    // 历史卡片文本预览行数（0 表示不限制），供 history.css 的 pre 裁剪与渲染逻辑使用
    const previewLines = Number(normalized.previewLines);
    app.previewLines = Number.isFinite(previewLines) ? Math.max(0, Math.min(20, previewLines)) : 2;
    root.style.setProperty('--preview-max-h', app.previewLines > 0 ? `${app.previewLines * 1.7}em` : 'none');
    // 历史页布局：vertical（默认）/ sidebar（笔记式左右布局）
    if (normalized.layout === 'sidebar') {
      root.dataset.historyLayout = 'sidebar';
    } else {
      delete root.dataset.historyLayout;
    }
    // 历史列表列数：容量计算 + 内容区宽度联动（仅历史页注册，见 history-layout.js）
    app.applyHistoryLayout?.(normalized);
    root.style.setProperty('--tx', colors.tx);
    root.style.setProperty('--sub', colors.sub);
    root.style.setProperty('--muted', colors.muted);
    root.style.setProperty('--p-dim', app.hexToRgba(colors.p, .12));
    root.style.setProperty('--p-glow', app.hexToRgba(colors.p, .18));
    root.style.setProperty('--p-border', app.hexToRgba(colors.p, .22));
    root.style.setProperty('--accent-dim', app.hexToRgba(colors.accent, .1));
    root.style.setProperty('--accent-glow', app.hexToRgba(colors.accent, .16));
    root.style.setProperty('--accent-border', app.hexToRgba(colors.accent, .3));
    root.style.setProperty('--font-scale', `${Math.max(100, Math.min(150, Number(normalized.fontScale) || 100)) / 100}`);
    root.style.setProperty('--shadow-md', `0 4px 16px ${app.hexToRgba(colors.p, .1)}, 0 2px 6px rgba(0,0,0,.05)`);
    root.style.setProperty('--shadow-lg', `0 8px 32px ${app.hexToRgba(colors.p, .15)}, 0 3px 10px rgba(0,0,0,.08)`);
  };
  app.loadPublicAppearance = async function loadPublicAppearance() {
    try {
      const appearance = await app.fetch('/api/v1/appearance', {
        cache: 'no-store'
      }).then(r => r.ok ? r.json() : null);
      if (!appearance) return;
      app.applyTheme(appearance.theme);
      app.applyBrand(appearance.brand);
      app.applyBackground(appearance.background);
    } catch (_) {}
  };
  app.brandAssetURL = function brandAssetURL(kind, version) {
    return Number(version) > 0 ? `/brand/${kind}?v=${encodeURIComponent(version)}` : '';
  };
  app.applyBrand = function applyBrand(brand) {
    const value = brand || {};
    const title = String(value.title || 'ClipVault').trim() || 'ClipVault';
    const pageTitle = document.documentElement.dataset.pageTitle;
    document.title = `${pageTitle} · ${title}`;
    const name = document.getElementById('brandName');
    if (name) name.textContent = title;
    const logoURL = app.brandAssetURL('logo', value.logoVersion);
    const logo = document.getElementById('brandLogo');
    const defaultLogo = document.getElementById('brandDefaultLogo');
    if (logo) {
      if (logoURL) {
        logo.src = logoURL;
        logo.classList.remove('hidden');
        defaultLogo?.classList.add('hidden');
      } else {
        logo.removeAttribute('src');
        logo.classList.add('hidden');
        defaultLogo?.classList.remove('hidden');
      }
    }
    const lockTitle = document.getElementById('lockTitle');
    if (lockTitle) lockTitle.textContent = title;
    const lockLogo = document.getElementById('lockBrandLogo');
    const lockDefaultLogo = document.getElementById('lockDefaultLogo');
    if (lockLogo) {
      if (logoURL) {
        lockLogo.src = logoURL;
        lockLogo.classList.remove('hidden');
        lockDefaultLogo?.classList.add('hidden');
      } else {
        lockLogo.removeAttribute('src');
        lockLogo.classList.add('hidden');
        lockDefaultLogo?.classList.remove('hidden');
      }
    }
    const favicon = document.getElementById('brandFavicon');
    if (favicon) favicon.href = Number(value.faviconVersion) > 0 ? `/favicon.ico?v=${encodeURIComponent(value.faviconVersion)}` : '/favicon.ico';
    const logoPreview = document.getElementById('brandLogoPreview');
    const faviconPreview = document.getElementById('brandFaviconPreview');
    if (logoPreview) {
      if (logoURL) logoPreview.src = logoURL;else logoPreview.removeAttribute('src');
    }
    const faviconURL = Number(value.faviconVersion) > 0 ? `/favicon.ico?v=${encodeURIComponent(value.faviconVersion)}` : '';
    if (faviconPreview) {
      if (faviconURL) faviconPreview.src = faviconURL;else faviconPreview.removeAttribute('src');
    }
  };
}
