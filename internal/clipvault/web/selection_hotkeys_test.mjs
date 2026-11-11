import assert from 'node:assert/strict';
import { test } from 'node:test';

globalThis.window = { fetch: globalThis.fetch, clipvaultI18n: { t: key => key } };
const { validateSelectionBindings } = await import('./app/selection-hotkeys.js');
const binding = { enabled: true, modifiers: 3, key: 70, action: 'favorite' };

test('选区快捷键检查动作、重复绑定及系统复制键冲突', () => {
  assert.equal(validateSelectionBindings([binding], []), true);
  assert.equal(validateSelectionBindings([binding, binding], []), false);
  assert.equal(validateSelectionBindings([binding], [binding]), false);
  for (const key of [67, 88, 86]) {
    assert.equal(validateSelectionBindings([{ ...binding, modifiers: 2, key }], []), false);
  }
  assert.equal(validateSelectionBindings([binding], [{ enabled: true, modifiers: 2, key: 67 }]), false);
  assert.equal(validateSelectionBindings([{ ...binding, modifiers: 0 }], []), false);
  assert.equal(validateSelectionBindings([{ ...binding, action: 'toggle' }], []), false);
  assert.equal(validateSelectionBindings([{ ...binding, key: null }], []), false);
  assert.equal(validateSelectionBindings(Array(9).fill(binding), []), false);
  assert.equal(validateSelectionBindings([{ ...binding, enabled: false }, binding], []), true);
});
