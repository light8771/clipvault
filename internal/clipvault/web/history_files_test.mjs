import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = { fetch: async () => { throw new Error('unexpected native fetch'); }, clipvaultI18n: { t: key => key } };
const { app } = await import('./app/app-context.js');
const { registerTextUtils } = await import('./app/text-utils.js');
const { registerHistoryFiles } = await import('./app/history-files.js');
registerTextUtils();
app.filePaths = new Map();
app.IMAGE_EXTS = ['png'];
app.toast = () => {};
registerHistoryFiles();

function fileSurface() {
  const stat = { innerHTML: '' };
  const hash = { innerHTML: '', isConnected: true };
  const row = {
    dataset: {}, isConnected: true,
    querySelector: selector => ({ '.file-status': stat, '.file-hash': hash })[selector] || null,
    querySelectorAll: () => [button]
  };
  const button = { dataset: {}, disabled: false, closest: () => row };
  const root = { querySelectorAll: () => [{ querySelector: () => row }] };
  return { root, row, stat, hash, button };
}

test('card and composite modal update their own file status and hash', async () => {
  const card = fileSurface(), modal = fileSurface();
  app.renderFileList({ id: 11 }, ['C:\\report.pdf']);
  app.fetch = async () => ({ ok: true, json: async () => ({ exists: true, size: 1024 }) });
  await app.checkFiles(11, card.root);
  assert.match(card.stat.innerHTML, /1.0 KB/);
  assert.equal(modal.stat.innerHTML, '');
  await app.checkFiles(11, modal.root);
  assert.match(modal.stat.innerHTML, /1.0 KB/);
  app.authorizedItemFetch = async (id, url) => {
    assert.equal(id, 11);
    assert.equal(url, '/api/v1/files/hash?item_id=11&file_index=0');
    return { ok: true, json: async () => ({ ok: true, md5: 'abc', sha256: 'def' }) };
  };
  await app.doFileHash(11, 0, modal.button);
  assert.match(modal.hash.innerHTML, /abc/);
  assert.match(modal.hash.innerHTML, /def/);
  assert.equal(card.hash.innerHTML, '');
  assert.equal(modal.button.disabled, false);
});

test('a background check does not reenable an in-flight file action', async () => {
  const surface = fileSurface();
  app.renderFileList({ id: 12 }, ['C:\\report.pdf']);
  let finish, requests = 0;
  app.authorizedItemFetch = async (id, url, init) => {
    requests++;
    assert.equal(id, 12);
    assert.equal(url, '/api/v1/files/open-directory?item_id=12&file_index=0');
    assert.equal(init.method, 'POST');
    return new Promise(resolve => { finish = resolve; });
  };
  const pending = app.doOpenDir(12, 0, surface.button);
  assert.equal(surface.button.disabled, true);
  app.fetch = async () => ({ ok: true, json: async () => ({ exists: true, size: 1 }) });
  await app.checkFiles(12, surface.root);
  assert.equal(surface.button.disabled, true);
  await app.doOpenDir(12, 0, surface.button);
  assert.equal(requests, 1);
  finish({ ok: true });
  await pending;
  assert.equal(surface.button.disabled, false);
});

for (const abort of [false, true]) {
  test(`file checks ignore ${abort ? 'aborted' : 'detached'} composite previews`, async () => {
    const surface = fileSurface();
    const controller = new AbortController();
    app.renderFileList({ id: 13 }, ['C:\\one.txt', 'C:\\two.txt']);
    let finish, requests = 0;
    app.fetch = async () => { requests++; return new Promise(resolve => { finish = resolve; }); };
    const pending = app.checkFiles(13, surface.root, { signal: controller.signal });
    if (abort) controller.abort();
    else surface.row.isConnected = false;
    finish({ ok: true, json: async () => ({ exists: true, size: 1 }) });
    await pending;
    assert.equal(surface.stat.innerHTML, '');
    assert.equal(requests, 1);
  });
}

test('late hash response cannot write to a replaced preview', async () => {
  const surface = fileSurface();
  app.renderFileList({ id: 14 }, ['C:\\report.pdf']);
  let finish;
  app.authorizedItemFetch = () => new Promise(resolve => { finish = resolve; });
  const pending = app.doFileHash(14, 0, surface.button);
  surface.hash.isConnected = false;
  surface.hash.innerHTML = 'closed';
  finish({ ok: true, json: async () => ({ ok: true, md5: 'private', sha256: 'private' }) });
  await pending;
  assert.equal(surface.hash.innerHTML, 'closed');
});
