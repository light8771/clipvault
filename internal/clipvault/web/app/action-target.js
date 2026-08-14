export function delegatedActionTarget(origin) {
  const target = origin.closest('[data-action]');
  if (!target) return null;
  if (target.classList.contains('modal-overlay') && origin !== target) return null;
  return target;
}
