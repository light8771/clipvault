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
const { compositeCard, contentKindLabel, renderCompositeBody, loadCompositeCards, stopCompositeCards } = await import('./app/composite-content.js');
const { clearHistoryContent, showHistoryLoadError } = await import('./app/history-load-error.js');

registerTextUtils();
registerHistoryActions();

test('file download saves the authorized blob and releases its object URL', async () => {
  const originalFetch = app.authorizedItemFetch;
  const originalToast = app.toast;
  const originalCreate = document.createElement;
  const originalAppend = document.body.appendChild;
  const originalTimer = globalThis.setTimeout;
  const originalCreateURL = URL.createObjectURL;
  const originalRevokeURL = URL.revokeObjectURL;
  const blob = new Blob(['file contents']);
  const messages = [];
  let clicked = false, removed = false, release;
  const anchor = { style: {}, click() { clicked = true; }, remove() { removed = true; } };
  app.filePaths.set(92, ['C:\\files\\report.txt']);
  app.authorizedItemFetch = async (id, url) => {
    assert.equal(id, 92);
    assert.equal(url, '/api/v1/files/content?item_id=92&file_index=0&download=1');
    return { ok: true, blob: async () => blob };
  };
  app.toast = message => messages.push(message);
  document.createElement = tag => { assert.equal(tag, 'a'); return anchor; };
  document.body.appendChild = child => assert.equal(child, anchor);
  URL.createObjectURL = data => { assert.equal(data, blob); return 'blob:download'; };
  const revoked = [];
  URL.revokeObjectURL = url => revoked.push(url);
  globalThis.setTimeout = callback => { release = callback; };
  try {
    await app.doDownload(92, 0);
    assert.deepEqual(messages, ['history.download_started']);
    assert.equal(anchor.href, 'blob:download');
    assert.equal(anchor.download, 'report.txt');
    assert.equal(clicked, true);
    assert.equal(removed, true);
    release();
    assert.deepEqual(revoked, ['blob:download']);
  } finally {
    app.filePaths.delete(92);
    app.authorizedItemFetch = originalFetch;
    app.toast = originalToast;
    document.createElement = originalCreate;
    document.body.appendChild = originalAppend;
    globalThis.setTimeout = originalTimer;
    URL.createObjectURL = originalCreateURL;
    URL.revokeObjectURL = originalRevokeURL;
  }
});

test('composite copy restores all representations through the authorized endpoint', async () => {
  app.items.set(91, { id: 91, kind: 'composite', sensitive: true });
  app.authorizedItemFetch = async (id, url, init) => {
    assert.equal(id, 91);
    assert.equal(url, '/api/v1/items/restore?id=91');
    assert.equal(init.method, 'POST');
    return { ok: true };
  };
  app.requireOK = async response => assert.equal(response.ok, true);
  const messages = [];
  app.toast = message => messages.push(message);
  await app.copyItemToClipboard(91);
  assert.deepEqual(messages, ['history.copied_to_clipboard']);
});

test('composite cards escape text and expose all derived kinds', () => {
  const item = { kind: 'composite', kinds: ['image', 'text'], text: '<script>bad()</script>', content: { issues: [{}] } };
  assert.equal(contentKindLabel(item, 'composite'), 'history.kind_image + history.kind_text');
  const card = compositeCard(item, 'mixed');
  assert.doesNotMatch(card.bodyHTML, /<script>/);
  assert.doesNotMatch(card.bodyHTML, /bad\(\)/);
  assert.match(card.bodyHTML, /history.composite_incomplete/);
});

test('unified composite body uses the ordered document instead of repeating search text', () => {
  const body = { innerHTML: '', textContent: '', appendChild() { assert.fail('embedded images must stay in their document'); } };
  const html = '<p>before</p><img src="data:image/png;base64,AA=="><p>after</p>';
  renderCompositeBody(body, { html, text: 'before after\nbeforeafter', images: [] });
  assert.equal(body.innerHTML, html);
  assert.equal(body.textContent, '');
});

