import assert from 'node:assert/strict';
import test from 'node:test';

import { clipboardEventDetailModel, renderClipboardEventDetails } from './app/clipboard-event-details.js';

function translate(key) {
  const values = {
    'clipboard.event.clipboard_busy.title': 'Clipboard busy',
    'clipboard.event.clipboard_busy.description': 'Another application temporarily owns the clipboard.',
    'clipboard.event.unsupported_format.title': 'Unsupported format',
    'clipboard.event.unsupported_format.description': 'No supported clipboard representation was found.',
    'history.event_code': 'Event code',
    'history.event_occurrences': 'Occurrences',
    'history.event_first_occurred': 'First occurred',
    'history.event_last_occurred': 'Last occurred',
    'history.event_sequence': 'Sequence',
    'history.event_error_category': 'Error category',
    'history.event_technical_details': 'Technical details',
    'history.error_stage': 'Stage',
    'history.error_stage_open': 'Open clipboard',
    'history.error_attempts': 'Attempts',
    'history.error_formats': 'Detected formats',
    'history.error_formats_none': 'No formats detected',
    'history.source_clipboard_owner': 'Clipboard owner',
    'history.source_foreground': 'Foreground application',
    'history.source_unknown': 'Unknown source'
  };
  return values[key] || key;
}

test('capture event details use the structural event and source DTOs', () => {
  const first = '2026-09-03T01:00:00Z';
  const last = '2026-09-03T01:01:00Z';
  const model = clipboardEventDetailModel({
    event: {
      code: 'clipboard_busy',
      titleKey: 'clipboard.event.clipboard_busy.title',
      descriptionKey: 'clipboard.event.clipboard_busy.description',
      severity: 'warning',
      sequence: 42,
      stage: 'open',
      attempts: 0,
      errorCategory: 'clipboard_busy',
      occurrenceCount: 2,
      firstOccurredAt: first,
      lastOccurredAt: last,
      formats: [{ id: 13, name: 'CF_UNICODETEXT' }, { id: 49155, name: 'HTML Format' }]
    },
    source: { displayName: 'Editor', exeName: 'editor.exe', attribution: 'clipboard_owner' }
  }, translate);

  assert.equal(model.title, 'Clipboard busy ×2');
  assert.equal(model.description, 'Another application temporarily owns the clipboard.');
  assert.equal(model.severity, 'warning');
  assert.deepEqual(model.rows, [
    { label: 'Event code', value: 'clipboard_busy' },
    { label: 'Occurrences', value: '2' },
    { label: 'First occurred', value: new Date(first).toLocaleString() },
    { label: 'Last occurred', value: new Date(last).toLocaleString() },
    { label: 'Stage', value: 'Open clipboard' },
    { label: 'Attempts', value: '0' },
    { label: 'Sequence', value: '42' },
    { label: 'Error category', value: 'clipboard_busy' },
    { label: 'Detected formats', value: 'CF_UNICODETEXT (#13), HTML Format (#49155)' },
    { label: 'Clipboard owner', value: 'Editor' }
  ]);
});

test('empty format lists are stated explicitly', () => {
  const model = clipboardEventDetailModel({
    event: {
      code: 'unsupported_format',
      titleKey: 'clipboard.event.unsupported_format.title',
      descriptionKey: 'clipboard.event.unsupported_format.description',
      occurrenceCount: 1,
      attempts: 1,
      formats: []
    }
  }, translate);

  assert.ok(model.rows.some(row => row.label === 'Detected formats' && row.value === 'No formats detected'));
});

test('rendered details escape event and source values', () => {
  const escapeHTML = value => String(value).replaceAll('<', '&lt;').replaceAll('>', '&gt;');
  const html = renderClipboardEventDetails({
    event: {
      code: '<script>',
      titleKey: 'clipboard.event.unsupported_format.title',
      descriptionKey: 'clipboard.event.unsupported_format.description',
      occurrenceCount: 1,
      attempts: 1,
      formats: [{ id: 49155, name: '<img>' }]
    },
    source: { exeName: '<owner>', attribution: 'unknown' }
  }, translate, escapeHTML);

  assert.ok(html.includes('&lt;script&gt;'));
  assert.ok(html.includes('&lt;img&gt;'));
  assert.ok(html.includes('&lt;owner&gt;'));
  assert.ok(!html.includes('<script>'));
});

test('event source rendering follows visibility and resolved display name', () => {
  const item = {
    event: {
      code: 'unsupported_format',
      titleKey: 'clipboard.event.unsupported_format.title',
      descriptionKey: 'clipboard.event.unsupported_format.description',
      occurrenceCount: 1,
      attempts: 1,
      formats: []
    },
    source: { displayName: 'raw.exe', exeName: 'raw.exe', attribution: 'foreground' }
  };

  const aliased = clipboardEventDetailModel(item, translate, { sourceDisplayName: 'Friendly App' });
  assert.ok(aliased.rows.some(row => row.label === 'Foreground application' && row.value === 'Friendly App'));

  const hidden = clipboardEventDetailModel(item, translate, { showSource: false, sourceDisplayName: 'Friendly App' });
  assert.ok(!hidden.rows.some(row => row.label === 'Foreground application'));
});
