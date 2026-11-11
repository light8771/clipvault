import { app } from './app-context.js';

const t = window.clipvaultI18n.t;
const actionLabels = { copy: 'selection.copy', favorite: 'selection.favorite', sensitive: 'selection.sensitive', 'favorite-sensitive': 'selection.favorite_sensitive' };
const actions = Object.keys(actionLabels);

export function validateSelectionBindings(bindings, otherBindings) {
  if (!Array.isArray(bindings) || bindings.length > 8) return false;
  const used = new Set(otherBindings.filter(h => h.enabled).map(h => `${h.modifiers}:${h.key}`));
  for (const h of bindings) {
    if (!actions.includes(h.action) || h.key === null) return false;
    if (!h.enabled) continue;
    if (otherBindings.some(other => other.enabled && other.modifiers === 2 && other.key === 67)) return false;
    const key = `${h.modifiers}:${h.key}`;
    if (!h.modifiers || !h.key || used.has(key)) return false;
    if (h.modifiers === 2 && [67, 88, 86].includes(h.key)) return false;
    used.add(key);
  }
  return true;
}

export function registerSelectionHotkeys() {
  let rendered = false;
  const host = document.getElementById('selectionHotkeyRows');
  const add = document.getElementById('selectionHotkeyAdd');

  function read() {
    return [...host.querySelectorAll('[data-selection-row]')].map(row => ({
      enabled: row.querySelector('[data-enabled]').checked,
      modifiers: [...row.querySelectorAll('[data-modifier]:checked')].reduce((n, el) => n | Number(el.value), 0),
      key: app.parseVkInput(row.querySelector('[data-key]').value),
      action: row.querySelector('[data-copy-action]').value
    }));
  }

  function append(h) {
    const row = document.createElement('div');
    row.className = 'field-row';
    row.dataset.selectionRow = '';
    row.innerHTML = `<div class="field-info"><label class="ar-toggle"><input type="checkbox" data-enabled><span>${app.esc(t('common.enabled_short'))}</span></label>
      <select class="field-input" data-copy-action aria-label="${app.esc(t('selection.action'))}">${actions.map(action => `<option value="${action}">${app.esc(t(actionLabels[action]))}</option>`).join('')}</select></div>
      <div class="u-inline-3"><div class="mod-group">${[['Ctrl', 2], ['Alt', 1], ['Shift', 4], ['Win', 8]].map(([name, value]) => `<label class="mod-btn"><input type="checkbox" data-modifier value="${value}">${name}</label>`).join('')}</div>
      <input type="text" class="field-input u-inline-5" data-key maxlength="5" placeholder="F8" aria-label="${app.esc(t('selection.key'))}">
      <button type="button" class="btn-export" data-remove>${app.esc(t('selection.remove'))}</button></div>`;
    row.querySelector('[data-enabled]').checked = h.enabled;
    row.querySelector('[data-copy-action]').value = h.action;
    row.querySelector('[data-key]').value = app.vkToDisplay(h.key);
    row.querySelectorAll('[data-modifier]').forEach(el => { el.checked = (h.modifiers & Number(el.value)) !== 0; });
    row.querySelector('[data-remove]').addEventListener('click', () => {
      row.remove();
      add.disabled = host.children.length >= 8;
      app.scheduleConfigSave(150);
    });
    host.append(row);
    add.disabled = host.children.length >= 8;
  }

  app.renderSelectionHotkeys = bindings => {
    if (!host || rendered) return;
    rendered = true;
    bindings.forEach(append);
  };
  app.collectSelectionHotkeys = others => {
    if (!host) return app.currentCfg.selectionHotkeys;
    const bindings = read();
    if (validateSelectionBindings(bindings, others)) return bindings;
    app.toast(t('selection.invalid'), 'err');
    return null;
  };
  add?.addEventListener('click', () => {
    if (host.children.length >= 8) return;
    append({ enabled: false, modifiers: 3, key: 0, action: 'favorite' });
    host.lastElementChild.querySelector('[data-key]').focus();
    app.scheduleConfigSave(150);
  });
}