test('composite file lists remain visible with rich text and render paths as literal text', () => {
  const originalCreate = document.createElement;
  document.createElement = tag => ({ tag, children: [], setAttribute() {}, appendChild(child) { this.children.push(child); } });
  try {
    for (const html of ['', '<p>note</p>']) {
      const body = { innerHTML: '', textContent: '', children: [], appendChild(child) { this.children.push(child); } };
      const paths = ['C:\\<b>report</b>.txt', 'C:\\image.png'];
      renderCompositeBody(body, { html, text: '', files: paths }, 91);
      assert.equal(body.innerHTML, html);
      assert.equal(body.textContent, '');
      assert.equal(body.children[0].tag, 'div');
      const rendered = body.children[0].innerHTML;
      assert.match(rendered, /&lt;b&gt;report&lt;\/b&gt;/);
      assert.doesNotMatch(rendered, /<b>/);
      assert.match(rendered, /data-file-list-id="91"/);
      assert.match(rendered, /data-action="file-view" data-item-id="91" data-file-index="1"/);
      for (const action of ['file-download', 'file-open-directory', 'file-hash']) {
        assert.equal(rendered.split('data-action="' + action + '"').length - 1, 2);
      }
      assert.deepEqual(app.filePaths.get(91), paths);
      const registered = app.filePaths.get(91);
      app.renderFileList({ id: 91 }, [...paths]);
      assert.equal(app.filePaths.get(91), registered, 'opening the modal must not invalidate the card check');
    }
  } finally { document.createElement = originalCreate; }
});

test('inline composite loading aborts stale pages and never requests secondary authentication', async () => {
  const originalList = app.LIST;
  const originalFetch = app.fetch;
  let resolve;
  let requestSignal;
  const body = { dataset: { compositeId: '99' }, isConnected: true, innerHTML: '', textContent: '' };
  app.LIST = { querySelectorAll: () => [body] };
  app.fetch = (url, init) => {
    assert.equal(url, '/api/v1/items/composite-preview?id=99');
    requestSignal = init.signal;
    return new Promise(done => { resolve = done; });
  };
  try {
    loadCompositeCards();
    app.LIST = { querySelectorAll: () => [] };
    loadCompositeCards();
    assert.equal(requestSignal.aborted, true);
    resolve({ json: async () => ({ html: '<p>obsolete</p>' }) });
    await new Promise(setImmediate);
    assert.equal(body.innerHTML, '');
  } finally {
    app.LIST = originalList;
    app.fetch = originalFetch;
  }
});

test('search failure disconnects inline observers and discards already queued callbacks', () => {
  const originalList = app.LIST;
  const originalFetch = app.fetch;
  const originalRC = app.RC;
  const observers = [];
  globalThis.IntersectionObserver = class {
    constructor(callback) { this.callback = callback; observers.push(this); }
    observe() {}
    unobserve() {}
    disconnect() { this.disconnected = true; }
  };
  const body = { dataset: { compositeId: '100' }, isConnected: true };
  app.LIST = { querySelectorAll: () => [body] };
  app.RC = {};
  elements.set('pagination', { classList: { add() {} } });
  let requests = 0;
  app.fetch = () => { requests++; return new Promise(() => {}); };
  try {
    loadCompositeCards();
    showHistoryLoadError({ code: 'secondary_cancelled' });
    assert.equal(observers[0].disconnected, true);
    observers[0].callback([{ target: body, isIntersecting: true }]);
    assert.equal(requests, 0);
    assert.match(app.LIST.innerHTML, /role="alert"/);
  } finally {
    delete globalThis.IntersectionObserver;
    elements.delete('pagination');
    app.LIST = originalList;
    app.fetch = originalFetch;
    app.RC = originalRC;
  }
});

