import { app } from './app-context.js';

// 破坏性操作的二次确认：首次点击进入确认态（按钮文案变更），超时自动复位；
// 确认态内再次点击才真正执行，执行中的错误以 toast 呈现。
export function armConfirm(button, confirmLabel, run) {
  if (button.dataset.armed === '1') {
    button.dataset.armed = '';
    button.classList.remove('armed');
    button.textContent = button.dataset.label;
    void run().catch(err => app.toast(err && err.message ? err.message : '操作失败', 'err'));
    return;
  }
  button.dataset.armed = '1';
  button.dataset.label = button.textContent;
  button.classList.add('armed');
  button.textContent = confirmLabel;
  setTimeout(() => {
    if (button.dataset.armed === '1') {
      button.dataset.armed = '';
      button.classList.remove('armed');
      button.textContent = button.dataset.label;
    }
  }, 3000);
}
