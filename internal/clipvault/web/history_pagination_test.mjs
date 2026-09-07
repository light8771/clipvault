import assert from 'node:assert/strict';
import test from 'node:test';

import { clampHistoryPage } from './app/history-pagination.js';

test('history page clamps to the last page from the authoritative total', () => {
  assert.equal(clampHistoryPage(2, 100, 50), 1);
  assert.equal(clampHistoryPage(3, 0, 50), 0);
  assert.equal(clampHistoryPage(1, 51, 50), 1);
});

test('history page clamp normalizes invalid inputs', () => {
  assert.equal(clampHistoryPage(-1, 10, 50), 0);
  assert.equal(clampHistoryPage(1, Number.NaN, 0), 0);
});
