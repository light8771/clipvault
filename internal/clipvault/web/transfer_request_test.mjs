import assert from 'node:assert/strict';
import test from 'node:test';
import { importRequestHeaders } from './app/transfer-request.js';

test('encrypted import headers preserve the exact UTF-8 export password', () => {
  for (const password of ['ascii-password', '中文🔐 password + / = é', ' leading and trailing ', 'line\nbreak']) {
    const headers = new Headers(importRequestHeaders('cvexport', password));
    const encoded = headers.get('X-ClipVault-Import-Password-Base64');
    assert.equal(Buffer.from(encoded, 'base64').toString('utf8'), password);
    assert.equal(headers.get('X-ClipVault-Import-Format'), 'cvexport');
    assert.equal(headers.has('X-ClipVault-Import-Password'), false);
  }
});

test('plain imports do not transmit a leftover password', () => {
  for (const format of ['json', 'xlsx']) {
    assert.deepEqual(importRequestHeaders(format, 'unused password'), { 'X-ClipVault-Import-Format': format });
  }
});
