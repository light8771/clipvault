import test from 'node:test';
import assert from 'node:assert/strict';
import { createStore } from './store.js';

test('store publishes immutable changed snapshots only', () => {
  const store = createStore({ count: 0 });
  const seen = [];
  const unsubscribe = store.subscribe(state => seen.push(state));
  store.update({ count: 0 });
  store.update(state => ({ count: state.count + 1 }));
  unsubscribe();
  store.update({ count: 2 });
  assert.equal(store.getState().count, 2);
  assert.equal(seen.length, 1);
  assert.equal(seen[0].count, 1);
  assert.ok(Object.isFrozen(seen[0]));
});
