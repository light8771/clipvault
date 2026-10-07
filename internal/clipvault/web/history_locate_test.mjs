import assert from 'node:assert/strict';
import test from 'node:test';
globalThis.window = {fetch() {}, location: {origin: 'http://localhost'}, clipvaultI18n: {t: key => key}};
const element = {value: '', classList: {add() {}, remove() {}}};
globalThis.document = {querySelectorAll: () => [element], querySelector: () => element, getElementById: () => element};
const {app} = await import('./app/app-context.js');
const {registerHistoryLocate} = await import('./app/history-locate.js');
const deferred = () => { let resolve; const promise = new Promise(done => {resolve = done;}); return {promise, resolve}; };
function setup() {
  Object.assign(app, {
    currentSourceExes: ['editor.exe'], currentKind: 'image', currentSubtype: 'ext:png', currentGroupID: 8,
    filterDateFrom: '2026-01-01', filterDateTo: '2026-01-02', currentPage: 0,
    Q: {value: 'old query'}, PAGESIZE: {value: '50'}, searchRevision: 0, locateActive: false,
    searchController: new AbortController(),
    fetch: async () => ({ok: true, json: async () => ({position: 75})}),
    resetSubtypeFilter: () => {app.currentSubtype = '';}, renderSrcExeDropdown() {},
    loadSourceExesForHistory: async () => true,
    toast: () => assert.fail('unexpected toast'),
    search: async () => {}
  });
  registerHistoryLocate();
}
test('locate clears all filters and uses the global history page', async () => {
  setup();
  let searched = false;
  app.search = async options => {
    searched = true;
    assert.equal(options.locate, true);
    assert.deepEqual(app.currentSourceExes, []);
    assert.equal(app.currentKind, '');
    assert.equal(app.currentSubtype, '');
    assert.equal(app.currentGroupID, 0);
    assert.equal(app.filterDateFrom, '');
    assert.equal(app.filterDateTo, '');
    assert.equal(app.Q.value, '');
    assert.equal(app.currentPage, 1);
    assert.equal(app.pendingLocateId, 12);
  };
  await app.doLocate(12);
  assert.equal(searched, true);
  assert.equal(app.locateActive, false);
});
test('a cancelled position response cannot overwrite the new filter', async () => {
  setup();
  const pending = deferred();
  let signal;
  app.fetch = (_, options) => {signal = options.signal; return pending.promise;};
  app.search = () => assert.fail('cancelled locate searched');
  const locating = app.doLocate(12);
  app.cancelLocate();
  app.currentKind = 'video';
  pending.resolve({ok: true, json: async () => ({position: 75})});
  await locating;
  assert.equal(signal.aborted, true);
  assert.equal(app.currentKind, 'video');
  assert.equal(app.Q.value, 'old query');
});
test('cancellation during source loading prevents an obsolete page search', async () => {
  setup();
  const pending = deferred();
  const entered = deferred();
  app.loadSourceExesForHistory = () => {entered.resolve(); return pending.promise;};
  app.search = () => assert.fail('cancelled locate searched');
  const locating = app.doLocate(12);
  await entered.promise;
  app.cancelLocate();
  pending.resolve(true);
  await locating;
  assert.equal(app.locateActive, false);
  assert.equal(app.pendingLocateId, 0);
});
test('source failure releases locate state so subsequent refreshes work', async () => {
  setup();
  const messages = [];
  app.toast = message => messages.push(message);
  app.loadSourceExesForHistory = async () => {throw new Error('source failed');};
  await app.doLocate(12);
  assert.equal(app.locateActive, false);
  assert.equal(app.pendingLocateId, 0);
  assert.deepEqual(messages, ['source failed']);
});
test('only the newest locate request may select a page', async () => {
  setup();
  const first = deferred();
  const pages = [];
  app.fetch = url => url.endsWith('=1') ? first.promise : Promise.resolve({ok: true, json: async () => ({position: 120})});
  app.search = async () => pages.push(app.currentPage);
  const old = app.doLocate(1);
  await app.doLocate(2);
  first.resolve({ok: true, json: async () => ({position: 0})});
  await old;
  assert.deepEqual(pages, [2]);
});

test('locate integrates with the real list query without cancelling its own highlight', async () => {
  setup();
  const nodes = new Map();
  document.addEventListener = () => {};
  document.getElementById = id => {
    if (!nodes.has(id)) nodes.set(id, {value: '', addEventListener() {}, classList: {add() {}, remove() {}, toggle() {}}});
    return nodes.get(id);
  };
  app.TOG = {addEventListener() {}};
  app.esc = value => value;
  const {registerHistoryList} = await import('./app/history-list.js');
  registerHistoryList();
  app.PAGESIZE.value = '50';
  app.currentSourceExes = ['editor.exe'];
  app.currentKind = 'image';
  let rendered = false;
  app.renderSrcExeDropdown = () => {};
  app.updatePagination = () => {};
  app.render = (items, query) => {
    rendered = true;
    assert.deepEqual(items, [{id: 12}]);
    assert.equal(query, '');
    assert.equal(app.pendingLocateId, 12);
  };
  app.fetch = async raw => {
    const url = new URL(raw, 'http://localhost');
    if (url.pathname.endsWith('/position')) return {ok: true, json: async () => ({position: 75})};
    assert.equal(url.searchParams.get('offset'), '50');
    assert.equal(url.searchParams.has('source_exe'), false);
    assert.equal(url.searchParams.has('kind'), false);
    return {ok: true, json: async () => ({items: [{id: 12}], total: 100})};
  };
  await app.doLocate(12);
  assert.equal(rendered, true);
  assert.equal(app.locateActive, false);
});

test('list refresh after the last row disappears moves to the last valid page', async () => {
  setup();
  const nodes = new Map();
  document.addEventListener = () => {};
  document.getElementById = id => {
    if (!nodes.has(id)) nodes.set(id, {value: '', addEventListener() {}, classList: {add() {}, remove() {}, toggle() {}}});
    return nodes.get(id);
  };
  app.TOG = {addEventListener() {}};
  app.esc = value => value;
  const {registerHistoryList} = await import('./app/history-list.js');
  registerHistoryList();
  app.PAGESIZE.value = '50';
  app.currentSourceExes = [];
  app.currentKind = '__starred__';
  app.currentPage = 1;
  app.updatePagination = () => {};
  const offsets = [];
  let rendered = false;
  app.fetch = async raw => {
    const params = new URL(raw, 'http://localhost').searchParams;
    assert.equal(params.get('starred'), '1');
    offsets.push(params.get('offset'));
    return {ok: true, json: async () => ({total: 50, items: params.get('offset') === '0' ? [{id: 7}] : []})};
  };
  app.render = items => {rendered = true; assert.deepEqual(items, [{id: 7}]);};
  await app.search();
  assert.deepEqual(offsets, ['50', '0']);
  assert.equal(app.currentPage, 0);
  assert.equal(app.totalCount, 50);
  assert.equal(rendered, true);
});
