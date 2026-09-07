import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = {
  fetch: async () => { throw new Error('unexpected native fetch'); },
  location: { origin: 'http://localhost' },
  clipvaultI18n: {
    t: key => key
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

registerHistoryActions();

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
