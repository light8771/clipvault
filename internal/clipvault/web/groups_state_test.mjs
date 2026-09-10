import assert from 'node:assert/strict';
import test from 'node:test';

let body = null;
globalThis.window = { fetch: async () => {}, location: { origin: 'http://localhost' }, clipvaultI18n: { t: key => key } };
globalThis.document = { getElementById: id => id === 'groupModalBody' ? body : null };
const { app } = await import('./app/app-context.js');
const { registerGroups } = await import('./app/groups.js');
const { registerGroupsPage } = await import('./app/groups-page.js');
const deferred = () => { let resolve, reject; const promise = new Promise((a,b) => { resolve=a; reject=b; }); return { promise, resolve, reject }; };
const response = data => ({ ok: true, json: async () => data });
const group = { id: 1, parentId: 0, name: 'saved' };
function setup() { body = null; registerGroups(); registerGroupsPage(); app.toast = () => {}; app.esc = String; }

test('failed group refresh preserves the last successful list', async () => {
  setup(); app.allGroups = [group]; app.fetch = async () => { throw new Error('offline'); };
  await app.loadGroups(); assert.deepEqual(app.allGroups, [group]);
  app.fetch = async () => ({ ok: false, json: async () => ({error: 'denied'}) });
  await app.loadGroups(); assert.deepEqual(app.allGroups, [group]);
});

test('late group response cannot overwrite a newer refresh', async () => {
  setup(); const old = deferred(); let n = 0;
  app.fetch = () => ++n === 1 ? old.promise : Promise.resolve(response({ groups: [group] }));
  const first = app.loadGroups(); await app.loadGroups(); old.resolve(response({ groups: [] })); await first;
  assert.deepEqual(app.allGroups, [group]);
});

test('inline creation ignores repeat submission and retains the name after failure', async () => {
  setup(); const pending = deferred(); let writes = 0, refreshes = 0;
  app.fetch = () => { writes++; return pending.promise; };
  app.refreshGroupViews = async () => { refreshes++; };
  app.groupCreatingUnder = 0;
  const input = { value: 'draft', disabled: false };
  const first = app.commitInlineCreate(input, 0);
  const repeat = app.commitInlineCreate(input, 0);
  assert.equal(writes, 1);
  pending.resolve({ ok: false, json: async () => ({error: 'try again'}) });
  await Promise.all([first,repeat]);
  assert.equal(input.value, 'draft'); assert.equal(input.disabled, false);
  assert.equal(app.groupCreatingUnder, 0); assert.equal(refreshes, 0);
  app.fetch = async () => { writes++; return response({ok: true}); };
  await app.commitInlineCreate(input, 0);
  assert.equal(writes, 2); assert.equal(refreshes, 1);
});

test('failure from an obsolete assign modal does not close its replacement', async () => {
  setup(); app.allGroups = [group]; const pending = deferred(); let closed = 0;
  app.authorizedItemFetch = () => pending.promise;
  app.closeGroupModal = () => { closed++; };
  app.bindGroupModalEvents = () => {};
  body = { innerHTML: '' }; app.groupModalMode = 'assign'; app.groupModalOpts = {itemId: 1};
  const old = app.renderGroupModalContent();
  body = { innerHTML: '' }; app.groupModalMode = 'filter'; app.groupModalOpts = {};
  await app.renderGroupModalContent();
  pending.reject(new Error('late failure')); await old;
  assert.equal(closed, 0); assert.match(body.innerHTML, /saved/);
});

test('failed group rename retains its draft and retries only once at a time', async () => {
  setup(); const listeners = {}; let replacements = 0, writes = 0;
  const name = { textContent: 'old', replaceWith() {} };
  const input = { value: '', readOnly: false, focus() {}, select() {}, replaceWith() { replacements++; }, addEventListener: (event, fn) => { listeners[event] = fn; } };
  document.querySelector = () => name;
  document.createElement = () => input;
  app.refreshGroupViews = async () => { replacements++; };
  const pending = deferred(); app.fetch = () => { writes++; return pending.promise; };
  app.startRenameGroup(1); input.value = 'new draft';
  const first = listeners.blur(); const second = listeners.blur();
  assert.equal(writes, 1);
  pending.resolve({ ok: false, json: async () => ({ error: 'retry' }) });
  await Promise.all([first, second]);
  assert.equal(replacements, 0); assert.equal(input.value, 'new draft'); assert.equal(input.readOnly, false);
  app.fetch = async () => { writes++; return response({ ok: true }); };
  await listeners.blur(); assert.equal(writes, 2);
});

test('Escape cancels rename and a subsequent blur cannot save it', async () => {
  setup(); const listeners = {}; let writes = 0;
  const name = { textContent: 'old', replaceWith() {} };
  const input = { value: '', focus() {}, select() {}, replaceWith() {}, addEventListener: (event, fn) => { listeners[event] = fn; } };
  document.querySelector = () => name; document.createElement = () => input;
  app.refreshGroupViews = async () => {};
  app.fetch = async () => { writes++; return response({ ok: true }); };
  app.startRenameGroup(1); input.value = 'cancelled';
  listeners.keydown({ key: 'Escape', preventDefault() {} }); await listeners.blur();
  assert.equal(writes, 0);
});
