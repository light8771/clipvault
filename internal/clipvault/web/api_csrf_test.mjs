import assert from 'node:assert/strict';
import test from 'node:test';

const json = data => new Response(JSON.stringify({ data }), { status: 200 });

test('a failed CSRF bootstrap does not poison subsequent saves', async () => {
  for (const [name, failure] of [
    ['network', () => { throw new Error('offline'); }],
    ['http', () => new Response('', { status: 503 })],
    ['json', () => new Response('invalid JSON')],
    ['missing token', () => json({})],
  ]) {
    let attempts = 0;
    let writes = 0;
    globalThis.window = {
      location: { origin: 'http://localhost' },
      clipvaultI18n: { t: key => key },
      fetch: async (url, options) => {
        if (url === '/api/v1/session/csrf') {
          attempts++;
          if (attempts === 1) return failure();
          return json({ token: 'recovered' });
        }
        writes++;
        assert.equal(options.headers.get('X-ClipVault-CSRF'), 'recovered');
        return json({ ok: true });
      },
    };
    const { apiFetch } = await import(`./api.js?csrf-recovery=${encodeURIComponent(name)}`);
    const first = await Promise.allSettled([
      apiFetch('/api/v1/config', { method: 'PUT' }),
      apiFetch('/api/v1/groups', { method: 'POST' }),
    ]);
    assert.deepEqual(first.map(r => r.status), ['rejected', 'rejected'], name);
    assert.equal(attempts, 1, name);
    assert.equal(writes, 0, name);
    assert.equal((await apiFetch('/api/v1/config', { method: 'PUT' })).ok, true, name);
    assert.equal(attempts, 2, name);
    assert.equal(writes, 1, name);
  }
});
