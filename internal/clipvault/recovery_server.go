package clipvault

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const recoveryHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>恢复数据 · ClipVault</title>
<style>
:root {
  --primary:#14b8a6;
  --primary-strong:#0d9488;
  --primary-soft:rgba(20,184,166,.11);
  --danger:#dc2626;
  --danger-soft:rgba(220,38,38,.08);
  --bg:#f0fdfa;
  --surface:#ffffff;
  --surface-soft:#f7fffd;
  --border:rgba(15,118,110,.16);
  --text:#102a27;
  --sub:#3d6863;
  --muted:#688d88;
  --shadow:0 24px 70px rgba(13,148,136,.13),0 3px 12px rgba(15,23,42,.06);
  font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",system-ui,sans-serif;
  color-scheme:light dark;
}
@media (prefers-color-scheme:dark) {
  :root {
    --bg:#070c14;
    --surface:#101820;
    --surface-soft:#0c151d;
    --border:rgba(45,212,191,.14);
    --text:#e8f6f3;
    --sub:#9ac9c4;
    --muted:#709f9a;
    --primary-soft:rgba(20,184,166,.13);
    --danger-soft:rgba(239,68,68,.1);
    --shadow:0 28px 80px rgba(0,0,0,.5),0 0 0 1px rgba(20,184,166,.05);
  }
}
*,*::before,*::after{box-sizing:border-box}
html,body{margin:0;min-height:100%}
body{
  min-height:100dvh;
  color:var(--text);
  background:
    radial-gradient(circle at 12% 10%,rgba(20,184,166,.15),transparent 34rem),
    radial-gradient(circle at 92% 90%,rgba(249,115,22,.08),transparent 28rem),
    var(--bg);
  -webkit-font-smoothing:antialiased;
}
button,input{font:inherit}
button{cursor:pointer}
.page{width:min(940px,calc(100% - 32px));margin:0 auto;padding:28px 0 44px}
.topbar{display:flex;align-items:center;justify-content:space-between;margin-bottom:22px}
.brand{font-size:17px;font-weight:750;letter-spacing:-.35px}
.local-badge{padding:6px 10px;border:1px solid var(--border);border-radius:999px;background:var(--surface);color:var(--sub);font-size:12px;box-shadow:0 1px 3px rgba(0,0,0,.04)}
.shell{display:grid;grid-template-columns:minmax(0,.88fr) minmax(360px,1.12fr);overflow:hidden;border:1px solid var(--border);border-radius:20px;background:var(--surface);box-shadow:var(--shadow)}
.intro{padding:48px 42px;background:linear-gradient(145deg,var(--primary-soft),transparent 70%);border-right:1px solid var(--border)}
.eyebrow{display:inline-flex;padding:6px 10px;margin-bottom:18px;border-radius:999px;background:var(--primary-soft);color:var(--primary-strong);font-size:12px;font-weight:700;letter-spacing:.04em}
h1{max-width:360px;margin:0 0 16px;font-size:clamp(28px,4vw,38px);line-height:1.16;letter-spacing:-1.2px}
.lead{margin:0;color:var(--sub);font-size:15px;line-height:1.8}
.steps{display:grid;gap:14px;margin:30px 0 0;padding:0;list-style:none;counter-reset:recovery-step}
.steps li{position:relative;padding-left:34px;color:var(--sub);font-size:13px;line-height:1.55;counter-increment:recovery-step}
.steps li::before{content:counter(recovery-step);position:absolute;left:0;top:0;width:22px;height:22px;border-radius:50%;display:grid;place-items:center;background:var(--surface);border:1px solid var(--border);color:var(--primary-strong);font-size:11px;font-weight:750}
.privacy-note{margin-top:30px;padding-top:20px;border-top:1px solid var(--border);color:var(--muted);font-size:12px;line-height:1.65}
.panel{padding:42px}
.panel-title{margin:0 0 8px;font-size:20px;letter-spacing:-.35px}
.panel-desc{margin:0 0 26px;color:var(--sub);font-size:13px;line-height:1.65}
.message{display:none;margin:0 0 18px;padding:12px 14px;border:1px solid transparent;border-radius:10px;font-size:13px;line-height:1.55}
.message.info,.message.error,.message.success{display:block}
.message.info{background:var(--primary-soft);border-color:var(--border);color:var(--primary-strong)}
.message.error{background:var(--danger-soft);border-color:rgba(220,38,38,.2);color:var(--danger)}
.message.success{background:var(--primary-soft);border-color:rgba(20,184,166,.25);color:var(--primary-strong)}
.field-label{display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;color:var(--text);font-size:13px;font-weight:650}
.input-wrap{position:relative}
.field-input{width:100%;height:46px;padding:0 82px 0 13px;border:1px solid var(--border);border-radius:10px;background:var(--surface-soft);color:var(--text);outline:none;transition:border-color .15s,box-shadow .15s,background .15s}
.field-input::placeholder{color:var(--muted)}
.field-input:focus{border-color:var(--primary);background:var(--surface);box-shadow:0 0 0 3px rgba(20,184,166,.12)}
.show-password{position:absolute;right:6px;top:6px;height:34px;padding:0 10px;border:0;border-radius:7px;background:transparent;color:var(--sub);font-size:12px}
.show-password:hover{background:var(--primary-soft);color:var(--primary-strong)}
.hint{margin:8px 0 18px;color:var(--muted);font-size:12px;line-height:1.55}
.btn{width:100%;min-height:46px;border:1px solid transparent;border-radius:10px;padding:11px 16px;font-weight:700;transition:transform .12s,background .15s,border-color .15s,opacity .15s}
.btn:active:not(:disabled){transform:translateY(1px)}
.btn:disabled{cursor:not-allowed;opacity:.48}
.btn-primary{background:var(--primary);color:#fff;box-shadow:0 7px 18px rgba(20,184,166,.2)}
.btn-primary:hover:not(:disabled){background:var(--primary-strong)}
.btn-danger{background:transparent;border-color:rgba(220,38,38,.3);color:var(--danger)}
.btn-danger:hover:not(:disabled){background:var(--danger);border-color:var(--danger);color:#fff}
.alternative{margin-top:28px;padding-top:22px;border-top:1px solid var(--border)}
.alternative summary{list-style:none;display:flex;align-items:center;justify-content:space-between;color:var(--sub);font-size:13px;font-weight:650;cursor:pointer;user-select:none}
.alternative summary::-webkit-details-marker{display:none}
.alternative summary::after{content:"展开";padding:3px 8px;border-radius:999px;background:var(--primary-soft);color:var(--primary-strong);font-size:11px;font-weight:650}
.alternative[open] summary::after{content:"收起"}
.danger-box{margin-top:14px;padding:16px;border:1px solid rgba(220,38,38,.18);border-radius:12px;background:var(--danger-soft)}
.danger-title{margin:0 0 7px;color:var(--danger);font-size:13px;font-weight:750}
.danger-copy{margin:0 0 14px;color:var(--sub);font-size:12px;line-height:1.65}
.confirm-input{padding-right:13px;margin-bottom:10px;background:var(--surface)}
.finish-note{display:none;margin-top:18px;color:var(--sub);font-size:12px;line-height:1.6;text-align:center}
.finish-note.visible{display:block}
button:focus-visible,input:focus-visible,summary:focus-visible{outline:3px solid rgba(20,184,166,.28);outline-offset:2px}
@media (max-width:760px){
  .page{width:min(100% - 20px,560px);padding:14px 0 26px}
  .topbar{margin-bottom:12px;padding:0 4px}
  .shell{grid-template-columns:1fr;border-radius:16px}
  .intro{padding:28px 24px;border-right:0;border-bottom:1px solid var(--border)}
  .intro .steps{display:none}
  .privacy-note{margin-top:20px;padding-top:16px}
  .panel{padding:28px 24px}
}
@media (prefers-reduced-motion:reduce){*{scroll-behavior:auto!important;transition:none!important}}
</style>
</head>
<body>
<main class="page">
  <header class="topbar">
    <div class="brand">ClipVault</div>
    <div class="local-badge">仅在本机处理</div>
  </header>

  <section class="shell" aria-labelledby="pageTitle">
    <div class="intro">
      <div class="eyebrow">数据安全恢复</div>
      <h1 id="pageTitle">恢复剪贴板数据库</h1>
      <p class="lead">Windows 无法解开当前数据库密钥。通常发生在重装系统、更换 Windows 用户或本机密钥损坏后。</p>
      <ol class="steps">
        <li>输入之前在 ClipVault 中设置的数据恢复密码。</li>
        <li>验证成功后，应用会重新保护数据库密钥。</li>
        <li>现有历史记录、收藏和设置会保持不变。</li>
      </ol>
      <p class="privacy-note">恢复密码和数据库内容不会上传。本页面由 ClipVault 在本机临时启动，15 分钟后自动失效。</p>
    </div>

    <div class="panel">
      <h2 class="panel-title">使用恢复密码</h2>
      <p class="panel-desc">这不是软件激活码，也不是锁屏密码。请填写你为数据库单独设置的数据恢复密码。</p>

      <div id="msg" class="message" role="status" aria-live="polite"></div>

      <div id="recoverSection">
        <label class="field-label" for="password">数据恢复密码</label>
        <div class="input-wrap">
          <input class="field-input" type="password" id="password" placeholder="请输入数据恢复密码" autocomplete="current-password" autofocus>
          <button class="show-password" id="showPasswordBtn" type="button" aria-controls="password" aria-pressed="false" onclick="togglePassword()">显示</button>
        </div>
        <p class="hint">恢复失败不会覆盖或删除现有数据库，可以重新尝试。</p>
        <button class="btn btn-primary" id="recoverBtn" type="button" onclick="doRecover()">验证密码并恢复数据</button>
      </div>

      <details class="alternative" id="newDatabaseDetails">
        <summary>没有恢复密码，无法打开原数据？</summary>
        <div class="danger-box">
          <p class="danger-title">创建新的空数据库</p>
          <p class="danger-copy">原数据库不会被删除，但会被隔离保存。没有正确的恢复密码，旧数据仍然无法读取。</p>
          <label class="field-label" for="confirmText">输入“创建新数据库”确认</label>
          <input class="field-input confirm-input" id="confirmText" type="text" autocomplete="off" placeholder="创建新数据库">
          <button class="btn btn-danger" id="newBtn" type="button" onclick="doNew()" disabled>隔离旧数据并创建新库</button>
        </div>
      </details>

      <p class="finish-note" id="finishNote">操作完成后，可以关闭此页面并返回 ClipVault。</p>
    </div>
  </section>
</main>

<script>
var recoveryToken = location.hash.length > 1 ? location.hash.substring(1) : '';
if (recoveryToken) history.replaceState(null, '', location.pathname);

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
  if (!password) {
    showMsg('请输入数据恢复密码。', 'error');
    input.focus();
    return;
  }
  setBusy(button, true, '正在验证并恢复…', '验证密码并恢复数据');
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
  setBusy(button, false, '正在验证并恢复…', '验证密码并恢复数据');
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

// RecoveryServer is a minimal HTTP server for database recovery.
type RecoveryServer struct {
	dataDir string
	server  *http.Server
	url     string
	done    chan struct{}
	success bool
	token   string
	port    int
	once    sync.Once
	timer   *time.Timer
}

// NewRecoveryServer creates a recovery server bound to localhost on a dynamic port.
func NewRecoveryServer(dataDir string) *RecoveryServer {
	return &RecoveryServer{
		dataDir: dataDir,
		done:    make(chan struct{}),
	}
}

// Start binds and starts the recovery HTTP server.
func (rs *RecoveryServer) Start() (string, error) {
	token, err := generateCSRFToken()
	if err != nil {
		return "", fmt.Errorf("recovery token: %w", err)
	}
	rs.token = token
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", rs.handleIndex)
	mux.HandleFunc("POST /api/recovery/recover", rs.handleRecover)
	mux.HandleFunc("POST /api/recovery/new", rs.handleNew)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("recovery server bind: %w", err)
	}
	rs.port = tcpPort(ln.Addr(), 0)
	rs.url = "http://" + ln.Addr().String() + "/#" + rs.token
	rs.server = &http.Server{
		Handler:           rs.secureHTTP(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		_ = rs.server.Serve(ln)
	}()
	rs.timer = time.AfterFunc(15*time.Minute, func() {
		rs.finish(false)
		rs.Close()
	})
	return rs.url, nil
}

// URL returns the recovery server URL.
func (rs *RecoveryServer) URL() string { return rs.url }

// Done returns a channel that is closed when recovery succeeds.
func (rs *RecoveryServer) Done() <-chan struct{} { return rs.done }

// Success reports whether recovery was successful.
func (rs *RecoveryServer) Success() bool { return rs.success }

// Close shuts down the recovery server.
func (rs *RecoveryServer) Close() {
	if rs.timer != nil {
		rs.timer.Stop()
	}
	if rs.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = rs.server.Shutdown(ctx)
		cancel()
	}
}

func (rs *RecoveryServer) finish(success bool) {
	rs.once.Do(func() {
		rs.success = success
		close(rs.done)
	})
}

func (rs *RecoveryServer) secureHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if !hostMatchesLoopbackPort(r.Host, rs.port) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if isMutationMethod(r.Method) {
			if !originMatchesRequest(r.Header.Get("Origin"), r.Host, rs.port) {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
			capability := r.Header.Get("X-ClipVault-Recovery-Token")
			csrf := r.Header.Get(csrfHeaderName)
			if subtle.ConstantTimeCompare([]byte(capability), []byte(rs.token)) != 1 || subtle.ConstantTimeCompare([]byte(csrf), []byte(rs.token)) != 1 {
				http.Error(w, "recovery authorization failed", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (rs *RecoveryServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(recoveryHTML))
}

func (rs *RecoveryServer) handleRecover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRecoveryJSON(w, false, "请求格式无效")
		return
	}
	if req.Password == "" {
		writeRecoveryJSON(w, false, "请输入数据恢复密码")
		return
	}
	if err := RecoverWithPassword(rs.dataDir, req.Password); err != nil {
		writeRecoveryJSON(w, false, "恢复失败：密码不正确，或恢复文件已经损坏")
		return
	}
	writeRecoveryJSON(w, true, "")
	rs.finish(true)
}

func (rs *RecoveryServer) handleNew(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Confirmation string `json:"confirmation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Confirmation) != "CREATE NEW" {
		writeRecoveryJSON(w, false, "确认文字不匹配")
		return
	}
	if err := CreateNewDatabase(rs.dataDir); err != nil {
		writeRecoveryJSON(w, false, "创建新数据库失败，原数据未被覆盖")
		return
	}
	writeRecoveryJSON(w, true, "")
	rs.finish(true)
}

func writeRecoveryJSON(w http.ResponseWriter, ok bool, errMsg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "error": errMsg})
}

// openBrowser tries to open the default browser to the given URL.
func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	default:
		_ = exec.Command("xdg-open", url).Start()
	}
}
