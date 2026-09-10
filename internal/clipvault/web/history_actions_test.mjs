import assert from 'node:assert/strict';
import test from 'node:test';

const translations = {};
globalThis.window = {
  fetch: async () => { throw new Error('unexpected native fetch'); },
  location: { origin: 'http://localhost' },
  clipvaultI18n: {
    t: key => translations[key] ?? key
  }
};

const modal = {
  addEventListener() {},
  classList: { add() {}, remove() {} },
  style: {}
};
const elements = new Map([
  ['viewModal', modal],
  ['modalPre', { style: {} }],
  ['modalTitle', {}]
]);
globalThis.document = {
  activeElement: null,
  body: { style: {} },
  getElementById: id => elements.get(id) || null,
  querySelector: () => null,
  createElement: () => ({ style: {}, click() {}, remove() {} })
};

const { app } = await import('./app/app-context.js');
const { registerHistoryActions } = await import('./app/history-actions.js');
const { registerTextUtils } = await import('./app/text-utils.js');

registerTextUtils();
registerHistoryActions();

test('file hash button uses and escapes the active translation', () => {
  try {
    translations['history.hash'] = '<custom hash>';
    registerHistoryActions();
    assert.ok(app.hashHTML.endsWith(' &lt;custom hash&gt;'));
  } finally {
    delete translations['history.hash'];
    registerHistoryActions();
  }
});

test('successful grouped-event deletion refreshes the server projection', async () => {
  const id = 17;
  const item = { id, recordType: 'event' };
  app.items.set(id, item);
  app.PAGESIZE = { value: '50' };
  app.totalCount = 3;
  app.currentPage = 0;
  app.authorizedItemFetch = async (gotID, url, init) => {
    assert.equal(gotID, id);
    assert.equal(url, '/api/v1/items?id=17');
    assert.equal(init.method, 'DELETE');
    return { ok: true };
  };
  let searches = 0;
  app.search = async () => { searches++; };
  const toasts = [];
  app.toast = (...args) => toasts.push(args);

  await app.doDelete(id);

  assert.equal(searches, 1);
  assert.deepEqual(toasts, [['history.deleted']]);
});

test('content deletion refreshes instead of guessing the new projection', async () => {
  const id = 18;
  app.items.clear();
  app.items.set(id, { id, recordType: 'content' });
  app.PAGESIZE = { value: '50' };
  app.totalCount = 101;
  app.currentPage = 2;
  app.authorizedItemFetch = async () => ({ ok: true });
  let searches = 0;
  app.search = async () => { searches++; };
  app.toast = () => {};

  await app.doDelete(id);

  assert.equal(searches, 1);
  assert.equal(app.totalCount, 101);
  assert.equal(app.currentPage, 2);
});

test('deleting an item absent from the current projection is a no-op', async () => {
  let requests = 0;
  app.authorizedItemFetch = async () => {
    requests++;
    return { ok: true };
  };

  await app.doDelete(999);

  assert.equal(requests, 0);
});

test('viewing a locked sensitive managed image dispatches after authorized format resolution', async () => {
  const id = 19;
  app.items.clear();
  app.items.set(id, {
    id,
    recordType: 'content',
    kind: 'image',
    format: '',
    text: 'history.sensitive_masked',
    sensitive: true
  });
  app.authorizedItemFetch = async (gotID, url) => {
    assert.equal(gotID, id);
    assert.equal(url, '/api/v1/items/sensitive/item?id=19');
    return {
      ok: true,
      json: async () => ({
        item: {
          id,
          recordType: 'content',
          kind: 'image',
          format: 'CLIPVAULT_IMAGE_FILE',
          text: 'images/2026/09/capture.png',
          sensitive: true
        }
      })
    };
  };
  let viewedImage = 0;
  app.doViewManagedImage = async gotID => {
    viewedImage++;
    assert.equal(gotID, id);
  };

  await app.doView(id);

  assert.equal(viewedImage, 1);
});

for (const action of ['new preview', 'close']) {
  test(`late sensitive preview cannot override ${action}`, async () => {
    registerHistoryActions();
    globalThis.requestAnimationFrame = fn => fn();
    let visible = false;
    modal.classList = { add: () => { visible = false; }, remove: () => { visible = true; } };
    app.toast = () => {};
    app.items.set(1, { id: 1, kind: 'text', sensitive: true, text: 'masked' });
    app.items.set(2, { id: 2, kind: 'text', sensitive: false, text: 'new preview' });
    let finish;
    app.authorizedItemFetch = () => new Promise(resolve => { finish = resolve; });
    const old = app.doView(1);
    if (action === 'close') app.closeModal(); else await app.doView(2);
    finish({ ok: true, json: async () => ({ item: { id: 1, kind: 'text', text: 'late sensitive text' } }) });
    await old;
    assert.equal(visible, action !== 'close');
    assert.notEqual(app.MODAL_PRE.textContent, 'late sensitive text');
    if (action === 'new preview') {
      assert.equal(app.MODAL_PRE.textContent, 'new preview');
      assert.equal(app.currentModalId, 2);
    }
  });
}

for (const kind of ['managed image', 'file image']) {
  test(`late ${kind} response cannot reopen a closed preview`, async () => {
    registerHistoryActions();
    let visible = false;
    modal.classList = { add: () => { visible = false; }, remove: () => { visible = true; } };
    const preview = { style: {}, innerHTML: '', querySelector: () => ({}) };
    elements.set('modalFilePreview', preview);
    app.items.set(30, { id: 30, kind: 'image', sensitive: false });
    app.filePaths.set(30, ['C:/image.png']);
    let finish;
    app.loadManagedImage = app.loadFileImage = () => new Promise(resolve => { finish = resolve; });
    const viewing = kind === 'managed image' ? app.doViewManagedImage(30) : app.doFileView(30, 0);
    app.closeModal();
    finish();
    await viewing;
    assert.equal(visible, false);
    assert.equal(app.currentModalId, 0);
    elements.delete('modalFilePreview');
  });
}

test('detached image responses do not create orphaned object URLs', async () => {
  registerHistoryActions();
  app.authorizedItemFetch = async () => ({ ok: true, blob: async () => new Blob(['image']) });
  const image = { isConnected: false, style: {}, closest: () => null };
  const original = URL.createObjectURL;
  let created = 0;
  URL.createObjectURL = () => { created++; return 'blob:test'; };
  try {
    await app.loadManagedImage(image, 30);
    await app.loadFileImage(image, 30, 0);
    assert.equal(created, 0);
    assert.equal(app.fileObjectURLs.size, 0);
  } finally { URL.createObjectURL = original; }
});
