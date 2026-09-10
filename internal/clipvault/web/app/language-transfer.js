import { app } from './app-context.js';
import { createOperationState } from './operation-state.js';

const t = (key, params) => window.clipvaultI18n.t(key, params);
const maxPackBytes = 64 * 1024 * 1024;
const maxRequestBytes = maxPackBytes + 64 * 1024;

export function addLanguagePackExport(language, row, actions) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'btn';
  button.textContent = t('i18n.pack.export');
  actions.appendChild(button);
  const operation = createOperationState(actions, row);
  button.addEventListener('click', async () => {
    if (!operation.begin(t('transfer.exporting'))) return;
    let ok = false;
    try {
      ok = await app.authedDownload('/api/v1/i18n/export?code=' + encodeURIComponent(language.code), 'clipvault-language-' + language.code + '.json');
    } finally {
      operation.finish(t(ok ? 'i18n.pack.exported' : 'transfer.export_retry'), !ok);
    }
  });
}

export function registerLanguagePackImport(getLanguages, reloadLanguages) {
  const form = document.getElementById('langPackForm');
  const fileInput = document.getElementById('langPackFile');
  const preview = document.getElementById('langPackPreview');
  const summary = document.getElementById('langPackSummary');
  const code = document.getElementById('langPackCode');
  const name = document.getElementById('langPackName');
  const nativeName = document.getElementById('langPackNative');
  const base = document.getElementById('langPackBase');
  const merge = document.getElementById('langPackMerge');
  const operation = createOperationState(form, form.parentElement);
  let pack = null;

  fileInput.addEventListener('change', async () => {
    if (!operation.begin(t('i18n.pack.reading'))) return;
    pack = null;
    preview.hidden = true;
    let failure = '';
    try {
      const file = fileInput.files?.[0];
      if (!file) return;
      if (file.size > maxPackBytes) throw new Error(t('i18n.pack.too_large'));
      const parsed = JSON.parse(await file.text());
      if (!parsed || parsed.format !== 'clipvault-language' || parsed.version !== 1 || !parsed.language ||
          !parsed.translations || Array.isArray(parsed.translations) || typeof parsed.translations !== 'object' ||
          Object.values(parsed.translations).some(value => typeof value !== 'string')) {
        throw new Error(t('i18n.pack.invalid'));
      }
      const language = parsed.language;
      if (['code', 'name', 'nativeName', 'baseLanguageCode'].some(key => typeof language[key] !== 'string')) {
        throw new Error(t('i18n.pack.invalid'));
      }
      if (!getLanguages().length && !await reloadLanguages()) throw new Error(t('common.network_error'));
      pack = parsed;
      const builtin = getLanguages().some(item => item.isBuiltin && item.code === language.code);
      code.value = builtin ? '' : language.code;
      name.value = language.name;
      nativeName.value = language.nativeName;
      base.textContent = '';
      for (const language of getLanguages().filter(item => item.isBuiltin)) {
        const option = document.createElement('option');
        option.value = language.code;
        option.textContent = (language.nativeName || language.name) + ' · ' + language.code;
        base.appendChild(option);
      }
      base.value = language.baseLanguageCode;
      merge.checked = false;
      summary.textContent = t('i18n.pack.preview', { name: language.name, code: language.code, count: Object.keys(parsed.translations).length }) +
        (builtin ? ' ' + t('i18n.pack.builtin') : '');
      preview.hidden = false;
    } catch (err) {
      pack = null;
      fileInput.value = '';
      failure = err instanceof SyntaxError ? t('i18n.pack.invalid') : err.message;
    } finally {
      operation.finish(failure, !!failure);
    }
  });

  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (operation.busy || !pack || !form.reportValidity()) return;
    const language = { code: code.value.trim(), name: name.value.trim(), nativeName: nativeName.value.trim(), baseLanguageCode: base.value };
    const current = getLanguages().find(item => item.code === language.code);
    if (current?.isBuiltin) { operation.finish(t('i18n.pack.builtin'), true); return; }
    if (current && !merge.checked) { operation.finish(t('i18n.pack.exists'), true); return; }
    const body = JSON.stringify({ pack: { ...pack, language }, merge: merge.checked });
    if (new Blob([body]).size > maxRequestBytes) { operation.finish(t('i18n.pack.too_large'), true); return; }
    operation.begin(t('transfer.importing'));
    let message = '', failed = false;
    try {
      const response = await app.protectedFetch('/api/v1/i18n/import', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body });
      if (!response.ok) throw new Error((await app.readAPIError(response)).message);
      const result = await response.json();
      message = result.warning || t('i18n.pack.imported', { count: result.count });
      failed = !!result.warning;
      pack = null;
      form.reset();
      preview.hidden = true;
      if (!await reloadLanguages()) {
        message = [result.warning, t('i18n.pack.refresh_failed')].filter(Boolean).join(' ');
        failed = true;
      }
    } catch (err) {
      message = err.message || t('common.network_error');
      failed = true;
    } finally {
      operation.finish(message, failed);
    }
  });
}
