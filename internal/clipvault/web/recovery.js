var recoveryToken = location.hash.length > 1 ? location.hash.substring(1) : '';
if (recoveryToken) history.replaceState(null, '', location.pathname);

// 恢复页自带双语字典：恢复服务器运行在数据库无法解锁的场景，i18n 服务
// 不可用，无法走 /i18n-dict.js。按浏览器语言就地选择，与主界面词条同源
// 维护（键名对齐 i18n_seed_* 的 recovery_page.* 词条）。
var RECOVERY_TEXT = {
  'zh-CN': {
    title: '恢复数据 · ClipVault', local_status: '仅在本机处理',
    page_title: '恢复剪贴板数据库',
    page_lead: '当前 Windows 凭据无法解锁数据库。使用之前设置的恢复密码重新绑定，不会改变已有数据。',
    steps_aria: '恢复流程', steps_label: '恢复流程',
    step1: '输入数据恢复密码', step2: '验证数据库密钥', step3: '重新绑定到当前 Windows',
    privacy_note: '恢复密码与数据库内容不会上传。恢复服务仅在本机运行，并会在 15 分钟后关闭。',
    panel_title: '输入恢复密码',
    panel_desc: '这不是软件激活码，也不是锁屏密码，而是为数据库单独设置的数据恢复密码。',
    password_label: '数据恢复密码', password_placeholder: '请输入数据恢复密码',
    show: '显示', hide: '隐藏',
    panel_hint: '验证失败不会修改或删除现有数据库。',
    recover_btn: '验证密码并恢复', recover_busy: '正在验证并恢复…',
    recovering_hint: '正在验证恢复密码，请稍候。', recovered: '恢复成功',
    recover_ok: '数据恢复成功。ClipVault 正在继续启动。',
    recover_failed: '恢复失败：密码不正确，或恢复文件已经损坏。请检查后重试。',
    service_unreachable: '暂时无法连接本机恢复服务。请重新从 ClipVault 打开恢复页面。',
    alternative_summary: '无法找回恢复密码',
    danger_title: '创建新的空数据库',
    danger_copy: '原数据库会被隔离保留，但在找回正确密码之前无法读取。新数据库不包含任何历史记录、收藏或设置。',
    confirm_label: '输入“创建新数据库”确认', confirm_phrase: '创建新数据库',

    confirm_mismatch: '请输入“{phrase}”后再继续。',
    new_btn: '隔离旧数据并创建新库', creating_busy: '正在创建新数据库…',
    creating_hint: '正在隔离旧数据并创建新的空数据库。',
    new_ok: '新的空数据库已创建，原数据已隔离保存。',
    new_failed: '创建失败，原数据库没有被覆盖。请查看 ClipVault 日志。',
    finish_note: '操作完成后，可以关闭此页面并返回 ClipVault。若创建了新数据库，之后可在“设置 → 存储与诊断 → 隔离的旧数据库”中切回查看旧数据。'
  },
  'en-US': {
    title: 'Recover Data · ClipVault', local_status: 'Processed locally only',
    page_title: 'Recover the Clipboard Database',
    page_lead: 'The current Windows credentials cannot unlock the database. Rebind with the recovery password you set earlier; existing data stays unchanged.',
    steps_aria: 'Recovery steps', steps_label: 'Recovery Steps',
    step1: 'Enter the data recovery password', step2: 'Verify the database key', step3: 'Rebind to the current Windows',
    privacy_note: 'The recovery password and database content are never uploaded. The recovery service runs locally and closes after 15 minutes.',
    panel_title: 'Enter the Recovery Password',
    panel_desc: 'This is not the license code or the lock password — it is the data recovery password set specifically for the database.',
    password_label: 'Data recovery password', password_placeholder: 'Enter the data recovery password',
    show: 'Show', hide: 'Hide',
    panel_hint: 'A failed verification never modifies or deletes the existing database.',
    recover_btn: 'Verify & Recover', recover_busy: 'Verifying and recovering…',
    recovering_hint: 'Verifying the recovery password, please wait.', recovered: 'Recovered',
    recover_ok: 'Data recovered. ClipVault is continuing startup.',
    recover_failed: 'Recovery failed: wrong password, or the recovery file is damaged. Check and retry.',
    service_unreachable: 'Cannot reach the local recovery service. Reopen the recovery page from ClipVault.',
    alternative_summary: 'Cannot recall the recovery password',
    danger_title: 'Create a New Empty Database',
    danger_copy: 'The original database is kept isolated but unreadable until the correct password is found. The new database contains no history, stars, or settings.',
    confirm_label: 'Type "{phrase}" to confirm', confirm_phrase: 'CREATE NEW DATABASE',
    confirm_mismatch: 'Type "{phrase}" before continuing.',
    new_btn: 'Isolate Old Data & Create New', creating_busy: 'Creating the new database…',
    creating_hint: 'Isolating the old data and creating a new empty database.',
    new_ok: 'A new empty database was created; the original data is isolated.',
    new_failed: 'Creation failed; the original database was not overwritten. Check the ClipVault logs.',
    finish_note: 'Once done, close this page and return to ClipVault. If a new database was created, switch back to the old data later under Settings → Storage & Diagnostics → Isolated Old Databases.'
  }
};
var recoveryLang = (navigator.language || '').toLowerCase().indexOf('zh') === 0 ? 'zh-CN' : 'en-US';
var recoveryDict = RECOVERY_TEXT[recoveryLang];
var t = function(key, params) {
  var text = recoveryDict[key] || key;
  if (params) {
    Object.keys(params).forEach(function(name) {
      text = text.split('{' + name + '}').join(String(params[name]));
    });
  }
  return text;
};
// 启动时把 data-i18n / data-i18n-placeholder / data-i18n-aria 标注的
// 节点填充为当前语言文案，并同步 <html lang>。
(function applyRecoveryLanguage() {
  document.documentElement.lang = recoveryLang;
  document.querySelectorAll('[data-i18n]').forEach(function(el) {
    el.textContent = t(el.dataset.i18n);
  });
  document.querySelectorAll('[data-i18n-placeholder]').forEach(function(el) {
    el.placeholder = t(el.dataset.i18nPlaceholder);
  });
  document.querySelectorAll('[data-i18n-aria]').forEach(function(el) {
    el.setAttribute('aria-label', t(el.dataset.i18nAria));
  });
  document.title = t('title');
})();

