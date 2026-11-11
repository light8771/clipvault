import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = { fetch: globalThis.fetch, clipvaultI18n: { t: key => key } };
const { app } = await import('./app/app-context.js');
const { registerConfigEvents } = await import('./app/config-events.js');

test('OCR 草稿不触发通用自动保存，动态选区快捷键仍自动保存', () => {
  const listeners = new Map();
  const settings = { addEventListener: (type, callback) => listeners.set(type, callback) };
  globalThis.document = {
    getElementById: id => id === 'viewSettings' ? settings : null,
    querySelectorAll: () => []
  };
  const delays = [];
  app.autoSaveBound = false;
  app.scheduleConfigSave = delay => delays.push(delay);
  registerConfigEvents();
  app.setupAutoSave();

  const ocrInput = { closest: selector => selector === '#s-ocr' ? {} : null };
  for (const type of ['input', 'change']) listeners.get(type)({ target: ocrInput });
  assert.deepEqual(delays, [], 'OCR 独立保存前不能出现通用保存提示');

  const hotkeyInput = { closest: () => null };
  for (const type of ['input', 'change']) listeners.get(type)({ target: hotkeyInput });
  assert.deepEqual(delays, [800, 300], '动态快捷键控件仍需触发自动保存');
});
