package clipvault

const recoveryHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>恢复数据 · ClipVault</title>
<link id="appearanceFavicon" rel="icon" href="/appearance/favicon">
<style>
:root {
  --primary:#0f766e;
  --primary-hover:#115e59;
  --primary-soft:#e8f5f3;
  --danger:#b42318;
  --danger-hover:#912018;
  --danger-soft:#fff3f1;
  --warning:#9a6700;
  --warning-soft:#fff8e6;
  --bg:#f4f7f7;
  --surface:#ffffff;
  --surface-soft:#f7f9f9;
  --border:#d8e0df;
  --border-strong:#bcc9c7;
  --text:#172624;
  --sub:#455b58;
  --muted:#6d817e;
  --shadow:0 10px 30px rgba(24,44,41,.08);
  font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",system-ui,sans-serif;
  color-scheme:light dark;
}
@media (prefers-color-scheme:dark) {
  :root {
    --primary:#2dd4bf;
    --primary-hover:#5eead4;
    --primary-soft:#123c38;
    --danger:#ff8a80;
    --danger-hover:#ffaaa2;
    --danger-soft:#3b211f;
    --warning:#f7c95c;
    --warning-soft:#342d19;
    --bg:#0b1114;
    --surface:#121a1d;
    --surface-soft:#0f1719;
    --border:#293639;
    --border-strong:#3a4b4e;
    --text:#edf5f4;
    --sub:#b3c4c2;
    --muted:#839795;
    --shadow:0 14px 36px rgba(0,0,0,.34);
  }
}
*,*::before,*::after{box-sizing:border-box}
html,body{margin:0;min-height:100%}
body{
  min-height:100dvh;
  color:var(--text);
  background:var(--bg);
  -webkit-font-smoothing:antialiased;
}
.appearance-background,.appearance-overlay{position:fixed;inset:0;pointer-events:none}
.appearance-background{z-index:0;overflow:hidden;background:var(--bg)}
.appearance-background img,.appearance-background video{display:none;width:100%;height:100%;object-fit:cover;transform-origin:center center}
.appearance-overlay{z-index:0;background:transparent}
.page{position:relative;z-index:1}
button,input{font:inherit}
button{cursor:pointer}
.page{width:min(900px,calc(100% - 32px));margin:0 auto;padding:24px 0 40px}
.topbar{height:46px;display:flex;align-items:center;justify-content:space-between;margin-bottom:18px}
.brand{display:flex;align-items:center;gap:10px;font-size:15px;font-weight:700}
.brand-mark{width:30px;height:30px;display:grid;place-items:center;border:1px solid var(--border);border-radius:6px;background:var(--surface);color:var(--primary)}
.brand-mark svg{width:16px;height:16px}
.brand img{width:20px;height:20px;object-fit:contain}
.local-status{display:flex;align-items:center;gap:7px;color:var(--muted);font-size:12px}
.local-status::before{content:"";width:7px;height:7px;border-radius:50%;background:#16a34a;box-shadow:0 0 0 3px rgba(22,163,74,.12)}
.shell{border:1px solid var(--border);border-radius:8px;background:var(--surface);box-shadow:var(--shadow);overflow:hidden}
.page-heading{padding:28px 32px 24px;border-bottom:1px solid var(--border)}
.title-row{display:flex;align-items:flex-start;gap:14px}
.title-icon{width:38px;height:38px;flex:0 0 38px;display:grid;place-items:center;border-radius:7px;background:var(--primary-soft);color:var(--primary)}
.title-icon svg{width:20px;height:20px}
h1{margin:0 0 7px;font-size:23px;line-height:1.3;letter-spacing:0}
.lead{max-width:680px;margin:0;color:var(--sub);font-size:13px;line-height:1.65}
.workspace{display:grid;grid-template-columns:260px minmax(0,1fr);min-height:420px}
.intro{padding:28px 24px;border-right:1px solid var(--border);background:var(--surface-soft)}
.section-label{margin:0 0 16px;color:var(--muted);font-size:11px;font-weight:700;text-transform:uppercase}
.steps{display:grid;gap:0;margin:0;padding:0;list-style:none;counter-reset:recovery-step}
.steps li{position:relative;min-height:62px;padding:1px 0 20px 34px;color:var(--sub);font-size:12px;line-height:1.5;counter-increment:recovery-step}
.steps li:not(:last-child)::after{content:"";position:absolute;left:10px;top:24px;bottom:0;width:1px;background:var(--border)}
.steps li::before{content:counter(recovery-step);position:absolute;left:0;top:0;width:21px;height:21px;border-radius:50%;display:grid;place-items:center;background:var(--surface);border:1px solid var(--border-strong);color:var(--sub);font-size:10px;font-weight:700}
.privacy-note{display:flex;gap:8px;margin:12px 0 0;padding-top:16px;border-top:1px solid var(--border);color:var(--muted);font-size:11px;line-height:1.55}
.privacy-note svg{width:14px;height:14px;flex:0 0 14px;margin-top:1px}
.panel{padding:30px 32px}
.panel-title{margin:0 0 6px;font-size:17px;letter-spacing:0}
.panel-desc{margin:0 0 22px;color:var(--sub);font-size:12px;line-height:1.6}
.message{display:none;margin:0 0 18px;padding:11px 12px;border:1px solid transparent;border-radius:6px;font-size:12px;line-height:1.55}
.message.info,.message.error,.message.success{display:block}
.message.info{background:var(--primary-soft);border-color:var(--border);color:var(--primary)}
.message.error{background:var(--danger-soft);border-color:color-mix(in srgb,var(--danger) 25%,transparent);color:var(--danger)}
.message.success{background:var(--primary-soft);border-color:color-mix(in srgb,var(--primary) 28%,transparent);color:var(--primary)}
.field-label{display:flex;justify-content:space-between;align-items:center;margin-bottom:7px;color:var(--text);font-size:12px;font-weight:650}
.input-wrap{position:relative}
.field-input{width:100%;height:42px;padding:0 76px 0 12px;border:1px solid var(--border-strong);border-radius:6px;background:var(--surface);color:var(--text);outline:none;transition:border-color .15s,box-shadow .15s}
.field-input::placeholder{color:var(--muted)}
.field-input:focus{border-color:var(--primary);box-shadow:0 0 0 3px color-mix(in srgb,var(--primary) 15%,transparent)}
.show-password{position:absolute;right:5px;top:5px;height:32px;padding:0 9px;border:0;border-radius:5px;background:transparent;color:var(--sub);font-size:11px}
.show-password:hover{background:var(--primary-soft);color:var(--primary-hover)}
.hint{margin:7px 0 16px;color:var(--muted);font-size:11px;line-height:1.5}
.btn{min-height:40px;border:1px solid transparent;border-radius:6px;padding:9px 15px;font-size:12px;font-weight:700;transition:background .15s,border-color .15s,opacity .15s}
.btn:disabled{cursor:not-allowed;opacity:.48}
.btn-primary{width:100%;background:var(--primary);color:#fff}
.btn-primary:hover:not(:disabled){background:var(--primary-hover)}
.btn-danger{width:100%;background:var(--danger);border-color:var(--danger);color:#fff}
.btn-danger:hover:not(:disabled){background:var(--danger-hover);border-color:var(--danger-hover)}
.alternative{margin-top:24px;padding-top:18px;border-top:1px solid var(--border)}
.alternative summary{list-style:none;display:flex;align-items:center;justify-content:space-between;color:var(--sub);font-size:12px;font-weight:650;cursor:pointer;user-select:none}
.alternative summary::-webkit-details-marker{display:none}
.alternative summary::after{content:"+";width:22px;height:22px;display:grid;place-items:center;border:1px solid var(--border);border-radius:5px;color:var(--muted);font-size:15px;font-weight:400}
.alternative[open] summary::after{content:"−"}
.danger-box{margin-top:12px;padding:14px;border-left:3px solid var(--danger);background:var(--danger-soft)}
.danger-title{margin:0 0 6px;color:var(--danger);font-size:12px;font-weight:750}
.danger-copy{margin:0 0 13px;color:var(--sub);font-size:11px;line-height:1.6}
.confirm-input{padding-right:13px;margin-bottom:10px;background:var(--surface)}
.finish-note{display:none;margin-top:16px;color:var(--sub);font-size:11px;line-height:1.6;text-align:center}
.finish-note.visible{display:block}
button:focus-visible,input:focus-visible,summary:focus-visible{outline:3px solid color-mix(in srgb,var(--primary) 28%,transparent);outline-offset:2px}
@media (max-width:680px){
  .page{width:100%;padding:0}
  .topbar{height:52px;margin:0;padding:0 18px;border-bottom:1px solid var(--border);background:var(--surface)}
  .local-status{font-size:0}
  .shell{border:0;border-radius:0;box-shadow:none}
  .page-heading{padding:24px 20px 20px}
  .title-icon{width:34px;height:34px;flex-basis:34px}
  h1{font-size:20px}
  .workspace{grid-template-columns:1fr;min-height:0}
  .intro{padding:20px;border-right:0;border-bottom:1px solid var(--border)}
  .section-label{margin-bottom:12px}
  .steps{grid-template-columns:repeat(3,1fr);gap:10px}
  .steps li{min-height:0;padding:28px 4px 0;text-align:center;font-size:10px}
  .steps li::before{left:50%;transform:translateX(-50%)}
  .steps li:not(:last-child)::after{left:calc(50% + 13px);right:calc(-50% + 13px);top:10px;bottom:auto;width:auto;height:1px}
  .privacy-note{margin-top:18px}
  .panel{padding:24px 20px 32px}
}
@media (prefers-reduced-motion:reduce){*{scroll-behavior:auto!important;transition:none!important}}
</style>
</head>
<body>
<div class="appearance-background" aria-hidden="true">
  <img id="appearanceBackgroundImage" alt="">
  <video id="appearanceBackgroundVideo" muted loop playsinline></video>
</div>
<div id="appearanceOverlay" class="appearance-overlay" aria-hidden="true"></div>
<main class="page">
  <header class="topbar">
    <div class="brand">
      <span class="brand-mark">
        <svg id="recoveryDefaultLogo" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect x="9" y="3" width="6" height="4" rx="1"/><path d="M9 12h6M9 16h4"/></svg>
        <img id="recoveryBrandLogo" hidden alt="">
      </span>
      <span id="recoveryBrandName">ClipVault</span>
    </div>
    <div class="local-status">仅在本机处理</div>
  </header>

  <section class="shell" aria-labelledby="pageTitle">
    <div class="page-heading">
      <div class="title-row">
        <span class="title-icon">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l3 2"/></svg>
        </span>
        <div>
          <h1 id="pageTitle">恢复剪贴板数据库</h1>
          <p class="lead">当前 Windows 凭据无法解锁数据库。使用之前设置的恢复密码重新绑定，不会改变已有数据。</p>
        </div>
      </div>
    </div>

    <div class="workspace">
      <aside class="intro" aria-label="恢复流程">
        <p class="section-label">恢复流程</p>
        <ol class="steps">
          <li>输入数据恢复密码</li>
          <li>验证数据库密钥</li>
          <li>重新绑定到当前 Windows</li>
        </ol>
        <p class="privacy-note">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
          <span>恢复密码与数据库内容不会上传。恢复服务仅在本机运行，并会在 15 分钟后关闭。</span>
        </p>
      </aside>

      <div class="panel">
        <h2 class="panel-title">输入恢复密码</h2>
        <p class="panel-desc">这不是软件激活码，也不是锁屏密码，而是为数据库单独设置的数据恢复密码。</p>

        <div id="msg" class="message" role="status" aria-live="polite"></div>

        <div id="recoverSection">
          <label class="field-label" for="password">数据恢复密码</label>
          <div class="input-wrap">
            <input class="field-input" type="password" id="password" placeholder="请输入数据恢复密码" autocomplete="current-password" autofocus>
            <button class="show-password" id="showPasswordBtn" type="button" aria-controls="password" aria-pressed="false" onclick="togglePassword()">显示</button>
          </div>
          <p class="hint">验证失败不会修改或删除现有数据库。</p>
          <button class="btn btn-primary" id="recoverBtn" type="button" onclick="doRecover()">验证密码并恢复</button>
        </div>

        <details class="alternative" id="newDatabaseDetails">
          <summary>无法找回恢复密码</summary>
          <div class="danger-box">
            <p class="danger-title">创建新的空数据库</p>
            <p class="danger-copy">原数据库会被隔离保留，但在找回正确密码之前无法读取。新数据库不包含任何历史记录、收藏或设置。</p>
            <label class="field-label" for="confirmText">输入“创建新数据库”确认</label>
            <input class="field-input confirm-input" id="confirmText" type="text" autocomplete="off" placeholder="创建新数据库">
            <button class="btn btn-danger" id="newBtn" type="button" onclick="doNew()" disabled>隔离旧数据并创建新库</button>
          </div>
        </details>

        <p class="finish-note" id="finishNote">操作完成后，可以关闭此页面并返回 ClipVault。若创建了新数据库，之后可在“设置 → 存储与诊断 → 隔离的旧数据库”中切回查看旧数据。</p>
      </div>
    </div>
  </section>
</main>

<script>
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
    var resp = await fetch('/api/recovery/recover', {
      method:'POST',
      headers:secureHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({password:password})
    });
    var data = await responseJSON(resp);
    if (resp.ok && data.ok) {
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
    var resp = await fetch('/api/recovery/new', {
      method:'POST',
      headers:secureHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({confirmation:'CREATE NEW'})
    });
    var data = await responseJSON(resp);
    if (resp.ok && data.ok) {
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

document.getElementById('confirmText').addEventListener('input', function(event) {
  document.getElementById('newBtn').disabled = event.target.value.trim() !== '创建新数据库';
});
document.getElementById('password').addEventListener('keydown', function(event) {
  if (event.key === 'Enter') doRecover();
});
</script>
</body>
</html>`
