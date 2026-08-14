import assert from 'node:assert/strict';
import test from 'node:test';

import { delegatedActionTarget } from './app/action-target.js';

function actionElement({ overlay = false } = {}) {
  const element = {
    classList: { contains: name => overlay && name === 'modal-overlay' }
  };
  element.closest = () => element;
  return element;
}

test('delegatedActionTarget dispatches ordinary action elements', () => {
  const button = actionElement();
  assert.equal(delegatedActionTarget(button), button);
});

test('delegatedActionTarget dispatches a modal backdrop only when clicked directly', () => {
  const backdrop = actionElement({ overlay: true });
  const panelContent = { closest: () => backdrop };

  assert.equal(delegatedActionTarget(backdrop), backdrop);
  assert.equal(delegatedActionTarget(panelContent), null);
});

test('delegatedActionTarget ignores elements outside an action', () => {
  assert.equal(delegatedActionTarget({ closest: () => null }), null);
});
