import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = {
  fetch: async () => { throw new Error('unexpected native fetch'); },
  location: { origin: 'http://localhost' },
  matchMedia: () => ({ matches: false }),
  clipvaultI18n: { t: key => key }
};
const chips = [{ classList: { remove() {} } }];
globalThis.document = {
  querySelectorAll: () => chips,
  getElementById: () => null,
  body: { appendChild() {}, style: {} }
};

const { app } = await import('./app/app-context.js');
const { registerGroups } = await import('./app/groups.js');

registerGroups();

test('group filter switches source choices back to the content projection before search', async () => {
  const order = [];
  app.currentGroupID = 0;
  app.GROUP_CHIP = { classList: { add() {} } };
  app.closeGroupModal = () => {};
  app.loadSourceExesForHistory = async recordType => {
    order.push(`sources:${recordType}`);
    return true;
  };
  app.resetSearch = () => order.push('search');

  await app.activateGroupFilter(42);

  assert.equal(app.currentKind, '__group__');
  assert.equal(app.currentGroupID, 42);
  assert.deepEqual(order, ['sources:content', 'search']);
});
