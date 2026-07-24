(function(root, factory) {
  const api = factory();
  root.ClipVaultTheme = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
}(typeof window !== 'undefined' ? window : globalThis, function() {
  'use strict';

  function validHex(value, fallback) {
    return /^#[0-9a-f]{6}$/i.test(value || '') ? value : fallback;
  }

  function rgb(hex) {
    const value = validHex(hex, '#000000');
    return [1, 3, 5].map(offset => parseInt(value.slice(offset, offset + 2), 16));
  }

  function blendHex(from, to, amount) {
    const a = rgb(from);
    const b = rgb(to);
    const ratio = Math.max(0, Math.min(1, amount));
    return '#' + a.map((channel, index) => Math.round(
      channel * (1 - ratio) + b[index] * ratio
    ).toString(16).padStart(2, '0')).join('');
  }

  function hexToRgba(hex, alpha) {
    const [r, g, b] = rgb(hex);
    return `rgba(${r},${g},${b},${alpha})`;
  }

  function relativeLuminance(hex) {
    const channels = rgb(hex).map(channel => {
      const value = channel / 255;
      return value <= 0.04045 ? value / 12.92 : Math.pow((value + 0.055) / 1.055, 2.4);
    });
    return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
  }

  function contrastRatio(first, second) {
    const [lighter, darker] = [relativeLuminance(first), relativeLuminance(second)].sort((a, b) => b - a);
    return (lighter + 0.05) / (darker + 0.05);
  }

  // Preserve a selected hue where possible, only moving toward black or white
  // until it is readable on the current surface.
  function ensureContrast(color, background, minimum) {
    const base = validHex(color, '#14B8A6');
    const surface = validHex(background, '#FFFFFF');
    const targetRatio = minimum || 4.5;
    if (contrastRatio(base, surface) >= targetRatio) return base;

    const target = relativeLuminance(surface) > 0.5 ? '#000000' : '#FFFFFF';
    for (let step = 1; step <= 100; step++) {
      const candidate = blendHex(base, target, step / 100);
      if (contrastRatio(candidate, surface) >= targetRatio) return candidate;
    }
    return target;
  }

  // Interactive controls use the selected color as a translucent background.
  // Validate both the base surfaces and those composited states.
  function ensureContrastOnSurfaces(color, surfaces, minimum, tintAlphas) {
    const base = validHex(color, '#14B8A6');
    const validSurfaces = (surfaces || []).map(surface => validHex(surface, '')).filter(Boolean);
    const candidates = validSurfaces.length ? validSurfaces : ['#FFFFFF'];
    const targetRatio = minimum || 4.5;
    const alphas = tintAlphas || [];
    const meetsContrast = candidate => candidates.every(surface =>
      contrastRatio(candidate, surface) >= targetRatio &&
      alphas.every(alpha => contrastRatio(candidate, blendHex(surface, candidate, alpha)) >= targetRatio)
    );

    if (meetsContrast(base)) return base;
    const averageLuminance = candidates.reduce((sum, surface) => sum + relativeLuminance(surface), 0) / candidates.length;
    const target = averageLuminance > 0.5 ? '#000000' : '#FFFFFF';
    for (let step = 1; step <= 100; step++) {
      const candidate = blendHex(base, target, step / 100);
      if (meetsContrast(candidate)) return candidate;
    }
    return target;
  }

  function textColorFor(background) {
    return contrastRatio('#FFFFFF', background) >= contrastRatio('#000000', background)
      ? '#FFFFFF'
      : '#000000';
  }

  return { validHex, blendHex, hexToRgba, relativeLuminance, contrastRatio, ensureContrast, ensureContrastOnSurfaces, textColorFor };
}));
