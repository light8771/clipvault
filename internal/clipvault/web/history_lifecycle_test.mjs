import assert from 'node:assert/strict';
import test from 'node:test';
globalThis.window = {fetch() {}, location: {origin: 'http://localhost', search: ''}, addEventListener() {}, clipvaultI18n: {t: key => key}};
const listeners = new Map();
globalThis.document = {getElementById: () => ({addEventListener() {}}), addEventListener: (name, listener) => listeners.set(name, listener)};
const {app} = await import('./app/app-context.js');
const {registerHistoryLifecycle} = await import('./app/history-lifecycle.js');
test('unlock refreshes the selected projection without rebinding source controls', async () => {
  let bindings = 0, searches = 0;
  const projections = [];
  Object.assign(app, {
    setupSrcExeUI: () => bindings++, loadConfigAppearance: async () => {}, loadLicenseStatus: async () => {},
    loadPublicAppearance: async () => {}, checkAuthStatus: async () => false,
    loadSourceExesForHistory: async (kind, options) => {assert.equal(options.refresh, true); projections.push(kind); return true;},
    search: () => searches++, loadGroups() {}, currentKind: '__events__'
  });
  registerHistoryLifecycle();
  await app.init();
  await app.init();
  app.currentKind = 'image';
  await app.init();
  assert.equal(bindings, 1);
  assert.equal(searches, 3);
  assert.deepEqual(projections, ['event', 'event', 'content']);
});

test('returning to a locked tab does not start protected history refreshes', async () => {
  document.visibilityState = 'visible';
  app.authState = {secondaryAuthed: true};
  app.checkAuthStatus = async () => {app.authState.secondaryAuthed = false; return false;};
  app.loadLicenseStatus = () => assert.fail('locked tab refreshed license');
  app.loadSourceExesForHistory = () => assert.fail('locked tab queried sources');
  app.search = () => assert.fail('locked tab queried history');
  await listeners.get('visibilitychange')();
});

test('superseded initialization does not run another history search', async () => {
  app.loadLicenseStatus = async () => {};
  app.loadSourceExesForHistory = async () => false;
  app.search = () => assert.fail('superseded initialization queried history');
  await app.init();
});