test('failed inline preview displays an explanation without opening authentication', async () => {
  const originalList = app.LIST;
  const originalFetch = app.fetch;
  const originalRequireOK = app.requireOK;
  const body = { dataset: { compositeId: '101' }, isConnected: true, textContent: 'loading' };
  app.LIST = { querySelectorAll: () => [body] };
  app.fetch = async () => ({ ok: false, status: 403 });
  app.requireOK = async r => { if (!r.ok) throw new Error('forbidden'); };
  try {
    loadCompositeCards();
    await new Promise(setImmediate);
    assert.equal(body.textContent, 'history.composite_preview_unavailable');
  } finally {
    app.LIST = originalList;
    app.fetch = originalFetch;
    app.requireOK = originalRequireOK;
  }
});

test('large composite pages bound concurrent downloads and discard queued cards on cancellation', async () => {
  const originalList = app.LIST;
  const originalFetch = app.fetch;
  const originalRequireOK = app.requireOK;
  const cards = Array.from({ length: 100 }, (_, id) => ({ dataset: { compositeId: String(id) }, isConnected: true, innerHTML: '', textContent: '' }));
  app.LIST = { querySelectorAll: () => cards };
  const requests = [];
  app.fetch = (url, { signal }) => new Promise(resolve => requests.push({ signal, resolve }));
  app.requireOK = async () => {};
  const response = () => ({ json: async () => ({ html: '<p>one document</p>', images: [] }) });
  try {
    loadCompositeCards();
    assert.equal(requests.length, 3);
    requests[0].resolve(response());
    await new Promise(setImmediate);
    assert.equal(requests.length, 4);
    assert.equal(cards[0].innerHTML, '<p>one document</p>');
    stopCompositeCards();
    assert.ok(requests.every(request => request.signal.aborted));
    requests.slice(1).forEach(request => request.resolve(response()));
    await new Promise(setImmediate);
    assert.equal(requests.length, 4, 'cancelled queue must not continue downloading');
    assert.ok(cards.slice(1).every(card => card.innerHTML === ''));
  } finally {
    stopCompositeCards();
    app.LIST = originalList;
    app.fetch = originalFetch;
    app.requireOK = originalRequireOK;
  }
});

