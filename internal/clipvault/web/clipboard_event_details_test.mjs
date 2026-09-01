import assert from 'node:assert/strict';
import test from 'node:test';

import { clipboardEventDetailModel, renderClipboardEventDetails } from './app/clipboard-event-details.js';

function translate(key, values = {}) {
  const templates = {
    'history.error_stage': 'Stage',
    'history.error_stage_open': 'Open clipboard',
    'history.error_format': 'Format',
    'history.error_win32': 'Win32 error',
    'history.error_attempts': 'Attempts',
    'history.error_read_attempt': 'read attempt {count}',
    'history.error_open_attempts': 'opened {count} times',
    'history.error_clipboard_process': 'Clipboard process',
    'history.error_pid': 'PID {pid}',
    'history.error_duration': 'Duration',
    'history.error_duration_ms': '{count} ms',
    'history.error_formats': 'Detected formats'
  };
  return Object.entries(values).reduce(
    (text, [name, value]) => text.replaceAll(`{${name}}`, String(value)),
    templates[key] || key
  );
}

test('read failures expose the persisted message and structured Win32 context', () => {
  const model = clipboardEventDetailModel({
    metaJson: JSON.stringify({
      formats: ['CF_DIB', 'CF_DIBV5'],
      error: {
        message: 'clipboard open failed for unknown: Access is denied.',
        stage: 'open',
        format: 'unknown',
        attempt: 1,
        openAttempts: 8,
        clipboardOpenerPid: 13700,
        durationMs: 1273,
        win32Error: 5
      }
    })
  }, translate);

  assert.equal(model.message, 'clipboard open failed for unknown: Access is denied.');
  assert.deepEqual(model.rows, [
    { label: 'Stage', value: 'Open clipboard' },
    { label: 'Format', value: 'unknown' },
    { label: 'Win32 error', value: '5' },
    { label: 'Attempts', value: 'read attempt 1 · opened 8 times' },
    { label: 'Clipboard process', value: 'PID 13700' },
    { label: 'Duration', value: '1273 ms' },
    { label: 'Detected formats', value: 'CF_DIB, CF_DIBV5' }
  ]);
});

test('unsupported formats are displayed even without an error object', () => {
  const model = clipboardEventDetailModel({
    metaJson: JSON.stringify({ formats: ['FileGroupDescriptorW', 'FileContents'] })
  }, translate);

  assert.equal(model.message, '');
  assert.deepEqual(model.rows, [
    { label: 'Detected formats', value: 'FileGroupDescriptorW, FileContents' }
  ]);
});

test('malformed metadata degrades to an empty detail model', () => {
  assert.deepEqual(clipboardEventDetailModel({ metaJson: '{' }, translate), {
    message: '',
    rows: []
  });
});

test('rendered details escape persisted messages and values', () => {
  const escapeHTML = value => String(value).replaceAll('<', '&lt;').replaceAll('>', '&gt;');
  const html = renderClipboardEventDetails({
    metaJson: JSON.stringify({ error: { message: '<script>', format: '<img>' } })
  }, '<reason>', translate, escapeHTML);

  assert.ok(html.includes('&lt;script&gt;'));
  assert.ok(html.includes('&lt;img&gt;'));
  assert.ok(html.includes('&lt;reason&gt;'));
  assert.ok(!html.includes('<script>'));
});
