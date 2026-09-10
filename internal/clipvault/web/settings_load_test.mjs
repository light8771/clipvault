import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = { fetch: async () => {}, location: { origin: 'http://localhost' }, clipvaultI18n: { t: key => key } };
globalThis.document = { getElementById: () => null };
const { app } = await import('./app/app-context.js');
const { registerSettingsStorage } = await import('./app/settings-storage.js');

test('slow source statistics do not delay settings or overwrite a later draft', async () => {
  registerSettingsStorage();
  app.configRequestSequence = 0;
  app.configRevision = 0;
  app.hasPendingConfigSave = () => false;
  const config = { brand: { title: 'saved title' } };
  app.fetch = async () => ({ headers: new Headers(), json: async () => config });
  app.requireOK = async () => {};
  app.replaceCurrentConfig = cfg => { app.configRevision++; app.currentCfg = cfg; };
  let finishSources;
  let sourcesStarted;
  const started = new Promise(resolve => { sourcesStarted = resolve; });
  app.loadSourceExes = () => { sourcesStarted(); return new Promise(resolve => { finishSources = resolve; }); };
  let title = '';
  let renders = 0;
  app.applyConfigToForm = cfg => { title = cfg.brand.title; renders++; };
  app.loadRecoveryStatus = () => {};
  app.loadStorageStatus = () => {};
  const loading = app.loadSettings();
  await started;
  const titleBeforeSources = title;
  title = 'new user draft';
  finishSources();
  await loading;
  assert.equal(titleBeforeSources, 'saved title');
  assert.equal(title, 'new user draft');
  assert.equal(renders, 1);
});
