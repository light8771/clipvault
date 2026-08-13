var recoveryToken = location.hash.length > 1 ? location.hash.substring(1) : '';
if (recoveryToken) history.replaceState(null, '', location.pathname);

function applyRecoveryAppearance(appearance) {
  var brand = appearance.brand || {};
  var title = String(brand.title || 'ClipVault').trim() || 'ClipVault';
  document.title = '恢复数据 · ' + title;
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
  document.getElementById('recoverBtn').textContent = '恢复成功';
  document.getElementById('newDatabaseDetails').hidden = true;
  document.getElementById('finishNote').className = 'finish-note visible';
}

function togglePassword() {
  var input = document.getElementById('password');
  var button = document.getElementById('showPasswordBtn');
  var visible = input.type === 'text';
  input.type = visible ? 'password' : 'text';
  button.textContent = visible ? '显示' : '隐藏';
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
  setBusy(button, true, '正在验证并恢复…', '验证密码并恢复');
  showMsg('正在验证恢复密码，请稍候。', 'info');
  try {
    var resp = await fetch('/api/v1/recovery/unlock', {
      method:'POST',
      headers:secureHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({password:password})
    });
    var data = await responseJSON(resp);
    if (resp.ok && data.data) {
      finishFlow('数据恢复成功。ClipVault 正在继续启动。');
      return;
    }
    showMsg('恢复失败：密码不正确，或恢复文件已经损坏。请检查后重试。', 'error');
  } catch (_) {
    showMsg('暂时无法连接本机恢复服务。请重新从 ClipVault 打开恢复页面。', 'error');
  }
  setBusy(button, false, '正在验证并恢复…', '验证密码并恢复');
}

async function doNew() {
  var confirmInput = document.getElementById('confirmText');
  var button = document.getElementById('newBtn');
  if (confirmInput.value.trim() !== '创建新数据库') {
    showMsg('请输入“创建新数据库”后再继续。', 'error');
    confirmInput.focus();
    return;
  }
  setBusy(button, true, '正在创建新数据库…', '隔离旧数据并创建新库');
  showMsg('正在隔离旧数据并创建新的空数据库。', 'info');
  try {
    var resp = await fetch('/api/v1/recovery/reset', {
      method:'POST',
      headers:secureHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({confirmation:'CREATE NEW'})
    });
    var data = await responseJSON(resp);
    if (resp.ok && data.data) {
      finishFlow('新的空数据库已创建，原数据已隔离保存。');
      return;
    }
    showMsg('创建失败，原数据库没有被覆盖。请查看 ClipVault 日志。', 'error');
  } catch (_) {
    showMsg('暂时无法连接本机恢复服务。请重新从 ClipVault 打开恢复页面。', 'error');
  }
  setBusy(button, false, '正在创建新数据库…', '隔离旧数据并创建新库');
  button.disabled = confirmInput.value.trim() !== '创建新数据库';
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
  document.getElementById('newBtn').disabled = event.target.value.trim() !== '创建新数据库';
});
