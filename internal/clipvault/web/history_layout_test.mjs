import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = {
  location: { origin: 'http://localhost' },
  fetch: async () => { throw new Error('unexpected fetch'); }
};
const properties = new Map();
globalThis.document = {
  documentElement: { dataset: {}, style: { setProperty: (key, value) => properties.set(key, value) } }
};
const { app } = await import('./app/app-context.js');
const { registerHistoryLayout } = await import('./app/history-layout.js');
registerHistoryLayout();

test('preview changes clear fixed height when disabled or unlimited, and can enable it again', () => {
  for (const [lines, fixed, expectedLines, expectedFixed] of [
    [3, true, 3, true], [1, true, 1, true], [20, true, 20, true],
    [3, false, 3, false], [0, true, 0, false], [4, true, 4, true],
    [-1, true, 0, false], [25, true, 20, true]
  ]) {
    app.applyHistoryPageLayout({ previewLines: lines, fixedPreview: fixed });
    assert.equal(app.previewLines, expectedLines);
    assert.equal(app.historyPageSettings.fixedPreview, expectedFixed);
    assert.equal(properties.get('--history-preview-height'), expectedFixed ? 'var(--preview-max-h)' : 'auto');
    assert.equal(properties.get('--preview-max-h'), expectedLines
      ? `calc(${expectedLines} * var(--history-preview-line-height))` : 'none');
  }
});
