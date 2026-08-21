let generatedTitleID = 0;

const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])'
].join(',');

function visibleFocusableElements(overlay) {
  return Array.from(overlay.querySelectorAll(focusableSelector)).filter(element => {
    return !element.hidden && element.getAttribute('aria-hidden') !== 'true' && element.offsetParent !== null;
  });
}

export function trapModalFocus(overlay, event) {
  if (event.key !== 'Tab') return;
  const focusable = visibleFocusableElements(overlay);
  const panel = overlay.querySelector('[data-modal-panel]');
  if (!focusable.length) {
    event.preventDefault();
    panel?.focus();
    return;
  }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

const t = window.clipvaultI18n.t;

function ensureDialogSemantics(panel) {
  panel.setAttribute('role', 'dialog');
  panel.setAttribute('aria-modal', 'true');
  if (panel.hasAttribute('aria-label') || panel.hasAttribute('aria-labelledby')) return;
  const title = panel.querySelector('.modal-title, h1, h2');
  if (!title) {
    panel.setAttribute('aria-label', t('common.dialog'));
    return;
  }
  if (!title.id) title.id = `generatedModalTitle${++generatedTitleID}`;
  panel.setAttribute('aria-labelledby', title.id);
}

export function createRemovableModal(overlay, options = {}) {
  const panel = overlay.querySelector('[data-modal-panel]');
  if (!panel) throw new Error('modal overlay requires a [data-modal-panel] element');
  ensureDialogSemantics(panel);

  const trigger = document.activeElement;
  let closed = false;

  const close = () => {
    if (closed || (options.canClose && !options.canClose())) return false;
    closed = true;
    overlay.removeEventListener('click', onClick);
    overlay.removeEventListener('keydown', onKeydown);
    overlay.remove();
    options.onClose?.();
    if (trigger?.isConnected && typeof trigger.focus === 'function') trigger.focus();
    return true;
  };

  const onClick = event => {
    if (event.target === overlay) close();
  };
  const onKeydown = event => {
    if (event.key === 'Escape') {
      event.preventDefault();
      close();
      return;
    }
    trapModalFocus(overlay, event);
  };

  overlay.addEventListener('click', onClick);
  overlay.addEventListener('keydown', onKeydown);
  requestAnimationFrame(() => {
    if (closed || !overlay.isConnected) return;
    const preferred = typeof options.initialFocus === 'function' ? options.initialFocus() : options.initialFocus;
    const target = preferred || overlay.querySelector('[autofocus]') || visibleFocusableElements(overlay)[0];
    if (target && typeof target.focus === 'function') {
      target.focus();
      return;
    }
    panel.tabIndex = -1;
    panel.focus();
  });

  return { close };
}
