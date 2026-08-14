import { app } from './app-context.js';

export function registerAppearanceBackground() {
  app.BACKGROUND_DEFAULTS = {
    opacity: 58,
    overlay: 64,
    blur: 0,
    brightness: 100,
    contrast: 100,
    saturation: 100,
    scale: 100,
    positionX: 50,
    positionY: 50,
    fit: 'cover'
  };
  app.backgroundValue = function backgroundValue(background) {
    return {
      ...app.BACKGROUND_DEFAULTS,
      ...(background || {})
    };
  };
  app.backgroundMediaURL = function backgroundMediaURL(version) {
    return Number(version) > 0 ? `/background/media?v=${encodeURIComponent(version)}` : '';
  };
  app.applyBackground = function applyBackground(background) {
    const value = app.backgroundValue(background);
    const contentType = String(value.contentType || '');
    const url = app.backgroundMediaURL(value.version);
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
    layer.classList.toggle('hidden', !active);
    shield.classList.toggle('hidden', !active);
    if (isImage && image) {
      image.src = url;
      video.pause();
      video.removeAttribute('src');
      video.load();
    } else if (isVideo && video) {
      image.removeAttribute('src');
      video.src = url;
      video.play().catch(() => {});
    } else {
      image.removeAttribute('src');
      video.pause();
      video.removeAttribute('src');
      video.load();
    }
  };
}
