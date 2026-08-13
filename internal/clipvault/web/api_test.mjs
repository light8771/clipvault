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

const { apiFetch, handleSecondaryRequired, unwrapAPIEnvelope } = await import('./api.js');

test('API envelopes normalize success and error responses', () => {
  assert.deepEqual(
    unwrapAPIEnvelope({ error: { code: 'secondary_required', message: '需要二次认证' } }),
    { ok: false, code: 'secondary_required', error: '需要二次认证' },
  );
  assert.deepEqual(unwrapAPIEnvelope({ data: { ok: true } }), { ok: true });
  assert.deepEqual(unwrapAPIEnvelope({ ok: true }), { ok: true });
});

test('config 403 prompts for secondary auth through the decorated API response', async () => {
  responses.push(
    new Response(JSON.stringify({ data: { token: 'csrf-token' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    new Response(JSON.stringify({ error: { code: 'secondary_required', message: '需要二次认证' } }), { status: 403, headers: { 'Content-Type': 'application/json' } }),
  );
  const configResponse = await apiFetch('/api/v1/config', { method: 'PUT', body: '{}' });
  let prompts = 0;
  let expired = 0;
  const authResult = await handleSecondaryRequired(
    configResponse,
    async () => { prompts++; return true; },
    () => { expired++; },
  );
  assert.deepEqual(authResult, { required: true, retry: true, error: '需要二次认证' });
  assert.equal(prompts, 1);
  assert.equal(expired, 1);
});

test('unrelated API errors do not prompt for secondary auth', async () => {
  const result = await handleSecondaryRequired(
    { status: 400, json: async () => ({ ok: false, code: 'invalid_config', error: '配置无效' }) },
    async () => { throw new Error('must not prompt'); },
    () => { throw new Error('must not expire auth'); },
  );
  assert.deepEqual(result, { required: false, retry: false, error: '配置无效' });
});
