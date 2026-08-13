    ocean: {
      light: { p: '#0284C7', p2: '#0369A1', accent: '#2563EB', bg: '#F0F9FF', bg2: '#E0F2FE', card: '#FFFFFF', border: 'rgba(2,132,199,.18)', tx: '#102A43', sub: '#3D5A75', muted: '#6B849C' },
      dark:  { p: '#38BDF8', p2: '#0EA5E9', accent: '#60A5FA', bg: '#07111F', bg2: '#0B1B30', card: '#102038', border: 'rgba(56,189,248,.16)', tx: '#E8F4FF', sub: '#A7C7E7', muted: '#7FA5CB' },
    },
    sunrise: {
      light: { p: '#EA580C', p2: '#C2410C', accent: '#D97706', bg: '#FFF7ED', bg2: '#FFEDD5', card: '#FFFFFF', border: 'rgba(234,88,12,.18)', tx: '#342012', sub: '#72513B', muted: '#99735B' },
      dark:  { p: '#FB923C', p2: '#F97316', accent: '#FBBF24', bg: '#16100B', bg2: '#24170E', card: '#2B1C10', border: 'rgba(251,146,60,.16)', tx: '#FFF2E5', sub: '#E6B58A', muted: '#B98D6B' },
    },
    graphite: {
      light: { p: '#475569', p2: '#334155', accent: '#0F766E', bg: '#F8FAFC', bg2: '#EEF2F6', card: '#FFFFFF', border: 'rgba(71,85,105,.18)', tx: '#1E293B', sub: '#475569', muted: '#748196' },
      dark:  { p: '#94A3B8', p2: '#64748B', accent: '#2DD4BF', bg: '#0B1018', bg2: '#151D28', card: '#182230', border: 'rgba(148,163,184,.16)', tx: '#EDF2F7', sub: '#B5C0CF', muted: '#8A9AAD' },
    },
  };

  const { validHex, hexToRgba, blendHex, ensureContrastOnSurfaces, textColorFor } = window.ClipVaultTheme;

  function resolvedThemeMode(theme) {
    if (theme && theme.mode === 'light') return 'light';
    if (theme && theme.mode === 'dark') return 'dark';
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function readablePalette(palette, mode) {
    const isDark = mode === 'dark';
    const surfaces = [palette.card, palette.bg, palette.bg2];
    const primary = ensureContrastOnSurfaces(palette.p, surfaces, 4.5, [.12]);
    const primaryHover = ensureContrastOnSurfaces(palette.p2, surfaces, 4.5, [.12]);
    const accent = ensureContrastOnSurfaces(palette.accent, surfaces, 4.5, [.1, .16]);
    return {
      ...palette,
      p: primary,
      p2: primaryHover,
      accent,
      border: hexToRgba(primary, isDark ? .18 : .2),
    };
  }

  function customPalette(theme, mode) {
    const isDark = mode === 'dark';
    const neutral = THEME_PALETTES.graphite[mode];
    const surfaces = [neutral.card, neutral.bg, neutral.bg2];
    const primary = ensureContrastOnSurfaces(validHex(theme.primary, '#14B8A6'), surfaces, 4.5, [.12]);
    const accent = ensureContrastOnSurfaces(validHex(theme.accent, '#F97316'), surfaces, 4.5, [.1, .16]);
    // Keep neutral surfaces fixed for each mode. A user-provided background
    // cannot guarantee text contrast, so custom skins only alter brand colors.
    return {
      ...neutral,
      p: primary,
      p2: ensureContrastOnSurfaces(blendHex(primary, isDark ? '#FFFFFF' : '#000000', .12), surfaces, 4.5, [.12]),
      accent,
      border: hexToRgba(primary, isDark ? .18 : .2),
    };
  }

  function applyTheme(theme) {
    const normalized = theme || { preset: 'mint', mode: 'system' };
    const mode = resolvedThemeMode(normalized);
    const preset = THEME_PALETTES[normalized.preset];
    const palette = normalized.preset === 'custom'
      ? customPalette(normalized, mode)
      : (preset || THEME_PALETTES.mint)[mode];
    const colors = normalized.preset === 'custom' ? palette : readablePalette(palette, mode);
    const root = document.documentElement;
    root.dataset.themeMode = mode;
    root.style.setProperty('--p', colors.p);
    root.style.setProperty('--p2', colors.p2);
    root.style.setProperty('--on-p', textColorFor(colors.p));
    root.style.setProperty('--on-p2', textColorFor(colors.p2));
    root.style.setProperty('--accent', colors.accent);
    root.style.setProperty('--bg', colors.bg);
    root.style.setProperty('--bg2', colors.bg2);
    root.style.setProperty('--card', colors.card);
    root.style.setProperty('--card-border', colors.border);
    const surfaceOpacity = Number(normalized.surfaceOpacity);
    const surfaceAlpha = (Number.isFinite(surfaceOpacity) ? Math.max(0, Math.min(100, surfaceOpacity)) : 100) / 100;
    root.style.setProperty('--card-rgba', hexToRgba(colors.card, surfaceAlpha));
    root.style.setProperty('--nav-bg', hexToRgba(colors.bg, surfaceAlpha));
    root.style.setProperty('--tx', colors.tx);
    root.style.setProperty('--sub', colors.sub);
    root.style.setProperty('--muted', colors.muted);
    root.style.setProperty('--p-dim', hexToRgba(colors.p, .12));
    root.style.setProperty('--p-glow', hexToRgba(colors.p, .18));
    root.style.setProperty('--p-border', hexToRgba(colors.p, .22));
    root.style.setProperty('--accent-dim', hexToRgba(colors.accent, .1));
    root.style.setProperty('--accent-glow', hexToRgba(colors.accent, .16));
    root.style.setProperty('--accent-border', hexToRgba(colors.accent, .3));
	root.style.setProperty('--font-scale', `${Math.max(100, Math.min(150, Number(normalized.fontScale) || 100)) / 100}`);
    root.style.setProperty('--shadow-md', `0 4px 16px ${hexToRgba(colors.p, .1)}, 0 2px 6px rgba(0,0,0,.05)`);
    root.style.setProperty('--shadow-lg', `0 8px 32px ${hexToRgba(colors.p, .15)}, 0 3px 10px rgba(0,0,0,.08)`);
  }

  async function loadPublicAppearance() {
    try {
      const appearance = await fetch('/api/v1/appearance', { cache: 'no-store' }).then(r => r.ok ? r.json() : null);
      if (!appearance) return;
      applyTheme(appearance.theme);
      applyBrand(appearance.brand);
      applyBackground(appearance.background);
    } catch (_) {}
  }

  function brandAssetURL(kind, version) {
    return Number(version) > 0 ? `/brand/${kind}?v=${encodeURIComponent(version)}` : '';
  }

  function applyBrand(brand) {
    const value = brand || {};
    const title = String(value.title || 'ClipVault').trim() || 'ClipVault';
    document.title = title;
    const name = document.getElementById('brandName');
    if (name) name.textContent = title;

    const logoURL = brandAssetURL('logo', value.logoVersion);
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
    if (lockTitle) lockTitle.textContent = `${title} 锁屏`;
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
    if (favicon) favicon.href = Number(value.faviconVersion) > 0
      ? `/favicon.ico?v=${encodeURIComponent(value.faviconVersion)}`
      : '/favicon.ico';
    const logoPreview = document.getElementById('brandLogoPreview');
    const faviconPreview = document.getElementById('brandFaviconPreview');
    if (logoPreview) {
      if (logoURL) logoPreview.src = logoURL;
      else logoPreview.removeAttribute('src');
    }
    const faviconURL = Number(value.faviconVersion) > 0 ? `/favicon.ico?v=${encodeURIComponent(value.faviconVersion)}` : '';
    if (faviconPreview) {
      if (faviconURL) faviconPreview.src = faviconURL;
      else faviconPreview.removeAttribute('src');
    }
  }

  function applyBrandFormState(brand) {
    const title = document.getElementById('cfg-brandTitle');
    if (title) title.value = brand?.title || 'ClipVault';
    applyBrand(brand);
  }

  async function saveBrandAsset(kind, file) {
    if (!file) return;
    const limits = { logo: 10 * 1024 * 1024, favicon: 10 * 1024 * 1024 };
    if (file.size > limits[kind]) {
      toast('文件不能超过 10 MB', 'err');
      return;
    }
    const accepted = kind === 'logo' ? ['image/png', 'image/jpeg'] : ['image/png', 'image/x-icon', 'image/vnd.microsoft.icon'];
    if (!accepted.includes(file.type)) {
      toast(kind === 'logo' ? 'Logo 仅支持 PNG、JPEG' : 'favicon 仅支持 PNG、ICO', 'err');
      return;
    }
    const complete = await reserveAssetOperation(kind);
    try {
      let res = await secondaryFetch(`/api/v1/appearance/${kind}`, {
        method: 'PUT',
        headers: { 'Content-Type': file.type },
        body: file,
      });
      if (res.status === 403) {
        if (!await requestSecondaryAuth()) return;
        res = await secondaryFetch(`/api/v1/appearance/${kind}`, {
          method: 'PUT',
          headers: { 'Content-Type': file.type },
          body: file,
        });
      }
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      updateCurrentConfig({ brand: data.brand });
      applyBrandFormState(data.brand);
      if (data.runtimeError || data.hotkeyError) {
        toast('素材已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      toast(kind === 'logo' ? 'Logo 已更新' : 'favicon 已更新');
    } catch (error) {
      toast('上传失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  }

  async function resetBrandAsset(kind) {
    const complete = await reserveAssetOperation(kind);
    try {
      let res = await secondaryFetch(`/api/v1/appearance/${kind}`, { method: 'DELETE' });
      if (res.status === 403) {
        if (!await requestSecondaryAuth()) return;
        res = await secondaryFetch(`/api/v1/appearance/${kind}`, { method: 'DELETE' });
      }
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      updateCurrentConfig({ brand: data.brand });
      applyBrandFormState(data.brand);
      if (data.runtimeError || data.hotkeyError) {
        toast('设置已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      toast(kind === 'logo' ? '已还原默认 Logo' : '已还原默认 favicon');
    } catch (error) {
      toast('还原失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  }

  const BACKGROUND_DEFAULTS = {
    opacity: 58, overlay: 64, blur: 0, brightness: 100, contrast: 100,
    saturation: 100, scale: 100, positionX: 50, positionY: 50, fit: 'cover',
  };

  function backgroundValue(background) {
    return { ...BACKGROUND_DEFAULTS, ...(background || {}) };
  }

  function backgroundMediaURL(version) {
    return Number(version) > 0 ? `/background/media?v=${encodeURIComponent(version)}` : '';
  }

  function updateBackgroundOutputs(value) {
    const units = {
      'cfg-backgroundOpacity': '%', 'cfg-backgroundOverlay': '%', 'cfg-backgroundBlur': 'px',
      'cfg-backgroundBrightness': '%', 'cfg-backgroundContrast': '%', 'cfg-backgroundSaturation': '%',
      'cfg-backgroundScale': '%', 'cfg-backgroundPositionX': '%', 'cfg-backgroundPositionY': '%',
    };
    Object.entries(units).forEach(([id, unit]) => {
      const output = document.querySelector(`[data-background-output="${id}"]`);
      const input = document.getElementById(id);
      if (output && input) output.value = `${input.value}${unit}`;
    });
  }

  function applyBackground(background) {
    const value = backgroundValue(background);
    const contentType = String(value.contentType || '');
    const url = backgroundMediaURL(value.version);
    const isImage = contentType.startsWith('image/');
    const isVideo = contentType.startsWith('video/');
    const active = !!url && (isImage || isVideo);
    const root = document.documentElement;
    root.style.setProperty('--background-opacity', `${Math.max(0, Math.min(100, Number(value.opacity))) / 100}`);
    root.style.setProperty('--background-overlay', `${Math.max(0, Math.min(90, Number(value.overlay))) / 100}`);
    root.style.setProperty('--background-filter', `blur(${Math.max(0, Number(value.blur))}px) brightness(${Math.max(40, Number(value.brightness))}%) contrast(${Math.max(40, Number(value.contrast))}%) saturate(${Math.max(0, Number(value.saturation))}%)`);
    root.style.setProperty('--background-scale', `${Math.max(25, Math.min(200, Number(value.scale))) / 100}`);
    root.style.setProperty('--background-position', `${Math.max(0, Math.min(100, Number(value.positionX)))}% ${Math.max(0, Math.min(100, Number(value.positionY)))}%`);
	root.style.setProperty('--background-fit', ['cover', 'contain', 'fill'].includes(value.fit) ? value.fit : 'cover');

    const layer = document.getElementById('backgroundMediaLayer');
    const shield = document.getElementById('backgroundShield');
    const image = document.getElementById('backgroundImage');
    const video = document.getElementById('backgroundVideo');
    document.body.classList.toggle('has-background', active);
    layer?.classList.toggle('hidden', !active);
    shield?.classList.toggle('hidden', !active);
    if (isImage && image) {
      image.src = url;
      video?.pause();
      video?.removeAttribute('src');
      video?.load();
    } else if (isVideo && video) {
      image?.removeAttribute('src');
      video.src = url;
      video.play().catch(() => {});
    } else {
      image?.removeAttribute('src');
      video?.pause();
      video?.removeAttribute('src');
      video?.load();
    }
  }

  function backgroundFromForm() {
    const value = backgroundValue(currentCfg?.background);
    const controls = {
      opacity: 'cfg-backgroundOpacity', overlay: 'cfg-backgroundOverlay', blur: 'cfg-backgroundBlur',
      brightness: 'cfg-backgroundBrightness', contrast: 'cfg-backgroundContrast', saturation: 'cfg-backgroundSaturation',
      scale: 'cfg-backgroundScale', positionX: 'cfg-backgroundPositionX', positionY: 'cfg-backgroundPositionY',
    };
    Object.entries(controls).forEach(([key, id]) => {
      const input = document.getElementById(id);
      if (input) value[key] = Number(input.value);
    });
	const fit = document.getElementById('cfg-backgroundFit');
	if (fit) value.fit = fit.value;
    return value;
  }

  function applyBackgroundFormState(background) {
    const value = backgroundValue(background);
    const controls = {
      opacity: 'cfg-backgroundOpacity', overlay: 'cfg-backgroundOverlay', blur: 'cfg-backgroundBlur',
      brightness: 'cfg-backgroundBrightness', contrast: 'cfg-backgroundContrast', saturation: 'cfg-backgroundSaturation',
      scale: 'cfg-backgroundScale', positionX: 'cfg-backgroundPositionX', positionY: 'cfg-backgroundPositionY',
    };
    Object.entries(controls).forEach(([key, id]) => {
      const input = document.getElementById(id);
      if (input) input.value = value[key];
    });
	const fit = document.getElementById('cfg-backgroundFit');
	if (fit) fit.value = value.fit;
    updateBackgroundOutputs(value);
    applyBackground(value);
  }

  async function saveBackgroundMedia(file) {
    if (!file) return;
    if (file.size > 10 * 1024 * 1024) {
      toast('背景文件不能超过 10 MB', 'err');
      return;
    }
    const complete = await reserveAssetOperation('background');
    try {
	  let res = await secondaryFetch('/api/v1/appearance/background', { method: 'PUT', headers: { 'Content-Type': file.type || 'application/octet-stream' }, body: file });
      if (res.status === 403) {
        if (!await requestSecondaryAuth()) return;
		res = await secondaryFetch('/api/v1/appearance/background', { method: 'PUT', headers: { 'Content-Type': file.type || 'application/octet-stream' }, body: file });
      }
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      updateCurrentConfig({ background: data.background });
      applyBackgroundFormState(data.background);
      if (data.runtimeError || data.hotkeyError) {
        toast('背景已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      toast('背景已更新');
    } catch (error) {
      toast('背景上传失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  }

  async function resetBackgroundMedia() {
    const complete = await reserveAssetOperation('background');
    try {
      let res = await secondaryFetch('/api/v1/appearance/background', { method: 'DELETE' });
      if (res.status === 403) {
        if (!await requestSecondaryAuth()) return;
        res = await secondaryFetch('/api/v1/appearance/background', { method: 'DELETE' });
      }
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      updateCurrentConfig({ background: data.background });
      applyBackgroundFormState(data.background);
      if (data.runtimeError || data.hotkeyError) {
        toast('设置已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      toast('背景已移除');
    } catch (error) {
      toast('移除背景失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  }

  function themeFromForm() {
    const selected = document.querySelector('.theme-preset.active');
    return {
      preset: selected ? selected.dataset.themePreset : (currentCfg?.theme?.preset || 'mint'),
      mode: document.getElementById('cfg-themeMode')?.value || 'system',
      primary: document.getElementById('cfg-themePrimary')?.value || '#14B8A6',
      accent: document.getElementById('cfg-themeAccent')?.value || '#F97316',
	  fontScale: Number(document.getElementById('cfg-themeFontScale')?.value) || 100,
	  surfaceOpacity: Number(document.getElementById('cfg-themeSurfaceOpacity')?.value),
    };
  }

  function applyThemeFormState(theme) {
    const value = theme || { preset: 'mint', mode: 'system' };
    document.querySelectorAll('.theme-preset').forEach(button => {
      const selected = button.dataset.themePreset === value.preset;
      button.classList.toggle('active', selected);
      button.setAttribute('aria-checked', selected ? 'true' : 'false');
    });
    const mode = document.getElementById('cfg-themeMode');
    if (mode) mode.value = value.mode || 'system';
    const primary = document.getElementById('cfg-themePrimary');
    const accent = document.getElementById('cfg-themeAccent');
    if (primary) primary.value = validHex(value.primary, '#14B8A6');
    if (accent) accent.value = validHex(value.accent, '#F97316');
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
    applyTheme(value.preset === 'custom'
      ? themeFromForm()
      : { preset: value.preset || 'mint', mode: value.mode || 'system', fontScale: Number(value.fontScale) || 100, surfaceOpacity: surfaceOpacityValue });
  }

  async function loadTheme() {
    const request = ++configRequestSequence;
    const revision = configRevision;
    try {
      const cfg = await fetch('/api/v1/config').then(r => r.json());
      if (request !== configRequestSequence || revision !== configRevision) return;
      replaceCurrentConfig(cfg);
      applyTheme(cfg.theme);
	  applyBrand(cfg.brand);
	  applyBackground(cfg.background);
    } catch (_) {
      if (request !== configRequestSequence || revision !== configRevision) return;
      applyTheme();
	  applyBrand();
	  applyBackground();
    }
  }

  if (window.matchMedia) {
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
      if (currentCfg?.theme?.mode === 'system') applyTheme(currentCfg.theme);
    });
  }

  async function loadSettings() {