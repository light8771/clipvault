import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

export function subtypeLabel(value) {
  if (value === 'plain') return t('history.subtype_plain');
  if (value === 'rich') return t('history.subtype_rich');
  if (value === 'embedded') return t('history.subtype_embedded');
  return value.slice(4).toUpperCase() || t('history.subtype_no_extension');
}

export function setupHistorySubtypes(filters, onSelect) {
  const kinds = ['text', 'image', 'video', 'archive', 'office', 'files'];
  const popup = document.createElement('div');
  popup.id = 'kindSubtypePop';
  popup.className = 'kind-subtype-pop';
  popup.setAttribute('role', 'dialog');
  popup.hidden = true;
  const search = document.createElement('input');
  search.type = 'search';
  search.className = 'kind-subtype-search';
  search.placeholder = t('history.subtype_search');
  search.setAttribute('aria-label', t('history.subtype_search'));
  const list = document.createElement('div');
  list.className = 'kind-subtype-list';
  popup.append(search, list);
  document.body.appendChild(popup);
  let trigger = null;
  let openKind = '';
  let revision = 0;
  let options = [];
  let requestController;
  const close = (focus = false) => {
    revision++;
    requestController?.abort();
    options = [];
    list.replaceChildren();
    search.value = '';
    popup.hidden = true;
    trigger?.setAttribute('aria-expanded', 'false');
    if (focus) trigger?.focus();
    trigger = null;
  };
  const position = () => {
    if (popup.hidden || !trigger) return;
    const rect = trigger.closest('.kind-split').getBoundingClientRect();
    if (rect.bottom <= 0 || rect.top >= window.innerHeight || rect.right <= 0 || rect.left >= window.innerWidth) {
      close();
      return;
    }
    const width = Math.min(264, window.innerWidth - 24);
    popup.style.width = width + 'px';
    popup.style.left = Math.max(12, Math.min(rect.left, window.innerWidth - width - 12)) + 'px';
    const below = window.innerHeight - rect.bottom - 12;
    const above = rect.top - 12;
    const height = Math.min(340, Math.max(below, above));
    popup.style.maxHeight = height + 'px';
    popup.style.top = (below >= Math.min(340, above) ? rect.bottom + 6 : Math.max(12, rect.top - height - 6)) + 'px';
  };
  const render = () => {
    list.replaceChildren();
    for (const value of ['', ...options]) {
      const label = value ? subtypeLabel(value) : t('common.all');
      if (value && !label.toLocaleLowerCase().includes(search.value.trim().toLocaleLowerCase())) continue;
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'kind-subtype-option';
      button.textContent = label;
      button.setAttribute('aria-pressed', String(app.currentKind === openKind && (app.currentSubtype || '') === value));
      button.addEventListener('click', () => onSelect(openKind, value));
      list.appendChild(button);
    }
  };
  search.addEventListener('input', render);
  kinds.forEach(kind => {
    const chip = filters.querySelector('[data-kind="' + kind + '"]');
    const wrapper = document.createElement('div');
    wrapper.className = 'kind-split';
    chip.before(wrapper);
    wrapper.appendChild(chip);
    const arrow = document.createElement('button');
    arrow.type = 'button';
    arrow.className = 'kind-subtype-toggle';
    arrow.setAttribute('aria-label', t('history.subtype_choose', { kind: chip.textContent.trim() }));
    arrow.setAttribute('aria-controls', popup.id);
    arrow.setAttribute('aria-haspopup', 'dialog');
    arrow.setAttribute('aria-expanded', 'false');
    arrow.innerHTML = '<svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true"><polyline points="6 9 12 15 18 9"/></svg>';
    wrapper.appendChild(arrow);
    arrow.addEventListener('click', async event => {
      event.stopPropagation();
      if (trigger === arrow && !popup.hidden) { close(true); return; }
      close();
      trigger = arrow;
      openKind = kind;
      popup.setAttribute('aria-label', arrow.getAttribute('aria-label'));
      arrow.setAttribute('aria-expanded', 'true');
      popup.hidden = false;
      search.value = '';
      search.disabled = true;
      list.textContent = t('common.loading');
      position();
      const request = ++revision;
      requestController = new AbortController();
      try {
        const response = await app.fetch('/api/v1/items/subtypes?kind=' + kind, { signal: requestController.signal });
        await app.requireOK(response, t('common.action_failed'));
        const data = await response.json();
        if (revision !== request) return;
        if (!Array.isArray(data) || data.some(value => typeof value !== 'string')) throw new Error('Invalid subtype options');
        options = data;
        search.disabled = false;
        render();
        if (!window.matchMedia('(max-width: 768px)').matches) search.focus();
      } catch (_) {
        if (revision === request) list.textContent = t('common.action_failed');
      }
    });
  });
  document.addEventListener('click', event => {
    if (!popup.hidden && !popup.contains(event.target)) close();
  });
  popup.addEventListener('keydown', event => {
    if (popup.hidden || event.isComposing) return;
    if (event.key === 'Escape') { event.preventDefault(); close(true); }
    else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      if (event.target === search && (event.key === 'Home' || event.key === 'End')) return;
      const buttons = [...list.querySelectorAll('button')];
      if (!buttons.length) return;
      event.preventDefault();
      const index = buttons.indexOf(document.activeElement);
      let next;
      if (event.key === 'Home') next = 0;
      else if (event.key === 'End') next = buttons.length - 1;
      else if (index < 0) next = event.key === 'ArrowDown' ? 0 : buttons.length - 1;
      else next = (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
      buttons[next].focus();
    }
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && !event.isComposing && !popup.hidden) close(true);
  });
  document.addEventListener('focusin', event => {
    if (!popup.hidden && !popup.contains(event.target) && event.target !== trigger) close();
  });
  window.addEventListener('resize', position);
  window.addEventListener('scroll', event => { if (!popup.contains(event.target)) position(); }, true);
  return { close };
}
