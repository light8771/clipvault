import { app } from './app-context.js';

const t = window.clipvaultI18n.t;

export function contentKindLabel(item, kindKey) {
  if (item.kind === 'composite') return (item.kinds || []).map(kind => t('history.kind_' + kind)).join(' + ');
  return app.KIND_LABEL[kindKey] ? t(app.KIND_LABEL[kindKey]) : kindKey;
}

export function compositeCard(item, label) {
  let bodyHTML = '<div class="composite-preview composite-card" data-composite-id="' + item.id + '" aria-label="' + app.esc(label) + '">' + app.esc(t('common.loading')) + '</div>';
  if (item.content?.issues?.length) bodyHTML += '<p class="composite-status">' + app.esc(t('history.composite_incomplete')) + '</p>';
  return { bodyHTML, charText: '', showView: true };
}

export function renderCompositeBody(body, content, itemId) {
  body.innerHTML = '';
  if (content.html) body.innerHTML = content.html;
  else body.textContent = content.text || (content.images?.length || content.files?.length ? '' : t('history.composite_preview_unavailable'));
  for (const src of content.images || []) {
    const image = document.createElement('img');
    image.src = src;
    image.alt = '';
    body.appendChild(image);
  }
  if (content.files?.length) {
    const files = document.createElement('div');
    files.className = 'composite-files';
    files.setAttribute('aria-label', t('history.file_count', { count: content.files.length }));
    files.innerHTML = app.renderFileList({ id: itemId }, content.files);
    body.appendChild(files);
  }
}

let cardObserver;
let cardRequests;
export function stopCompositeCards() {
  cardObserver?.disconnect();
  cardRequests?.abort();
  cardObserver = null;
  cardRequests = null;
}

export function loadCompositeCards() {
  stopCompositeCards();
  cardRequests = new AbortController();
  const signal = cardRequests.signal;
  const queue = [];
  const queued = new WeakSet();
  let active = 0;
  signal.addEventListener('abort', () => { queue.length = 0; }, { once: true });
  const load = async body => {
    if (signal.aborted || !body.isConnected) return;
    try {
      // Background previews must never open a secondary-auth prompt.
      const res = await app.fetch('/api/v1/items/composite-preview?id=' + body.dataset.compositeId, { signal });
      await app.requireOK(res, t('history.sensitive_load_failed'));
      const content = await res.json();
      if (!signal.aborted && body.isConnected) {
        const itemId = Number(body.dataset.compositeId);
        renderCompositeBody(body, content, itemId);
        if (content.files?.length) app.checkFiles(itemId, body, { signal });
      }
    } catch (_) {
      if (!signal.aborted && body.isConnected) body.textContent = t('history.composite_preview_unavailable');
    }
  };
  const pump = () => {
    while (!signal.aborted && active < 3 && queue.length) {
      const body = queue.shift();
      active++;
      load(body).finally(() => { active--; pump(); });
    }
  };
  const enqueue = body => {
    if (signal.aborted || queued.has(body)) return;
    queued.add(body);
    queue.push(body);
    pump();
  };
  const cards = app.LIST.querySelectorAll('[data-composite-id]');
  if (typeof IntersectionObserver === 'undefined') {
    cards.forEach(enqueue);
    return;
  }
  const observer = new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      observer.unobserve(entry.target);
      enqueue(entry.target);
    }
  }, { rootMargin: '200px' });
  cardObserver = observer;
  cards.forEach(card => observer.observe(card));
}

export async function showCompositePreview(id, isCurrent) {
  try {
    const res = await app.authorizedItemFetch(id, '/api/v1/items/composite-preview?id=' + id);
    await app.requireOK(res, t('history.sensitive_load_failed'));
    const content = await res.json();
    if (!isCurrent()) return;
    let preview = document.getElementById('modalFilePreview');
    if (!preview) {
      preview = document.createElement('div');
      preview.id = 'modalFilePreview';
      preview.className = 'modal-file-preview';
      app.VIEW_MODAL.querySelector('.modal-body').appendChild(preview);
    }
    preview.innerHTML = '';
    preview.style.display = 'block';
    const body = document.createElement('div');
    body.className = 'composite-preview';
    renderCompositeBody(body, content, id);
    preview.appendChild(body);
    if (content.files?.length) app.checkFiles(id, body);
    if (content.incomplete) {
      const note = document.createElement('p');
      note.textContent = t('history.composite_incomplete');
      preview.appendChild(note);
    }
    app.MODAL_PRE.style.display = 'none';
    app.MODAL_TITLE.textContent = t('history.kind_composite') + ' · #' + id;
    app.VIEW_MODAL.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
    requestAnimationFrame(() => {
      if (isCurrent()) app.VIEW_MODAL.querySelector('button[data-action="close-view-modal"]')?.focus();
    });
  } catch (_) {
    if (isCurrent()) app.toast(t('history.sensitive_load_failed'), 'err');
  }
}
