const STAGE_LABEL_KEYS = Object.freeze({
  open: 'history.error_stage_open',
  enumerate: 'history.error_stage_enumerate',
  get_data: 'history.error_stage_get_data',
  lock_data: 'history.error_stage_lock_data',
  decode: 'history.error_stage_decode',
  close: 'history.error_stage_close',
  persist: 'history.error_stage_persist',
  classify: 'history.error_stage_classify',
  validate: 'history.error_stage_validate',
  sequence: 'history.error_stage_sequence',
  read: 'history.error_stage_get_data'
});

export function clipboardEventDetailModel(item, t, options = {}) {
  const event = item?.event || {};
  const source = item?.source || {};
  const locale = globalThis.window?.clipvaultI18n?.locale?.();
  const rows = [];
  const add = (labelKey, value) => {
    if (value !== undefined && value !== null && String(value).trim() !== '') {
      rows.push({ label: t(labelKey), value: String(value) });
    }
  };
  add('history.event_code', event.code);
  if (event.occurrenceCount > 1) add('history.event_occurrences', event.occurrenceCount);
  if (event.firstOccurredAt && event.occurrenceCount > 1) {
    add('history.event_first_occurred', new Date(event.firstOccurredAt).toLocaleString(locale));
  }
  if (event.lastOccurredAt && event.occurrenceCount > 1) {
    add('history.event_last_occurred', new Date(event.lastOccurredAt).toLocaleString(locale));
  }
  if (event.stage) add('history.error_stage', t(STAGE_LABEL_KEYS[event.stage] || event.stage));
  if (Number.isInteger(event.attempts)) add('history.error_attempts', event.attempts);
  if (event.sequence) add('history.event_sequence', event.sequence);
  if (event.errorCategory) add('history.event_error_category', event.errorCategory);
  const formats = Array.isArray(event.formats)
    ? event.formats.slice(0, 32).map(format => {
      if (format.name && format.id) return `${format.name} (#${format.id})`;
      return format.name || (format.id ? `#${format.id}` : '');
    }).filter(Boolean)
    : [];
  add('history.error_formats', formats.length ? formats.join(', ') : t('history.error_formats_none'));
  if (options.showSource !== false && source.exeName) {
    const sourceKey = source.attribution === 'clipboard_owner'
      ? 'history.source_clipboard_owner'
      : source.attribution === 'foreground'
        ? 'history.source_foreground'
        : 'history.source_unknown';
    add(sourceKey, options.sourceDisplayName || source.displayName || source.exeName);
  }
  return {
    title: `${t(event.titleKey || `clipboard.event.${event.code}.title`)}${event.occurrenceCount > 1 ? ` ×${event.occurrenceCount}` : ''}`,
    description: t(event.descriptionKey || `clipboard.event.${event.code}.description`),
    severity: event.severity || 'info',
    rows
  };
}

export function renderClipboardEventDetails(item, t, escapeHTML, options) {
  const detail = clipboardEventDetailModel(item, t, options);
  const rowsHTML = detail.rows.length
    ? `<dl class="event-detail-grid">${detail.rows.map(row => `<dt>${escapeHTML(row.label)}</dt><dd>${escapeHTML(row.value)}</dd>`).join('')}</dl>`
    : '';
  return `<div class="capture-event capture-event-${escapeHTML(detail.severity)}">
    <div class="event-only-title">${escapeHTML(detail.title)}</div>
    <p class="capture-event-description">${escapeHTML(detail.description)}</p>
    <details class="capture-event-technical"><summary>${escapeHTML(t('history.event_technical_details'))}</summary>${rowsHTML}</details>
  </div>`;
}
