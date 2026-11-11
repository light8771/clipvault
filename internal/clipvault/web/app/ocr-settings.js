import { app } from './app-context.js';
const t = window.clipvaultI18n.t;

export function registerOCRSettings() {
  const section = document.getElementById('s-ocr');
  if (!section) return;
  const field = name => section.querySelector(`[name="ocr-${name}"]`);
  const button = name => section.querySelector(`.ocr-config-${name}`);
  const editor = section.querySelector('.ocr-profile-editor');
  const status = section.querySelector('[role="status"]');
  const result = section.querySelector('.ocr-test-result');
  let auto = null;
  const autoStatus = section.querySelector('.ocr-auto-status');
  const autoSave = section.querySelector('.ocr-auto-save');
  const autoConsent = section.querySelector('.ocr-auto-consent');
  let data = null, selected = '', dirty = false, busy = false;
  const profile = () => data?.profiles.find(p => p.id === selected);
  const controls = () => {
    const ready = data && !data.needsReset;
    field('auto-enabled').disabled = busy || !auto;
    field('auto-consent').disabled = busy || !auto;
    autoSave.disabled = busy || !auto;
    editor.disabled = busy || !ready;
    field('profile').disabled = busy || !ready;
    button('load').disabled = busy;
    button('new').disabled = busy || !ready;
    button('delete').disabled = busy || !profile();
    button('save').disabled = busy || !ready;
    button('test').disabled = busy || !ready;
    button('reset').hidden = !data?.needsReset;
    button('reset').disabled = busy;
  };
  const providerControls = () => {
    const provider = field('provider').value;
    const umi = provider === 'umi-ocr';
    field('location').disabled = provider !== 'chat-completions';
    field('model').disabled = umi;
    field('prompt').disabled = umi;
    field('tokens').disabled = umi;
    field('token-field').disabled = umi || provider === 'ollama';
    section.querySelector('.ocr-provider-hint').textContent = umi ? t('ocr.umi_hint') : provider === 'ollama' ? t('ocr.ollama_hint') : t('ocr.vision_hint');
  };
  const fill = config => {
    for (const [name, prop] of Object.entries({ provider: 'provider', location: 'location', base: 'baseUrl', path: 'requestPath', model: 'model', prompt: 'prompt', timeout: 'timeoutSeconds', tokens: 'maxTokens', 'token-field': 'tokenLimitField' })) field(name).value = config[prop];
    field('extra').value = JSON.stringify(config.extra || {}, null, 2);
    providerControls();
  };
  const show = () => {
    const p = profile();
    field('profile').value = selected;
    field('name').value = p?.name || '';
    field('key').value = ''; field('clear').checked = false;
    field('active').checked = p ? p.id === data.activeId : !data.profiles.length;
    field('active').disabled = !data.profiles.length || p?.id === data.activeId;
    fill(p?.config || data.presets.bailian);
    dirty = false; result.hidden = true; result.textContent = '';
    status.textContent = data.needsReset ? t('ocr.reset_required') : p ? (p.hasKey ? t('ocr.key_set') : t('ocr.key_missing')) : t('ocr.new_hint');
    controls();
  };
  const render = preferred => {
    const select = field('profile'); select.replaceChildren();
    for (const p of data.profiles) {
      const option = document.createElement('option'); option.value = p.id;
      option.textContent = p.name + (p.id === data.activeId ? ' · ' + t('ocr.default_connection') : '');
      select.appendChild(option);
    }
    const draft = document.createElement('option'); draft.value = ''; draft.textContent = t('ocr.new_connection'); select.appendChild(draft);
    selected = data.profiles.some(p => p.id === preferred) ? preferred : data.activeId || '';
    show();
  };
  const autoControls = () => {
    autoConsent.hidden = !field('auto-enabled').checked || auto?.connection?.location !== 'cloud';
  };
  const loadAuto = async () => {
    auto = null;
    const response = await app.protectedFetch('/api/v1/ocr/auto');
    await app.requireOK(response, t('common.action_failed'));
    auto = await response.json();
    field('auto-enabled').checked = auto.enabled;
    field('auto-consent').checked = false;
    section.querySelector('.ocr-auto-target').textContent = auto.connection ? t('ocr.auto_target') + ' ' + auto.connection.name + ' · ' + auto.connection.host : t('ocr.auto_no_connection');
    autoStatus.textContent = t(auto.effective ? 'ocr.auto_on' : auto.enabled ? 'ocr.auto_paused' : 'ocr.auto_off');
    autoControls();
  };
  const request = async (method, body) => {
    const response = await app.protectedFetch('/api/v1/ocr/config', { method, ...(body ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}) });
    await app.requireOK(response, t('common.action_failed'));
    const output = await response.json();
    await loadAuto();
    return output;
  };
  const perform = async action => {
    if (busy) return;
    busy = true; controls();
    try { await action(); }
    catch (error) { status.textContent = error.message || t('common.action_failed'); }
    finally { busy = false; controls(); }
  };
  field('auto-enabled').addEventListener('change', () => { autoControls(); autoStatus.textContent = t('ocr.auto_unsaved'); });
  autoSave.addEventListener('click', () => perform(async () => {
    try {
      const enabled = field('auto-enabled').checked;
      if (enabled && dirty) throw new Error(t('ocr.auto_save_connection'));
      if (enabled && !auto.connection) throw new Error(t('ocr.auto_no_connection'));
      if (enabled && auto.connection.location === 'cloud' && !field('auto-consent').checked) throw new Error(t('ocr.error.auto_consent'));
      autoStatus.textContent = t('common.loading');
      const response = await app.protectedFetch('/api/v1/ocr/auto', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled, consent: field('auto-consent').checked, fingerprint: auto.connection?.fingerprint || '', revision: auto.revision }) });
      await app.requireOK(response, t('common.action_failed'));
      await loadAuto();
    } catch (error) { autoStatus.textContent = error.message || t('common.action_failed'); }
  }));
  const discard = () => !dirty || window.confirm(t('ocr.discard_config'));
  app.loadOCRSettings = () => perform(async () => {
    status.textContent = t('common.loading');
    data = await request('GET');
    render(selected);
  });
  editor.addEventListener('input', () => { dirty = true; result.hidden = true; controls(); });
  editor.addEventListener('change', () => { dirty = true; result.hidden = true; controls(); });
  field('provider').addEventListener('change', () => {
    fill(data.presets[field('provider').value]);
    field('key').value = ''; field('clear').checked = !!profile()?.hasKey;
    status.textContent = t('ocr.provider_changed');
  });
  field('profile').addEventListener('change', () => {
    if (!discard()) { field('profile').value = selected; return; }
    selected = field('profile').value; show();
  });
  button('new').addEventListener('click', () => { if (discard()) { selected = ''; show(); } });
  button('load').addEventListener('click', () => { if (discard()) void app.loadOCRSettings(); });
  const collectDraft = () => {
    let extra;
    try { extra = JSON.parse(field('extra').value); } catch { throw new Error(t('ocr.invalid_extra')); }
    if (!extra || Array.isArray(extra) || typeof extra !== 'object') throw new Error(t('ocr.invalid_extra'));
    return {
      revision: data.revision, id: selected, name: field('name').value.trim(), makeActive: field('active').checked,
      provider: field('provider').value, location: field('location').value, baseUrl: field('base').value.trim(), requestPath: field('path').value.trim(), model: field('model').value.trim(),
      prompt: field('prompt').value, timeoutSeconds: Number(field('timeout').value), maxTokens: Number(field('tokens').value), tokenLimitField: field('token-field').value, extra,
      apiKey: field('key').value, clearKey: field('clear').checked
    };
  };
  button('save').addEventListener('click', () => perform(async () => {
    const previousIDs = data.profiles.map(p => p.id);
    data = await request('PUT', collectDraft());
    render(selected || data.profiles.find(p => !previousIDs.includes(p.id))?.id);
    status.textContent = t('ocr.config_saved');
  }));
  button('delete').addEventListener('click', () => {
    if (!window.confirm(t('ocr.delete_confirm'))) return;
    void perform(async () => { data = await request('DELETE', { id: selected, revision: data.revision }); render(); });
  });
  button('reset').addEventListener('click', () => {
    if (!window.confirm(t('ocr.reset_confirm'))) return;
    void perform(async () => { data = await request('POST', { action: 'reset', revision: data.revision }); render(); });
  });
  button('test').addEventListener('click', () => {
    const cloud = field('location').value === 'cloud';
    if (cloud && !window.confirm(t('ocr.test_cloud_confirm'))) return;
    void perform(async () => {
      const draft = collectDraft();
      status.textContent = t('ocr.running'); result.hidden = true;
      const response = await app.protectedFetch('/api/v1/ocr/test', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ draft, consent: cloud }) });
      await app.requireOK(response, t('common.action_failed'));
      const output = await response.json();
      result.textContent = output.empty ? t('ocr.empty') : output.text; result.hidden = false;
      status.textContent = t(dirty || !profile() ? 'ocr.test_draft_done' : 'ocr.test_done');
    });
  });
}
