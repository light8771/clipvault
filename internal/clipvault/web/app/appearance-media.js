import { app } from "./app-context.js";

const t = window.clipvaultI18n.t;

export function registerAppearanceMedia() {
  app.assetOperationTail = {
    logo: Promise.resolve(),
    favicon: Promise.resolve(),
    background: Promise.resolve()
  };
  app.updateCurrentConfig = function updateCurrentConfig(update) {
    const cfg = { ...app.currentCfg };
    // Asset responses own metadata only; never replace editable values or
    // advance the version of the settings snapshot held by this page.
    if (update.brand) {
      const { logoContentType, logoVersion, faviconContentType, faviconVersion } = update.brand;
      cfg.brand = { ...cfg.brand, logoContentType, logoVersion, faviconContentType, faviconVersion };
    }
    if (update.background) {
      const { contentType, version } = update.background;
      cfg.background = { ...cfg.background, contentType, version };
    }
    app.replaceCurrentConfig(cfg);
  };
  app.reserveAssetOperation = async function reserveAssetOperation(kind) {
    const previous = app.assetOperationTail[kind];
    let complete;
    app.assetOperationTail[kind] = new Promise(resolve => {
      complete = resolve;
    });
    await previous;
    return complete;
  };
  app.applyBrandFormState = function applyBrandFormState(brand) {
    const title = document.getElementById('cfg-brandTitle');
    if (title) title.value = brand?.title || 'ClipVault';
    app.applyBrand(brand);
  };
  app.saveBrandAsset = async function saveBrandAsset(kind, file) {
    if (!file) return;
    const limits = {
      logo: 10 * 1024 * 1024,
      favicon: 10 * 1024 * 1024
    };
    if (file.size > limits[kind]) {
      app.toast(t('appearance.asset_too_large'), 'err');
      return;
    }
    // 文件类型由服务端根据内容识别，浏览器 MIME 可能为空或不准确。
    const complete = await app.reserveAssetOperation(kind);
    try {
      const res = await app.protectedFetch(`/api/v1/appearance/${kind}`, {
        method: 'PUT',
        headers: {
          'Content-Type': file.type || 'application/octet-stream'
        },
        body: file
      });
      await app.requireOK(res, t('appearance.asset_upload_failed'));
      const data = await res.json();
      app.updateCurrentConfig({
        brand: data.brand
      });
      app.applyBrand(app.currentCfg?.brand);
      if (data.runtimeError || data.hotkeyError) {
        app.toast(t('appearance.asset_saved_with_warning', { warning: data.runtimeError || data.hotkeyError }), 'err');
        return;
      }
      app.toast(kind === 'logo' ? t('appearance.logo_updated') : t('appearance.favicon_updated'));
    } catch (error) {
      app.toast(t('appearance.upload_failed_with', { message: error.message || error }), 'err');
    } finally {
      complete();
    }
  };
  app.resetBrandAsset = async function resetBrandAsset(kind) {
    const complete = await app.reserveAssetOperation(kind);
    try {
      const res = await app.protectedFetch(`/api/v1/appearance/${kind}`, {
        method: 'DELETE'
      });
      await app.requireOK(res, t('appearance.asset_reset_failed'));
      const data = await res.json();
      app.updateCurrentConfig({
        brand: data.brand
      });
      app.applyBrand(app.currentCfg?.brand);
      if (data.runtimeError || data.hotkeyError) {
        app.toast(t('config.saved_with_warning', { warning: data.runtimeError || data.hotkeyError }), 'err');
        return;
      }
      app.toast(kind === 'logo' ? t('appearance.logo_reset') : t('appearance.favicon_reset'));
    } catch (error) {
      app.toast(t('appearance.reset_failed_with', { message: error.message || error }), 'err');
    } finally {
      complete();
    }
  };
  app.updateBackgroundOutputs = function updateBackgroundOutputs(value) {
    const units = {
      'cfg-backgroundOpacity': '%',
      'cfg-backgroundOverlay': '%',
      'cfg-backgroundBlur': 'px',
      'cfg-backgroundBrightness': '%',
      'cfg-backgroundContrast': '%',
      'cfg-backgroundSaturation': '%',
      'cfg-backgroundScale': '%',
      'cfg-backgroundPositionX': '%',
      'cfg-backgroundPositionY': '%'
    };
    Object.entries(units).forEach(([id, unit]) => {
      const output = document.querySelector(`[data-background-output="${id}"]`);
      const input = document.getElementById(id);
      if (output && input) output.value = `${input.value}${unit}`;
    });
  };
  app.backgroundFromForm = function backgroundFromForm() {
    const value = app.backgroundValue(app.currentCfg?.background);
    const controls = {
      opacity: 'cfg-backgroundOpacity',
      overlay: 'cfg-backgroundOverlay',
      blur: 'cfg-backgroundBlur',
      brightness: 'cfg-backgroundBrightness',
      contrast: 'cfg-backgroundContrast',
      saturation: 'cfg-backgroundSaturation',
      scale: 'cfg-backgroundScale',
      positionX: 'cfg-backgroundPositionX',
      positionY: 'cfg-backgroundPositionY'
    };
    Object.entries(controls).forEach(([key, id]) => {
      const input = document.getElementById(id);
      if (input) value[key] = Number(input.value);
    });
    const fit = document.getElementById('cfg-backgroundFit');
    if (fit) value.fit = fit.value;
    return value;
  };
  app.applyBackgroundFormState = function applyBackgroundFormState(background) {
    const value = app.backgroundValue(background);
    const controls = {
      opacity: 'cfg-backgroundOpacity',
      overlay: 'cfg-backgroundOverlay',
      blur: 'cfg-backgroundBlur',
      brightness: 'cfg-backgroundBrightness',
      contrast: 'cfg-backgroundContrast',
      saturation: 'cfg-backgroundSaturation',
      scale: 'cfg-backgroundScale',
      positionX: 'cfg-backgroundPositionX',
      positionY: 'cfg-backgroundPositionY'
    };
    Object.entries(controls).forEach(([key, id]) => {
      const input = document.getElementById(id);
      if (input) input.value = value[key];
    });
    const fit = document.getElementById('cfg-backgroundFit');
    if (fit) fit.value = value.fit;
    app.updateBackgroundOutputs(value);
    app.applyBackground(value);
  };
  app.saveBackgroundMedia = async function saveBackgroundMedia(file) {
    if (!file) return;
    // 后台按实际内容区分图片和视频，避免把 MIME 缺失的视频套用图片大小限制。
    const complete = await app.reserveAssetOperation('background');
    try {
      const res = await app.protectedFetch('/api/v1/appearance/background', {
        method: 'PUT',
        headers: {
          'Content-Type': file.type || 'application/octet-stream'
        },
        body: file
      });
      await app.requireOK(res, t('appearance.background_upload_failed'));
      const data = await res.json();
      app.updateCurrentConfig({
        background: data.background
      });
      app.applyBackground(app.backgroundFromForm());
      if (data.runtimeError || data.hotkeyError) {
        app.toast(t('appearance.background_saved_with_warning', { warning: data.runtimeError || data.hotkeyError }), 'err');
        return;
      }
      app.toast(t('appearance.background_updated'));
    } catch (error) {
      app.toast(t('appearance.background_upload_failed_with', { message: error.message || error }), 'err');
    } finally {
      complete();
    }
  };
  app.resetBackgroundMedia = async function resetBackgroundMedia() {
    const complete = await app.reserveAssetOperation('background');
    try {
      const res = await app.protectedFetch('/api/v1/appearance/background', {
        method: 'DELETE'
      });
      await app.requireOK(res, t('appearance.background_remove_failed'));
      const data = await res.json();
      app.updateCurrentConfig({
        background: data.background
      });
      app.applyBackground(app.backgroundFromForm());
      if (data.runtimeError || data.hotkeyError) {
        app.toast(t('config.saved_with_warning', { warning: data.runtimeError || data.hotkeyError }), 'err');
        return;
      }
      app.toast(t('appearance.background_removed'));
    } catch (error) {
      app.toast(t('appearance.background_remove_failed_with', { message: error.message || error }), 'err');
    } finally {
      complete();
    }
  };
}
