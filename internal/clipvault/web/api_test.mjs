import assert from 'node:assert/strict';
import test from 'node:test';

const responses = [];
globalThis.window = {
  fetch: async () => {
    const response = responses.shift();
    if (!response) throw new Error('unexpected network request');
    return response;
  },
  location: { origin: 'http://127.0.0.1:18765' },
};

const { apiFetch, readAPIError, requestWithSecondaryAuth, unwrapAPIEnvelope } = await import('./api.js');

test('API envelopes normalize success and error responses', () => {
  assert.deepEqual(
    unwrapAPIEnvelope({ error: { code: 'secondary_required', message: '需要二次认证' } }),
    { ok: false, code: 'secondary_required', error: '需要二次认证' },
  );
  assert.deepEqual(unwrapAPIEnvelope({ data: { ok: true } }), { ok: true });
	assert.throws(
		() => unwrapAPIEnvelope({ ok: true }),
		(error) => error.code === 'invalid_response',
	);
});

test('protected requests prompt again and retry only for the exact secondary error', async () => {
  responses.push(
    new Response(JSON.stringify({ data: { token: 'csrf-token' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    new Response(JSON.stringify({ error: { code: 'secondary_required', message: '需要二次认证' } }), { status: 403, headers: { 'Content-Type': 'application/json' } }),
  );
  let prompts = 0;
  let expired = 0;
  let sends = 0;
  const success = new Response(JSON.stringify({ data: { ok: true } }), { status: 200 });
  const response = await requestWithSecondaryAuth(
    async () => {
      sends++;
      return sends === 1
        ? apiFetch('/api/v1/config', { method: 'PUT', body: '{}' })
        : success;
    },
    async (forceFresh) => { prompts++; return prompts === 1 ? !forceFresh : forceFresh; },
    () => { expired++; },
  );
  assert.equal(response.status, 200);
  assert.equal(prompts, 2);
  assert.equal(expired, 1);
  assert.equal(sends, 2);
});

test('API errors are read from a clone without consuming the response body', async () => {
  const response = new Response(
    JSON.stringify({ error: { code: 'license_required', message: '需要授权' } }),
    { status: 402, headers: { 'Content-Type': 'application/json' } },
  );
  assert.deepEqual(await readAPIError(response), {
    code: 'license_required',
    message: '需要授权',
    status: 402,
  });
  assert.deepEqual(await response.json(), { error: { code: 'license_required', message: '需要授权' } });
});

test('unrelated forbidden responses are returned without reauthentication', async () => {
  let prompts = 0;
  const response = await requestWithSecondaryAuth(
    async () => new Response(JSON.stringify({ error: { code: 'forbidden', message: '禁止访问' } }), { status: 403 }),
    async () => { prompts++; return true; },
    () => { throw new Error('must not expire auth'); },
  );
  assert.equal(response.status, 403);
  assert.equal(prompts, 1);
});

test('fresh secondary requests retry fresh-secondary failures', async () => {
  let sends = 0;
  const forces = [];
  const response = await requestWithSecondaryAuth(
    async () => ++sends === 1
      ? new Response(JSON.stringify({ error: { code: 'fresh_secondary_required', message: '需要重新验证' } }), { status: 403 })
      : new Response(JSON.stringify({ data: { ok: true } }), { status: 200 }),
    async (forceFresh) => { forces.push(forceFresh); return true; },
    () => {},
    { fresh: true },
  );
  assert.equal(response.status, 200);
  assert.deepEqual(forces, [true, true]);
});