function applyRecoveryAppearance(appearance) {
  var brand = appearance.brand || {};
  var title = String(brand.title || 'ClipVault').trim() || 'ClipVault';
  document.title = t('title_suffix') + ' · ' + title;
  document.getElementById('recoveryBrandName').textContent = title;
  var logo = document.getElementById('recoveryBrandLogo');
  var defaultLogo = document.getElementById('recoveryDefaultLogo');
  if (Number(brand.logoVersion) > 0) {
    logo.src = '/appearance/logo?v=' + encodeURIComponent(brand.logoVersion);
    logo.hidden = false;
    defaultLogo.hidden = true;
  } else {
    logo.removeAttribute('src');
    logo.hidden = true;
    defaultLogo.hidden = false;
  }
  var favicon = document.getElementById('appearanceFavicon');
  favicon.href = Number(brand.faviconVersion) > 0
    ? '/appearance/favicon?v=' + encodeURIComponent(brand.faviconVersion)
    : '/appearance/favicon';

  var background = appearance.background || {};
  if (!background.contentType || Number(background.version) <= 0) return;
  var image = document.getElementById('appearanceBackgroundImage');
  var video = document.getElementById('appearanceBackgroundVideo');
  var media = background.contentType.indexOf('video/') === 0 ? video : image;
  var other = media === video ? image : video;
  other.removeAttribute('src');
  other.style.display = 'none';
  if (other === video) other.pause();
  media.src = '/appearance/background?v=' + encodeURIComponent(background.version);
  media.style.display = 'block';
  media.style.opacity = String(Math.max(0, Math.min(100, Number(background.opacity) || 0)) / 100);
  media.style.filter = 'blur(' + Math.max(0, Number(background.blur) || 0) + 'px) brightness(' + (Math.max(40, Number(background.brightness) || 100) / 100) + ') contrast(' + (Math.max(40, Number(background.contrast) || 100) / 100) + ') saturate(' + (Math.max(0, Number(background.saturation) || 100) / 100) + ')';
  media.style.transform = 'scale(' + (Math.max(25, Math.min(200, Number(background.scale) || 100)) / 100) + ')';
  media.style.objectFit = ['cover', 'contain', 'fill'].indexOf(background.fit) >= 0 ? background.fit : 'cover';
  media.style.objectPosition = (Math.max(0, Math.min(100, Number(background.positionX) || 50))) + '% ' + (Math.max(0, Math.min(100, Number(background.positionY) || 50))) + '%';
  document.getElementById('appearanceOverlay').style.background = 'rgba(5, 15, 18, ' + (Math.max(0, Math.min(90, Number(background.overlay) || 0)) / 100) + ')';
  if (media === video) media.play().catch(function() {});
}

