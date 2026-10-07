// Shared feedback for file operations. A busy operation owns its controls
// until the response body is consumed, and duplicate submissions are ignored.
export function createOperationState(root, host = root) {
  const status = document.createElement('p');
  status.className = 'operation-status';
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  status.hidden = true;
  host.appendChild(status);
  let busy = false;
  let controls = [];
  return {
    get busy() { return busy; },
    begin(message) {
      if (busy) return false;
      busy = true;
      controls = Array.from(root.querySelectorAll('button, input, select, textarea')).map(el => [el, el.disabled]);
      if (controls.some(([el]) => el === document.activeElement)) {
        root.tabIndex = -1;
        root.focus();
      }
      controls.forEach(([el]) => { el.disabled = true; });
      root.setAttribute('aria-busy', 'true');
      status.textContent = message;
      status.className = 'operation-status is-running';
      status.hidden = false;
      return true;
    },
    finish(message = '', failed = false) {
      busy = false;
      controls.forEach(([el, disabled]) => { el.disabled = disabled; });
      controls = [];
      root.removeAttribute('aria-busy');
      status.textContent = message;
      status.className = 'operation-status' + (failed ? ' is-error' : '');
      status.hidden = !message;
    }
  };
}
