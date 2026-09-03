const STAGE_LABEL_KEYS = Object.freeze({
  open: 'history.error_stage_open',
  enumerate: 'history.error_stage_enumerate',
  get_data: 'history.error_stage_get_data',
  lock_data: 'history.error_stage_lock_data',
  decode: 'history.error_stage_decode',
  close: 'history.error_stage_close',
  persist: 'history.error_stage_persist'
});

function parseMetadata(metaJson) {
  try {
    const value = JSON.parse(metaJson || '{}');
    return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
  } catch (_) {
    return {};
  }
}

function positiveInteger(value) {
  return Number.isInteger(value) && value > 0 ? value : 0;
}

export function clipboardEventDetailModel(item, t) {
  const meta = parseMetadata(item?.metaJson);
  const error = meta.error && typeof meta.error === 'object' && !Array.isArray(meta.error) ? meta.error : {};
  const rows = [];
  const add = (labelKey, value) => {
    if (value !== undefined && value !== null && String(value).trim() !== '') {
      rows.push({ label: t(labelKey), value: String(value) });
    }
  };

  if (error.stage) {
    const stageKey = STAGE_LABEL_KEYS[error.stage];
    add('history.error_stage', stageKey ? t(stageKey) : error.stage);
  }
  add('history.error_format', error.format);
  if (Number.isInteger(error.win32Error) && error.win32Error > 0) {
    add('history.error_win32', error.win32Error);
  }

  const readAttempt = positiveInteger(error.attempt);
  const openAttempts = positiveInteger(error.openAttempts);
  const attemptParts = [];
  if (readAttempt) attemptParts.push(t('history.error_read_attempt', { count: readAttempt }));
  if (openAttempts) attemptParts.push(t('history.error_open_attempts', { count: openAttempts }));
  add('history.error_attempts', attemptParts.join(' · '));

  const openerPID = positiveInteger(error.clipboardOpenerPid);
  const ownerPID = positiveInteger(error.clipboardOwnerPid);
  if (openerPID || ownerPID) {
    add('history.error_clipboard_process', t('history.error_pid', { pid: openerPID || ownerPID }));
  }
  if (Number.isInteger(error.durationMs) && error.durationMs >= 0) {
    add('history.error_duration', t('history.error_duration_ms', { count: error.durationMs }));
  }

  const formats = Array.isArray(meta.formats)
    ? meta.formats.filter(value => typeof value === 'string' && value.trim()).slice(0, 32)
    : [];
  if (formats.length) add('history.error_formats', formats.join(', '));
  else if (item?.unavailableReason === 'clipboard_empty') add('history.error_formats', t('history.error_formats_none'));

  return {
    message: typeof error.message === 'string' ? error.message : '',
    rows
  };
}

export function renderClipboardEventDetails(item, reasonLabel, t, escapeHTML) {
  const detail = clipboardEventDetailModel(item, t);
  const messageHTML = detail.message
    ? `<div class="event-error-message">${escapeHTML(detail.message)}</div>`
    : '';
  const rowsHTML = detail.rows.length
    ? `<dl class="event-detail-grid">${detail.rows.map(row => `<dt>${escapeHTML(row.label)}</dt><dd>${escapeHTML(row.value)}</dd>`).join('')}</dl>`
    : '';
  return `<div class="event-only"><div class="event-only-title">${escapeHTML(reasonLabel)}</div>${messageHTML}${rowsHTML}</div>`;
}