fetch('/appearance').then(function(resp) {
  return resp.ok ? resp.json() : null;
}).then(function(appearance) {
  if (appearance) applyRecoveryAppearance(appearance);
}).catch(function() {});

function secureHeaders(extra) {
  var headers = extra || {};
  headers['X-ClipVault-Recovery-Token'] = recoveryToken;
  headers['X-ClipVault-CSRF'] = recoveryToken;
  return headers;
}

function showMsg(text, type) {
  var el = document.getElementById('msg');
  el.textContent = text;
  el.className = 'message ' + type;
}

function setBusy(button, busy, busyText, normalText) {
  button.disabled = busy;
  button.textContent = busy ? busyText : normalText;
}

function finishFlow(message) {
  showMsg(message, 'success');
  document.getElementById('password').value = '';
  document.getElementById('password').disabled = true;
  document.getElementById('showPasswordBtn').disabled = true;
  document.getElementById('recoverBtn').disabled = true;
  document.getElementById('recoverBtn').textContent = t('recovered');
  document.getElementById('newDatabaseDetails').hidden = true;
  document.getElementById('finishNote').className = 'finish-note visible';
}

function togglePassword() {
  var input = document.getElementById('password');
  var button = document.getElementById('showPasswordBtn');
  var visible = input.type === 'text';
  input.type = visible ? 'password' : 'text';
  button.textContent = visible ? t('show') : t('hide');
  button.setAttribute('aria-pressed', visible ? 'false' : 'true');
  input.focus();
}

async function responseJSON(resp) {
  var raw = await resp.text();
  try { return JSON.parse(raw); } catch (_) { throw new Error('invalid_response'); }
}

async function doRecover() {
  var input = document.getElementById('password');
  var button = document.getElementById('recoverBtn');
  var password = input.value;
  setBusy(button, true, t('recovering'), t('recover_btn'));
  showMsg(t('recover_hint'), 'info');
  try {
    var resp = await fetch('/api/v1/recovery/unlock', {
      method:'POST',
      headers:secureHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({password:password})
    });
    var data = await responseJSON(resp);
    if (resp.ok && data.data) {
      finishFlow(t('recover_ok'));
      return;
    }
    showMsg(t('recover_failed'), 'error');
  } catch (_) {
    showMsg(t('service_unreachable'), 'error');
  }
  setBusy(button, false, t('recovering'), t('recover_btn'));
}

async function doNew() {
  var confirmInput = document.getElementById('confirmText');
  var button = document.getElementById('newBtn');
  // 安全确认口令按当前语言比对（词条与界面提示保持一致）。
  if (confirmInput.value.trim() !== t('confirm_phrase')) {
    showMsg(t('confirm_mismatch', { phrase: t('confirm_phrase') }), 'error');
    confirmInput.focus();
    return;
  }
  setBusy(button, true, t('creating'), t('new_btn'));
  showMsg(t('creating_hint'), 'info');
  try {
    var resp = await fetch('/api/v1/recovery/reset', {
      method:'POST',
      headers:secureHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({confirmation:'CREATE NEW'})
    });
    var data = await responseJSON(resp);
    if (resp.ok && data.data) {
      finishFlow(t('new_ok'));
      return;
    }
    showMsg(t('new_failed'), 'error');
  } catch (_) {
    showMsg(t('service_unreachable'), 'error');
  }
  setBusy(button, false, t('creating'), t('new_btn'));
  button.disabled = confirmInput.value.trim() !== t('confirm_phrase');
}

document.getElementById('showPasswordBtn').addEventListener('click', togglePassword);
document.getElementById('recoverSection').addEventListener('submit', function(event) {
  event.preventDefault();
  doRecover();
});
document.getElementById('newDatabaseForm').addEventListener('submit', function(event) {
  event.preventDefault();
  doNew();
});
document.getElementById('confirmText').addEventListener('input', function(event) {
  document.getElementById('newBtn').disabled = event.target.value.trim() !== t('confirm_phrase');
});

