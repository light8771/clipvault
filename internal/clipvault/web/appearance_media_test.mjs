import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = {
  fetch: async () => { throw new Error('unexpected native fetch'); },
  location: { origin: 'http://localhost' },
  clipvaultI18n: { t: key => key }
};
const { app } = await import('./app/app-context.js');
const { registerAppearanceMedia } = await import('./app/appearance-media.js');
registerAppearanceMedia();

test('brand upload sends missing or inaccurate MIME hints to server for content detection', async () => {
  const requests = [];
  const errors = [];
  app.protectedFetch = async (url, options) => {
    requests.push({ url, options });
    return { json: async () => ({ brand: {} }) };
  };
  app.requireOK = async () => {};
  app.updateCurrentConfig = () => {};
  app.applyBrand = () => {};
  app.toast = (message, level) => { if (level === 'err') errors.push(message); };
  for (const kind of ['logo', 'favicon']) {
    for (const type of ['', 'application/octet-stream', 'image/jpeg']) {
      const file = { name: 'image.png', size: 1024, type };
      await app.saveBrandAsset(kind, file);
      const request = requests.at(-1);
      assert.equal(request?.url, `/api/v1/appearance/${kind}`);
      assert.equal(request.options.body, file);
      assert.equal(request.options.headers['Content-Type'], type || 'application/octet-stream');
    }
  }
  assert.equal(requests.length, 6);
  assert.deepEqual(errors, []);
  await app.saveBrandAsset('logo', { size: 10 * 1024 * 1024 + 1, type: 'image/png' });
  assert.equal(requests.length, 6);
  assert.deepEqual(errors, ['appearance.asset_too_large']);
});

test('background videos with no MIME or extension are not blocked by the image size limit', async () => {
  const requests = [];
  const errors = [];
  app.protectedFetch = async (url, options) => {
    requests.push({ url, options });
    return { json: async () => ({ background: {} }) };
  };
  app.requireOK = async () => {};
  app.updateCurrentConfig = () => {};
  app.applyBackground = () => {};
  app.backgroundFromForm = () => ({});
  app.toast = (message, level) => { if (level === 'err') errors.push(message); };
  for (const type of ['', 'application/octet-stream', 'video/mp4', 'video/webm']) {
    const file = { name: 'background', size: 128 * 1024 * 1024, type };
    await app.saveBackgroundMedia(file);
    assert.equal(requests.at(-1)?.options.body, file);
  }
  assert.equal(requests.length, 4);
  assert.deepEqual(errors, []);
});

test('asset responses preserve editable values and their snapshot version', () => {
  registerAppearanceMedia();
  app.configVersion = 'original-version';
  app.currentCfg = {
    brand: { title: 'local title', logoVersion: 1 },
    background: { opacity: 37, fit: 'contain', version: 1 }
  };
  app.replaceCurrentConfig = cfg => { app.currentCfg = cfg; };
  app.updateCurrentConfig({ brand: { title: 'other page title', logoVersion: 2, logoContentType: 'image/png' } });
  app.updateCurrentConfig({ background: { opacity: 100, fit: 'cover', version: 3, contentType: 'video/mp4' } });
  assert.equal(app.currentCfg.brand.title, 'local title');
  assert.equal(app.currentCfg.brand.logoVersion, 2);
  assert.equal(app.currentCfg.background.opacity, 37);
  assert.equal(app.currentCfg.background.fit, 'contain');
  assert.equal(app.currentCfg.background.version, 3);
  assert.equal(app.configVersion, 'original-version');
});
