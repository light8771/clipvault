import { app } from './app-context.js';

export function bindTranslationSave({ input, row, status, entry, languageCode }) {
  let saved = entry.customText || '';
  let desired = saved;
  let running = null;
  const normalize = value => value.trim() ? value : '';
  const setStatus = (text, kind = '') => {
    status.title = '';
    status.textContent = text;
    status.className = 'lang-entry-status' + (kind ? ' is-' + kind : '');
  };
  async function drain() {
    while (desired !== saved) {
      const value = desired;
      setStatus('…');
      try {
        const response = await app.protectedFetch('/api/v1/i18n/entries?lang=' + encodeURIComponent(languageCode) + '&key=' + encodeURIComponent(entry.key), {
          method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ value })
        });
        if (!response.ok) throw new Error((await app.readAPIError(response)).message);
        saved = value;
        entry.customText = value;
        row.classList.toggle('lang-entry-untranslated', !value);
        input.classList.toggle('lang-untranslated', !value);
        input.removeAttribute('aria-invalid');
        setStatus(normalize(input.value) === saved ? '✓' : '', 'saved');
      } catch (err) {
        const message = err.message || window.clipvaultI18n.t('common.network_error');
        app.toast(message, 'err');
        input.setAttribute('aria-invalid', 'true');
        setStatus('!', 'error');
        status.title = message;
        return;
      }
    }
  }
  input.addEventListener('blur', () => {
    desired = normalize(input.value);
    if (!running) running = drain().finally(() => { running = null; });
    return running;
  });
}
