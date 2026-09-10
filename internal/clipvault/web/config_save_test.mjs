import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = { fetch: async () => {}, location: { origin: 'http://localhost' }, clipvaultI18n: { t: key => key } };
const control = { addEventListener() {} };
globalThis.document = { getElementById: id => id.startsWith('restart') ? control : null };
const { app } = await import('./app/app-context.js');
const { registerConfigSave } = await import('./app/config-save.js');

test('config saves carry their loaded version and advance it only after success', async () => {
  registerConfigSave();
  const messages = [];
  const config = { search: { listenAddr: '127.0.0.1:15678' }, brand: { title: 'draft' } };
  app.currentCfg = config;
  app.configVersion = 'loaded-version';
  app.collectConfig = () => config;
  app.toast = message => messages.push(message);
  app.replaceCurrentConfig = (cfg, version) => { app.currentCfg = cfg; app.configVersion = version; };
  app.requireOK = async response => { if (!response.ok) throw new Error('conflict'); };
  let success = false;
  const sent = [];
  app.protectedFetch = async (url, options) => {
    sent.push(options.headers['X-ClipVault-Config-Version']);
    return { ok: success, headers: new Headers({ 'X-ClipVault-Config-Version': 'saved-version' }), json: async () => ({ config }) };
  };
  assert.equal(await app.saveConfigNow(), false);
  assert.equal(app.configVersion, 'loaded-version');
  assert.equal(app.currentCfg.brand.title, 'draft');
  assert.deepEqual(messages, ['config.save_failed_with']);
  success = true;
  assert.equal(await app.saveConfigNow(), true);
  assert.equal(app.configVersion, 'saved-version');
  assert.deepEqual(sent, ['loaded-version', 'loaded-version']);
  assert.equal(messages.at(-1), 'config.saved');
});
