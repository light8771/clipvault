import { app } from './app-context.js';

export function registerNotifications() {
  app.TC = document.getElementById('tc');
  app.toast = function toast(message, type = 'ok') {
    const element = document.createElement('div');
    element.className = 'tk ' + type;
    element.textContent = message;
    element.setAttribute('role', type === 'err' ? 'alert' : 'status');
    element.setAttribute('aria-live', type === 'err' ? 'assertive' : 'polite');
    app.TC.appendChild(element);
    requestAnimationFrame(() => element.classList.add('show'));
    const duration = type === 'err' ? 7000 : 3500;
    setTimeout(() => {
      element.classList.remove('show');
      setTimeout(() => element.remove(), 260);
    }, duration);
  };
}
