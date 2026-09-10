import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = { fetch: async () => {}, location: { origin: 'http://localhost' }, clipvaultI18n: { t: key => key } };
const save = { disabled: false };
const input = { disabled: false };
let hidden = false;
const modal = { addEventListener() {}, querySelectorAll: () => [save, input], classList: { add() { hidden = true; }, remove() { hidden = false; } } };
globalThis.document = { activeElement: null, getElementById: id => id === 'sourceAppsModal' ? modal : id === 'sourceAppsSaveBtn' ? save : null, querySelectorAll: () => [] };
const { app } = await import('./app/app-context.js');
const { registerSettingsSources } = await import('./app/settings-sources.js');

function setup() {
  registerSettingsSources();
  app.allSourceExes = [];
  app.sourceAliases = { old: 'saved' };
  app.sourceWhitelist = null;
  app.pendingConfigRevision = 0;
  app.syncVisibleSourceAppsDraft = () => {};
  app.renderSourceAppsDialog = () => {};
  app.updateSourceAppsSummary = () => {};
  app.toast = () => {};
  app.sourceAppsDraft = { mode: 'all', aliases: { new: 'draft' } };
  save.disabled = false;
  input.disabled = false;
  hidden = false;
}

test('source dialog can save again after a successful save and reopening', async () => {
  setup();
  let saves = 0;
  app.saveConfig = async () => { saves++; return true; };
  await app.saveSourceAppsDialog();
  app.openSourceAppsDialog();
  assert.equal(save.disabled, false);
  await app.saveSourceAppsDialog();
  assert.equal(saves, 2);
});

test('source save owns its dialog until completion and restores controls on failure', async () => {
  setup();
  let complete;
  let saves = 0;
  app.saveConfig = () => { saves++; return new Promise(resolve => { complete = resolve; }); };
  const saving = app.saveSourceAppsDialog();
  const disabledDuringSave = input.disabled;
  app.closeSourceAppsDialog();
  const hiddenDuringSave = hidden;
  await app.saveSourceAppsDialog();
  complete(false);
  await saving;
  assert.equal(disabledDuringSave, true);
  assert.equal(hiddenDuringSave, false);
  assert.equal(saves, 1);
  assert.equal(save.disabled, false);
  assert.equal(input.disabled, false);
  assert.deepEqual(app.sourceAliases, { old: 'saved' });
  assert.deepEqual(app.sourceAppsDraft.aliases, { new: 'draft' });
});
