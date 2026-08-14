import { app } from "./app-context.js";
export function registerAppearanceMedia() {
  app.assetOperationTail = {
    logo: Promise.resolve(),
    favicon: Promise.resolve(),
    background: Promise.resolve()
  };
  app.updateCurrentConfig = function updateCurrentConfig(update) {
    app.replaceCurrentConfig({
      ...app.currentCfg,
      ...update
    });
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
      app.toast('文件不能超过 10 MB', 'err');
      return;
    }
    const accepted = kind === 'logo' ? ['image/png', 'image/jpeg'] : ['image/png', 'image/x-icon', 'image/vnd.microsoft.icon'];
    if (!accepted.includes(file.type)) {
      app.toast(kind === 'logo' ? 'Logo 仅支持 PNG、JPEG' : 'favicon 仅支持 PNG、ICO', 'err');
      return;
    }
    const complete = await app.reserveAssetOperation(kind);
    try {
      const res = await app.protectedFetch(`/api/v1/appearance/${kind}`, {
        method: 'PUT',
        headers: {
          'Content-Type': file.type
        },
        body: file
      });
      await app.requireOK(res, '素材上传失败');
      const data = await res.json();
      app.updateCurrentConfig({
        brand: data.brand
      });
      app.applyBrandFormState(data.brand);
      if (data.runtimeError || data.hotkeyError) {
        app.toast('素材已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      app.toast(kind === 'logo' ? 'Logo 已更新' : 'favicon 已更新');
    } catch (error) {
      app.toast('上传失败: ' + (error.message || error), 'err');
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
      await app.requireOK(res, '素材还原失败');
      const data = await res.json();
      app.updateCurrentConfig({
        brand: data.brand
      });
      app.applyBrandFormState(data.brand);
      if (data.runtimeError || data.hotkeyError) {
        app.toast('设置已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      app.toast(kind === 'logo' ? '已还原默认 Logo' : '已还原默认 favicon');
    } catch (error) {
      app.toast('还原失败: ' + (error.message || error), 'err');
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
    if (file.size > 10 * 1024 * 1024) {
      app.toast('背景文件不能超过 10 MB', 'err');
      return;
    }
    const complete = await app.reserveAssetOperation('background');
    try {
      const res = await app.protectedFetch('/api/v1/appearance/background', {
        method: 'PUT',
        headers: {
          'Content-Type': file.type || 'application/octet-stream'
        },
        body: file
      });
      await app.requireOK(res, '背景上传失败');
      const data = await res.json();
      app.updateCurrentConfig({
        background: data.background
      });
      app.applyBackgroundFormState(data.background);
      if (data.runtimeError || data.hotkeyError) {
        app.toast('背景已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      app.toast('背景已更新');
    } catch (error) {
      app.toast('背景上传失败: ' + (error.message || error), 'err');
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
      await app.requireOK(res, '背景移除失败');
      const data = await res.json();
      app.updateCurrentConfig({
        background: data.background
      });
      app.applyBackgroundFormState(data.background);
      if (data.runtimeError || data.hotkeyError) {
        app.toast('设置已保存，但' + (data.runtimeError || data.hotkeyError), 'err');
        return;
      }
      app.toast('背景已移除');
    } catch (error) {
      app.toast('移除背景失败: ' + (error.message || error), 'err');
    } finally {
      complete();
    }
  };
}