test('repeated visibility notifications load each composite card once', async () => {
  const originalList = app.LIST;
  const originalFetch = app.fetch;
  let callback;
  globalThis.IntersectionObserver = class {
    constructor(fn) { callback = fn; }
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  const card = { dataset: { compositeId: '1' }, isConnected: true };
  app.LIST = { querySelectorAll: () => [card] };
  let requests = 0;
  app.fetch = async () => { requests++; return { json: async () => ({ text: 'one' }) }; };
  try {
    loadCompositeCards();
    callback([{ target: card, isIntersecting: false }]);
    assert.equal(requests, 0);
    callback([{ target: card, isIntersecting: true }]);
    callback([{ target: card, isIntersecting: true }]);
    await new Promise(setImmediate);
    assert.equal(requests, 1);
  } finally {
    stopCompositeCards();
    delete globalThis.IntersectionObserver;
    app.LIST = originalList;
    app.fetch = originalFetch;
  }
});

test('late composite content cannot reopen a closed preview', async () => {
  registerHistoryActions();
  let visible = false;
  modal.classList = { add: () => { visible = false; }, remove: () => { visible = true; } };
  app.items.set(92, { id: 92, kind: 'composite' });
  let resolve;
  app.authorizedItemFetch = () => new Promise(done => { resolve = done; });
  app.requireOK = async () => {};
  const pending = app.doView(92);
  app.closeModal();
  resolve({ json: async () => ({ html: '<p>late private content</p>' }) });
  await pending;
  assert.equal(visible, false);
  assert.equal(app.currentModalId, 0);
});

test('composite preview becomes visible after a previously hidden file preview', async () => {
  registerHistoryActions();
  const preview = { style: { display: 'none' }, innerHTML: '', appendChild() {} };
  elements.set('modalFilePreview', preview);
  const original = document.createElement;
  document.createElement = () => ({ appendChild() {}, style: {} });
  globalThis.requestAnimationFrame = () => {};
  app.items.set(93, { id: 93, kind: 'composite' });
  app.authorizedItemFetch = async () => ({ json: async () => ({ html: '<p>ready</p>', images: [], incomplete: true }) });
  app.requireOK = async () => {};
  try {
    await app.doView(93);
    assert.equal(preview.style.display, 'block');
    assert.equal(app.MODAL_PRE.style.display, 'none');
  } finally {
    document.createElement = original;
    elements.delete('modalFilePreview');
    delete globalThis.requestAnimationFrame;
  }
});

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

test('unrenderable composite content explains how to view it instead of showing a blank modal', async () => {
  registerHistoryActions();
  const preview = { style: {}, innerHTML: '', appendChild() {} };
  elements.set('modalFilePreview', preview);
  const original = document.createElement;
  const body = { appendChild() {}, style: {} };
  document.createElement = () => body;
  globalThis.requestAnimationFrame = () => {};
  app.items.set(94, { id: 94, kind: 'composite' });
  app.authorizedItemFetch = async () => ({ json: async () => ({ html: '', text: '', images: [] }) });
  app.requireOK = async () => {};
  try {
    await app.doView(94);
    assert.equal(body.textContent, 'history.composite_preview_unavailable');
  } finally {
    document.createElement = original;
    elements.delete('modalFilePreview');
    delete globalThis.requestAnimationFrame;
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

test('unstar in the favorites view refreshes membership and pagination', async () => {
  registerHistoryActions();
  app.items.set(50, {id: 50, starred: true});
  app.currentKind = '__starred__';
  app.authorizedItemFetch = async (_, url) => {
    assert.equal(url, '/api/v1/items?id=50&starred=0');
    return {ok: true};
  };
  let refreshed = 0;
  app.search = async () => {refreshed++; assert.equal(app.items.get(50).starred, false);};
  app.toast = () => {};
  const article = {dataset: {}};
  await app.doStar(50, {closest: () => article, classList: {toggle() {}}});
  assert.equal(refreshed, 1);
  assert.equal(article.dataset.starred, '0');
  app.currentKind = '';
});

test('failed favorite mutation leaves the item and current results unchanged', async () => {
  registerHistoryActions();
  app.items.set(50, {id: 50, starred: true});
  app.currentKind = '__starred__';
  app.authorizedItemFetch = async () => ({ok: false});
  app.search = () => assert.fail('failed mutation refreshed results');
  app.toast = () => {};
  await app.doStar(50, {});
  assert.equal(app.items.get(50).starred, true);
  app.currentKind = '';
});

test('search failure clears cached content and cancels an outstanding modal preview', async () => {
  registerHistoryActions();
  let visible = false;
  modal.classList = {add: () => {visible = false;}, remove: () => {visible = true;}};
  app.items.set(92, {id: 92, kind: 'composite'});
  app.filePaths.set(92, ['C:\\private.txt']);
  app.fileObjectURLs.add('blob:old-preview');
  app.pendingLocateId = 92;
  app.LIST = {};
  app.RC = {};
  elements.set('pagination', {classList: {add() {}}});
  let resolve;
  app.authorizedItemFetch = () => new Promise(done => {resolve = done;});
  app.requireOK = async () => {};
  const pending = app.doView(92);
  const oldRevoke = URL.revokeObjectURL;
  const revoked = [];
  URL.revokeObjectURL = url => revoked.push(url);
  try {
    showHistoryLoadError({code: 'secondary_cancelled'});
    assert.deepEqual(revoked, ['blob:old-preview']);
    assert.equal(app.items.size, 0);
    assert.equal(app.filePaths.size, 0);
    assert.equal(app.fileObjectURLs.size, 0);
    assert.equal(app.pendingLocateId, 0);
    resolve({json: async () => ({html: '<p>late private content</p>'})});
    await pending;
    assert.equal(visible, false);
    assert.equal(app.currentModalId, 0);
  } finally {
    URL.revokeObjectURL = oldRevoke;
    elements.delete('pagination');
  }
});

function fileCheckFixture(stat) {
  const buttons = Array.from({ length: 3 }, () => ({ disabled: true, dataset: {} }));
  const row = { isConnected: true, dataset: {}, querySelector: selector => selector === '.file-status' ? stat : null, querySelectorAll: () => buttons };
  const root = { querySelectorAll: () => [{ querySelector: () => row }] };
  return { root, buttons, row };
}

test('background file checks distinguish request failures from missing files without prompting', async () => {
  registerHistoryActions();
  const stat = {innerHTML: ''};
  const { root, buttons } = fileCheckFixture(stat);
  app.filePaths.set(90, ['C:\\file.txt']);
  app.authorizedItemFetch = () => assert.fail('background check requested interactive authorization');
    app.fetch = async () => ({ok: false, status: 403, json: async () => ({})});
    await app.checkFiles(90, root);
    assert.match(stat.innerHTML, /history.file_check_failed/);
    assert.ok(buttons.every(button => !button.disabled), 'explicit actions can obtain one-time authorization');
    app.fetch = async () => ({ok: true, json: async () => ({exists: false})});
    await app.checkFiles(90, root);
    assert.match(stat.innerHTML, /history.file_missing/);
    assert.ok(buttons.every(button => button.disabled));
});

for (const fails of [false, true]) {
  test(`obsolete file check ${fails ? 'failure' : 'success'} cannot touch the new card or continue scanning`, async () => {
    registerHistoryActions();
    const stat = {innerHTML: 'new card'};
    const { root } = fileCheckFixture(stat);
    app.filePaths.set(90, ['C:\\old.txt', 'C:\\another.txt']);
    let finish, reject, requests = 0;
    app.fetch = () => {requests++; return new Promise((resolve, fail) => {finish = resolve; reject = fail;});};
    const pending = app.checkFiles(90, root);
    app.filePaths.set(90, ['C:\\old.txt', 'C:\\another.txt']);
    if (fails) reject(new Error('offline'));
    else finish({ok: true, json: async () => ({exists: true, size: 12, name: 'old.txt'})});
      await pending;
      assert.equal(stat.innerHTML, 'new card');
      assert.equal(requests, 1);
  });
}

test('background thumbnails never invoke interactive authentication', async () => {
  registerHistoryActions();
  app.authorizedItemFetch = () => assert.fail('thumbnail requested interactive authorization');
  const requests = [];
  app.fetch = async url => {requests.push(url); return {ok: false, status: 403};};
  const image = {isConnected: true, style: {}, closest: () => null};
  await app.loadManagedImage(image, 90, false);
  await app.loadFileImage(image, 90, 0, false);
  assert.deepEqual(requests, ['/api/v1/items/image?id=90', '/api/v1/files/content?item_id=90&file_index=0']);
  assert.equal(image.style.display, 'none');
  assert.equal(app.fileObjectURLs.size, 0);
});

test('revoking history access clears previews and cancels stale searches immediately', () => {
  registerHistoryActions();
  const originalRevoke = URL.revokeObjectURL;
  const controller = new AbortController();
  const revoked = [];
  URL.revokeObjectURL = url => revoked.push(url);
  app.searchController = controller;
  app.searchRevision = 5;
  app.LIST = { innerHTML: 'private card' };
  app.RC = { textContent: 'private count' };
  app.MODAL_PRE.textContent = 'private preview';
  app.currentModalId = 90;
  app.items.set(90, { sensitive: true });
  app.filePaths.set(90, ['private file']);
  app.fileObjectURLs.add('blob:private');
  elements.set('pagination', { classList: { add() {} } });
  try {
    clearHistoryContent();
    assert.equal(controller.signal.aborted, true);
    assert.equal(app.searchRevision, 6);
    assert.equal(app.LIST.innerHTML, '');
    assert.equal(app.RC.textContent, '');
    assert.equal(app.currentModalId, 0);
    assert.equal(app.MODAL_PRE.textContent, '');
    assert.equal(app.items.size, 0);
    assert.equal(app.filePaths.size, 0);
    assert.deepEqual(revoked, ['blob:private']);
  } finally {
    URL.revokeObjectURL = originalRevoke;
    elements.delete('pagination');
  }
});
