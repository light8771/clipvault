import assert from 'node:assert/strict';
import test from 'node:test';
globalThis.window = {clipvaultI18n: {t: key => key}};
globalThis.document = {activeElement: null};
globalThis.requestAnimationFrame = fn => fn();
const {trapModalFocus, createRemovableModal} = await import('./app/modal-controller.js');
function node() {
  return {hidden: false, offsetParent: {}, isConnected: true, getAttribute: () => null,
    focus() {document.activeElement = this;}};
}
function fixture(controls = []) {
  const panel = {...node(), setAttribute() {}, hasAttribute: () => true};
  const handlers = new Map();
  const overlay = {isConnected: true, removed: false,
    querySelector: selector => selector === '[data-modal-panel]' ? panel : null,
    querySelectorAll: () => controls,
    addEventListener: (type, fn) => handlers.set(type, fn),
    removeEventListener: type => handlers.delete(type),
    remove() {this.removed = true; this.isConnected = false;}
  };
  return {panel, handlers, overlay};
}
function tab(shiftKey = false) {
  return {key: 'Tab', shiftKey, prevented: false, preventDefault() {this.prevented = true;}};
}
test('busy modal focuses a programmatically focusable panel when all controls are disabled', () => {
  const {overlay, panel} = fixture();
  document.activeElement = node();
  const event = tab();
  trapModalFocus(overlay, event);
  assert.equal(event.prevented, true);
  assert.equal(panel.tabIndex, -1);
  assert.equal(document.activeElement, panel);
});
for (const reverse of [false, true]) {
  test(`focus returning from the panel or background enters the ${reverse ? 'last' : 'first'} control`, () => {
    const first = node(), last = node();
    const {overlay, panel} = fixture([first, last]);
    for (const origin of [panel, node()]) {
      document.activeElement = origin;
      const event = tab(reverse);
      trapModalFocus(overlay, event);
      assert.equal(event.prevented, true);
      assert.equal(document.activeElement, reverse ? last : first);
    }
  });
}
test('IME Escape preserves the dialog, regular Escape closes it without reaching parent handlers', () => {
  const trigger = node();
  document.activeElement = trigger;
  const {overlay, handlers} = fixture([node()]);
  createRemovableModal(overlay);
  const handler = handlers.get('keydown');
  handler({key: 'Escape', isComposing: true, preventDefault() {assert.fail('IME Escape intercepted');}});
  assert.equal(overlay.removed, false);
  let stopped = false;
  handler({key: 'Escape', preventDefault() {}, stopPropagation() {stopped = true;}});
  assert.equal(overlay.removed, true);
  assert.equal(stopped, true);
  assert.equal(document.activeElement, trigger);
});
test('busy dialog rejects closing and accepts Escape after the operation finishes', () => {
  const {overlay, handlers} = fixture([node()]);
  let busy = true;
  createRemovableModal(overlay, {canClose: () => !busy});
  const handler = handlers.get('keydown');
  const event = {key: 'Escape', preventDefault() {}, stopPropagation() {}};
  handler(event);
  assert.equal(overlay.removed, false);
  busy = false;
  handler(event);
  assert.equal(overlay.removed, true);
});
