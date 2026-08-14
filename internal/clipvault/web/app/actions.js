import { app } from "./app-context.js";
import { delegatedActionTarget } from "./action-target.js";

export function registerActions(pageActions) {
  app.fetch = app.apiFetch;
  const sharedActions = {
    'open-license-settings': () => app.openLicenseSettings(),
    'close-secondary': () => app.closeSecondaryModal(),
    'close-group-edit': () => app.closeGroupEditDialog(),
    'close-confirm': () => app.closeConfirmDialog()
  };
  app.actions = Object.assign(sharedActions, pageActions);
  document.addEventListener('click', event => {
    const target = delegatedActionTarget(event.target);
    if (!target) return;
    const action = app.actions[target.dataset.action];
    if (!action) return;
    event.preventDefault();
    action(target, event);
  });
  document.querySelectorAll('[data-credential-form]').forEach(form => {
    form.addEventListener('submit', event => event.preventDefault());
  });
}
