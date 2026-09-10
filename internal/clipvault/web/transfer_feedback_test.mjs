import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = { fetch: globalThis.fetch, location: { origin: 'http://localhost' }, clipvaultI18n: { t: key => key }, setTimeout: fn => fn() };
const { app } = await import('./app/app-context.js');
const { downloadProtectedFile } = await import('./app/download.js');
const { createOperationState } = await import('./app/operation-state.js');

test('file operation locks immediately, rejects duplicates and restores original disabled controls after failure', () => {
  const enabled = { disabled: false }, disabled = { disabled: true };
  const root = { querySelectorAll: () => [enabled, disabled], setAttribute() {}, removeAttribute() {}, appendChild(node) { this.status = node; } };
  globalThis.document = { createElement: () => ({ setAttribute() {} }) };
  const state = createOperationState(root);
  assert.equal(state.begin('Uploading'), true);
  assert.equal(enabled.disabled, true);
  assert.equal(root.status.hidden, false);
  assert.equal(state.begin('Duplicate'), false);
  state.finish('Retry', true);
  assert.equal(enabled.disabled, false);
  assert.equal(disabled.disabled, true);
  assert.equal(root.status.textContent, 'Retry');
  assert.equal(state.begin('Retrying'), true);
  state.finish();
});

test('download remains pending until the full response body arrives and errors never trigger a download', async () => {
  let receiveBody;
  const body = new Promise(resolve => { receiveBody = resolve; });
  let clicks = 0;
  const errors = [];
  const link = { click() { clicks++; }, remove() {} };
  globalThis.document = { createElement: () => link, body: { appendChild() {} } };
  app.toast = message => errors.push(message);
  app.protectedFetch = async () => ({ ok: true, headers: new Headers({ 'Content-Disposition': 'attachment; filename="language.json"' }), blob: () => body });
  let completed = false;
  const pending = downloadProtectedFile('/export', 'fallback.json').then(ok => { completed = true; return ok; });
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(completed, false);
  assert.equal(clicks, 0);
  receiveBody(new Blob(['{}']));
  assert.equal(await pending, true);
  assert.equal(link.download, 'language.json');
  assert.equal(clicks, 1);
  app.protectedFetch = async () => new Response(JSON.stringify({ error: 'Failed' }), { status: 500, headers: { 'Content-Type': 'application/json' } });
  assert.equal(await downloadProtectedFile('/export', 'fallback.json'), false);
  assert.equal(clicks, 1);
  assert.equal(errors.length, 1);
});
