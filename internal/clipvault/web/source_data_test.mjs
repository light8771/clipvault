import assert from 'node:assert/strict';
import test from 'node:test';

globalThis.window = {
  fetch: async () => { throw new Error('unexpected native fetch'); },
  location: { origin: 'http://localhost' },
  matchMedia: () => ({ matches: false }),
  clipvaultI18n: { t: key => key }
};
globalThis.document = {};

const { app } = await import('./app/app-context.js');
const { registerSourceData } = await import('./app/source-data.js');

test('stale source response cannot overwrite the latest record projection', async () => {
  registerSourceData();
  const pending = new Map();
  app.fetch = url => new Promise(resolve => pending.set(url, resolve));
  app.renderSrcExeDropdown = () => {};

  const eventLoad = app.loadSourceExes('event');
  const contentLoad = app.loadSourceExes('content');
  pending.get('/api/v1/items/sources?record_type=content')({
    json: async () => [{ name: 'content.exe', count: 1 }]
  });
  assert.equal(await contentLoad, 'ok');
  pending.get('/api/v1/items/sources?record_type=event')({
    json: async () => [{ name: 'event.exe', count: 9 }]
  });
  assert.equal(await eventLoad, 'stale');

  assert.equal(app.sourceStatsRecordType, 'content');
  assert.deepEqual(app.allSourceExes, [{ name: 'content.exe', count: 1 }]);
});

test('cached projection invalidates an in-flight request for another projection', async () => {
  registerSourceData();
	app.authState = { secondaryAuthed: false };
  app.sourceStatsRecordType = 'content';
	app.sourceStatsSensitiveAccess = false;
  app.allSourceExes = [{ name: 'cached.exe', count: 2 }];
  let resolveEvent;
  app.fetch = () => new Promise(resolve => { resolveEvent = resolve; });
  app.renderSrcExeDropdown = () => {};

  const eventLoad = app.loadSourceExes('event');
  assert.equal(await app.loadSourceExesForHistory('content'), true);
  resolveEvent({ json: async () => [{ name: 'event.exe', count: 9 }] });
  assert.equal(await eventLoad, 'stale');

  assert.equal(app.sourceStatsRecordType, 'content');
  assert.deepEqual(app.allSourceExes, [{ name: 'cached.exe', count: 2 }]);
});

test('source cache is refreshed when secondary visibility changes', async () => {
  registerSourceData();
  app.authState = { secondaryAuthed: false };
  app.sourceStatsRecordType = 'content';
  app.sourceStatsSensitiveAccess = false;
  app.allSourceExes = [{ name: 'public.exe', count: 1 }];
  let requests = 0;
  app.fetch = async () => {
    requests++;
    return { json: async () => [{ name: 'private.exe', count: 1 }] };
  };
  app.renderSrcExeDropdown = () => {};

  app.authState.secondaryAuthed = true;
  assert.equal(await app.loadSourceExesForHistory('content'), true);
  assert.equal(requests, 1);
  assert.equal(app.sourceStatsSensitiveAccess, true);
  assert.deepEqual(app.allSourceExes, [{ name: 'private.exe', count: 1 }]);
});

test('failed source refresh preserves the last good cache and selections', async () => {
  registerSourceData();
  app.authState = { secondaryAuthed: false };
  app.sourceStatsRecordType = 'content';
  app.sourceStatsSensitiveAccess = false;
  app.allSourceExes = [{ name: 'kept.exe', count: 2 }];
  app.currentSourceExes = ['kept.exe'];
  app.renderSrcExeDropdown = () => {};
  app.fetch = async () => { throw new Error('offline'); };

  assert.equal(await app.loadSourceExesForHistory('event'), true);
  assert.equal(app.sourceStatsRecordType, 'content');
  assert.deepEqual(app.allSourceExes, [{ name: 'kept.exe', count: 2 }]);
  assert.deepEqual(app.currentSourceExes, ['kept.exe']);
});

test('failed refresh after sensitive access expires clears privileged source names', async () => {
  registerSourceData();
  app.authState = { secondaryAuthed: false };
  app.sourceStatsRecordType = 'content';
  app.sourceStatsSensitiveAccess = true;
  app.allSourceExes = [{ name: 'private.exe', count: 1 }];
  app.currentSourceExes = ['private.exe'];
  app.renderSrcExeDropdown = () => {};
  app.fetch = async () => { throw new Error('offline'); };

  assert.equal(await app.loadSourceExesForHistory('content'), true);
  assert.deepEqual(app.allSourceExes, []);
  assert.deepEqual(app.currentSourceExes, []);
});
