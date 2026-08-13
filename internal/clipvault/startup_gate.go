package clipvault

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type startupGateMode string

const (
	startupGateSetCredentials startupGateMode = "set_credentials"
	startupGateRepairConfig   startupGateMode = "repair_config"
	startupGateCookieName                     = "clipvault_startup_session"
)

type startupGatePageData struct {
	Title             string
	Description       string
	Button            string
	Mode              string
	MinRecoveryRunes  int
	MaxRecoveryBytes  int
	MinSecondaryRunes int
	MaxSecondaryBytes int
}

// StartupGate blocks normal application startup until the mandatory recovery
// and secondary credentials exist, or a damaged configuration has been repaired
// after proving possession of the recovery password.
//
// 密码设置完成后门禁不立即关闭：主流程在主服务就绪后调用 SetRecordURL
// 把记录页地址写入门禁，前端轮询 /api/status 拿到后自动跳转，避免用户
// 停留在已失效的门禁端口上刷新出"拒绝连接"。
type StartupGate struct {
	store    *Store
	dataDir  string
	mode     startupGateMode
	token    string
	server   *http.Server
	url      string
	port     int
	done     chan struct{}
	once     sync.Once
	opMu     sync.Mutex
	complete bool
	success  bool

	recordURL  string
	recordDone chan struct{}
	recordOnce sync.Once
}

func NewStartupGate(store *Store, dataDir string, mode startupGateMode) *StartupGate {
	return &StartupGate{
		store:      store,
		dataDir:    dataDir,
		mode:       mode,
		done:       make(chan struct{}),
		recordDone: make(chan struct{}),
	}
}

func (g *StartupGate) Start() (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate startup capability: %w", err)
	}
	g.token = token
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen startup gate: %w", err)
	}
	g.port = ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", g.handleIndex)
	mux.HandleFunc("GET /api/status", g.handleStatus)
	mux.HandleFunc("POST /api/session", g.handleSession)
	mux.HandleFunc("POST /api/complete", g.handleComplete)
	g.server = &http.Server{
		Handler:           g.secureHTTP(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	g.url = "http://" + ln.Addr().String() + "/#" + token
	go func() {
		if err := g.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.fail()
		}
	}()
	return g.url, nil
}

func (g *StartupGate) Done() <-chan struct{} { return g.done }
func (g *StartupGate) Success() bool         { return g.success }

func (g *StartupGate) Close() {
	if g.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = g.server.Shutdown(ctx)
}

// SetRecordURL 在主服务就绪后把记录页地址写入门禁。
// 前端轮询 /api/status 拿到该地址后自动跳转，避免用户停留在已失效的
// 门禁端口上。调用后门禁 HTTP 服务保持 30 秒供前端轮询，随后自动关闭。
func (g *StartupGate) SetRecordURL(url string) {
	g.recordOnce.Do(func() {
		g.recordURL = url
		close(g.recordDone)
		go func() {
			time.Sleep(30 * time.Second)
			g.Close()
		}()
	})
}

func (g *StartupGate) finish(success bool) {
	g.once.Do(func() {
		g.success = success
		close(g.done)
	})
}

func (g *StartupGate) fail() {
	g.opMu.Lock()
	defer g.opMu.Unlock()
	if g.complete {
		return
	}
	g.complete = true
	g.finish(false)
}

func (g *StartupGate) secureHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'")
		if !hostMatchesLoopbackPort(r.Host, g.port) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if isMutationMethod(r.Method) {
			if !originMatchesRequest(r.Header.Get("Origin"), r.Host, g.port) || !g.hasStartupCapability(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (g *StartupGate) hasStartupCapability(r *http.Request) bool {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-ClipVault-Startup-Token")), []byte(g.token)) == 1 {
		return true
	}
	cookie, err := r.Cookie(startupGateCookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(g.token)) == 1
}

func (g *StartupGate) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	select {
	case <-g.recordDone:
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "recordURL": g.recordURL})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
	}
}

func (g *StartupGate) handleSession(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     startupGateCookieName,
		Value:    g.token,
		Path:     "/",
		MaxAge:   10 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeStartupGateJSON(w, true, "")
}

func (g *StartupGate) handleIndex(w http.ResponseWriter, _ *http.Request) {
	title := "首次启动安全设置"
	description := "完成以下安全设置后开始记录剪贴板。"
	button := "完成设置并开始使用"
	switch g.mode {
	case startupGateRepairConfig:
		title = "配置损坏，需验证恢复密码"
		description = "安全配置无法解析。输入恢复密码，验证通过后恢复最近一次有效配置。"
		button = "验证并修复配置"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = startupGateTemplate.Execute(w, startupGatePageData{
		Title:             title,
		Description:       description,
		Button:            button,
		Mode:              string(g.mode),
		MinRecoveryRunes:  minRecoveryPasswordRunes,
		MaxRecoveryBytes:  maxRecoveryPasswordBytes,
		MinSecondaryRunes: minAuthPasswordRunes,
		MaxSecondaryBytes: maxAuthPasswordBytes,
	})
}

func (g *StartupGate) handleComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password          string `json:"password"`
		Confirm           string `json:"confirm"`
		SecondaryPassword string `json:"secondaryPassword"`
		SecondaryConfirm  string `json:"secondaryConfirm"`
	}
	if err := decodeJSONRequest(w, r, &req, 8<<10); err != nil {
		writeStartupGateJSON(w, false, "请求格式无效")
		return
	}
	g.opMu.Lock()
	defer g.opMu.Unlock()
	if g.complete {
		writeStartupGateJSON(w, false, "安全设置已完成")
		return
	}
	var err error
	switch g.mode {
	case startupGateSetCredentials:
		if req.Password != req.Confirm {
			writeStartupGateJSON(w, false, "两次输入的恢复密码不一致")
			return
		}
		if req.SecondaryPassword != req.SecondaryConfirm {
			writeStartupGateJSON(w, false, "两次输入的二次密码不一致")
			return
		}
		err = g.setInitialCredentials(req.Password, req.SecondaryPassword)
	case startupGateRepairConfig:
		err = VerifyRecoveryPassword(g.dataDir, req.Password)
		if err == nil {
			_, err = g.store.RestoreLastGoodConfig()
		}
	default:
		err = fmt.Errorf("unsupported startup mode")
	}
	if err != nil {
		message := "恢复密码不正确，无法修复配置"
		if g.mode != startupGateRepairConfig {
			message = err.Error()
			message = strings.TrimPrefix(message, "weak_password: ")
		}
		writeStartupGateJSON(w, false, message)
		return
	}
	g.complete = true
	http.SetCookie(w, &http.Cookie{
		Name:     startupGateCookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeStartupGateJSON(w, true, "")
	go func() {
		time.Sleep(100 * time.Millisecond)
		g.finish(true)
	}()
}

func (g *StartupGate) setInitialCredentials(recoveryPassword, secondaryPassword string) error {
	if err := validateRecoveryPassword(recoveryPassword); err != nil {
		return err
	}
	secondaryHash, err := newPasswordHash(secondaryPassword)
	if err != nil {
		return err
	}
	cfg, err := g.store.GetConfig()
	if err != nil {
		return err
	}
	if cfg.Security.SecondaryPasswordHash != "" {
		return errors.New("二次密码已设置，无法重复初始化")
	}
	hasRecovery, err := HasRecoveryPassword(g.dataDir)
	if err != nil {
		return err
	}
	if hasRecovery {
		return errors.New("恢复密码已设置，无法重复初始化")
	}
	if err := SetRecoveryPassword(g.dataDir, g.store.DBKey(), recoveryPassword); err != nil {
		return err
	}
	cfg.Security.SecondaryPasswordHash = secondaryHash
	if err := g.store.SaveConfig(cfg); err != nil {
		removeErr := os.Remove(activeDatabaseFile(g.dataDir, databaseRecoveryFileName))
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return errors.Join(fmt.Errorf("保存二次密码: %w", err), fmt.Errorf("回滚恢复密码: %w", removeErr))
		}
		return fmt.Errorf("保存二次密码: %w", err)
	}
	return nil
}

func writeStartupGateJSON(w http.ResponseWriter, ok bool, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "error": message})
}

var startupGateTemplate = template.Must(template.New("startup-gate").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title><style>
:root{color-scheme:light dark;font-family:"Segoe UI","Microsoft YaHei",sans-serif}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0f172a;color:#e2e8f0;padding:40px 20px}
.main{width:min(480px,100%)}
h1{font-size:22px;margin:0 0 8px;font-weight:700}
.desc{line-height:1.7;color:#94a3b8;margin:0 0 30px;font-size:14px}
.section{margin-bottom:28px}
.section-title{font-size:13.5px;font-weight:600;color:#cbd5e1;margin:0 0 3px;padding-bottom:9px;border-bottom:1px solid #263548}
.section-hint{font-size:12.5px;color:#7d8ca3;margin:8px 0 16px;line-height:1.6}
.field{margin-bottom:16px}
.field label{display:block;font-size:13px;color:#cbd5e1;margin:0 0 6px}
.wrap{position:relative}
.wrap input{width:100%;box-sizing:border-box;border:1px solid #334155;background:#0f172a;color:#fff;border-radius:10px;padding:12px 52px 12px 14px;font:inherit;outline:none;transition:border-color .15s}
.wrap input:focus{border-color:#10b981}
.wrap input.bad{border-color:#b91c1c}
.eye{position:absolute;right:6px;top:50%;transform:translateY(-50%);border:0;background:transparent;color:#64748b;cursor:pointer;padding:6px 9px;font:inherit;font-size:12px;border-radius:6px}
.eye:hover{color:#cbd5e1;background:#1e293b}
.msg{font-size:12px;margin-top:5px;min-height:15px;line-height:1.5}
.msg.ok{color:#34d399}
.msg.err{color:#f87171}
.warn{background:#292524;border:1px solid #57534e;color:#fbbf24;font-size:12.5px;line-height:1.65;padding:12px 14px;border-radius:10px;margin:4px 0 22px}
button.submit{width:100%;border:0;border-radius:10px;padding:13px 14px;font:inherit;font-weight:700;cursor:pointer;background:#10b981;color:#052e24;transition:opacity .15s}
button.submit:disabled{opacity:.4;cursor:not-allowed}
.error{color:#fca5a5;font-size:13px;margin-top:12px;min-height:20px;text-align:center;line-height:1.5}
.credential-form{margin:0}
</style></head><body><main class="main">
<h1>{{.Title}}</h1>
<p class="desc">{{.Description}}</p>
{{if eq .Mode "set_credentials"}}
<form id="recovery-form" class="credential-form" novalidate>
<input type="text" name="username" value="ClipVault 恢复密码" autocomplete="username" hidden>
<div class="section">
<p class="section-title">恢复密码</p>
<p class="section-hint">用于重装系统、迁移电脑或 Windows 密钥失效时恢复数据库，请离线妥善保存。</p>
<div class="field"><label for="password">恢复密码</label><div class="wrap"><input id="password" name="password" type="password" autocomplete="new-password" required autofocus><button type="button" class="eye" data-target="password">显示</button></div><div class="msg" id="msg-password"></div></div>
<div class="field"><label for="confirm">确认恢复密码</label><div class="wrap"><input id="confirm" name="password-confirmation" type="password" autocomplete="new-password" required><button type="button" class="eye" data-target="confirm">显示</button></div><div class="msg" id="msg-confirm"></div></div>
</div>
</form>
<form id="secondary-form" class="credential-form" novalidate>
<input type="text" name="username" value="ClipVault 二次密码" autocomplete="username" hidden>
<div class="section">
<p class="section-title">二次密码</p>
<p class="section-hint">用于查看或操作敏感数据时的二次确认。</p>
<div class="field"><label for="secondaryPassword">二次密码</label><div class="wrap"><input id="secondaryPassword" name="password" type="password" autocomplete="new-password" required><button type="button" class="eye" data-target="secondaryPassword">显示</button></div><div class="msg" id="msg-secondaryPassword"></div></div>
<div class="field"><label for="secondaryConfirm">确认二次密码</label><div class="wrap"><input id="secondaryConfirm" name="password-confirmation" type="password" autocomplete="new-password" required><button type="button" class="eye" data-target="secondaryConfirm">显示</button></div><div class="msg" id="msg-secondaryConfirm"></div></div>
</div>
</form>
<div class="warn">请妥善离线保存：如果 Windows 密钥和恢复密码同时丢失，数据库将无法恢复。</div>
{{else}}
<form id="recovery-form" class="credential-form" novalidate>
<input type="text" name="username" value="ClipVault 恢复密码" autocomplete="username" hidden>
<div class="field"><label for="password">恢复密码</label><div class="wrap"><input id="password" name="password" type="password" autocomplete="current-password" required autofocus><button type="button" class="eye" data-target="password">显示</button></div><div class="msg" id="msg-password"></div></div>
</form>
{{end}}
<button type="button" class="submit" id="submit" disabled>{{.Button}}</button>
<div class="error" id="error"></div>
</main><script>
const startupToken=location.hash.slice(1);
history.replaceState(null,'',location.pathname);
const mode='{{.Mode}}',isSetup=mode==='set_credentials';
const $=id=>document.getElementById(id);
const els={password:$('password'),confirm:$('confirm'),secondaryPassword:$('secondaryPassword'),secondaryConfirm:$('secondaryConfirm')};
const msgs={password:$('msg-password'),confirm:$('msg-confirm'),secondaryPassword:$('msg-secondaryPassword'),secondaryConfirm:$('msg-secondaryConfirm')};
const submit=$('submit'),errBox=$('error');
const MIN_REC={{.MinRecoveryRunes}},MAX_REC_BYTES={{.MaxRecoveryBytes}},MIN_SEC={{.MinSecondaryRunes}},MAX_SEC_BYTES={{.MaxSecondaryBytes}};
const utf8Encoder=new TextEncoder();
const edgeWhitespace=/^\p{White_Space}|\p{White_Space}$/u;
let startupSessionReady=!startupToken;

async function establishStartupSession(){
  if(!startupToken)return;
  try{
    const res=await fetch('/api/session',{method:'POST',headers:{'X-ClipVault-Startup-Token':startupToken}});
    startupSessionReady=res.ok;
    if(!res.ok)errBox.textContent='安全设置链接已失效，请重启 ClipVault 后重试';
  }catch(_){
    startupSessionReady=false;
    errBox.textContent='无法连接本地服务，请确认 ClipVault 仍在运行';
  }
  validate();
}

document.querySelectorAll('.eye').forEach(btn=>{btn.addEventListener('click',()=>{const input=$(btn.dataset.target);const show=input.type==='password';input.type=show?'text':'password';btn.textContent=show?'隐藏':'显示';});});

function setMsg(key,ok,text){const el=msgs[key];if(!el)return;el.textContent=text||'';el.className='msg'+(text?(ok?' ok':' err'):'');}
function mark(key,bad){const el=els[key];if(el)el.classList.toggle('bad',bad);}
function checkPassword(key,min,maxBytes){
  const v=els[key].value;
  if(!v){setMsg(key,false,'');mark(key,false);return false;}
  if(edgeWhitespace.test(v)){setMsg(key,false,'首尾不能有空白字符');mark(key,true);return false;}
  const runeCount=Array.from(v).length;
  if(runeCount<min){setMsg(key,false,'至少 '+min+' 个字符，还差 '+(min-runeCount)+' 个');mark(key,true);return false;}
  if(utf8Encoder.encode(v).length>maxBytes){setMsg(key,false,'密码过长');mark(key,true);return false;}
  setMsg(key,true,'✓ 长度达标');mark(key,false);return true;
}
function checkPair(a,b){const va=els[a].value,vb=els[b].value;if(!vb){setMsg(b,false,'');mark(b,false);return false;}if(va!==vb){setMsg(b,false,'两次输入不一致');mark(b,true);return false;}setMsg(b,true,'✓ 一致');mark(b,false);return true;}

function validate(){
  let ok=true;
  if(isSetup){
    const recoveryOK=checkPassword('password',MIN_REC,MAX_REC_BYTES);
    const recoveryPairOK=checkPair('password','confirm');
    const secondaryOK=checkPassword('secondaryPassword',MIN_SEC,MAX_SEC_BYTES);
    const secondaryPairOK=checkPair('secondaryPassword','secondaryConfirm');
    ok=recoveryOK&&recoveryPairOK&&secondaryOK&&secondaryPairOK;
  }else{
    ok=els.password.value.length>0;
  }
  submit.disabled=!ok||!startupSessionReady;
}
Object.values(els).forEach(el=>{if(el)el.addEventListener('input',validate);});
validate();
establishStartupSession();

async function submitCredentials(e){
  if(e)e.preventDefault();
  if(submit.disabled)return;
  errBox.textContent='';submit.disabled=true;
  try{
    const res=await fetch('/api/complete',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:els.password.value,confirm:els.confirm?els.confirm.value:'',secondaryPassword:els.secondaryPassword?els.secondaryPassword.value:'',secondaryConfirm:els.secondaryConfirm?els.secondaryConfirm.value:''})});
    let data={};try{data=await res.json();}catch(_){data={};}
    if(!res.ok){errBox.textContent=res.status===403?'安全会话已失效，请刷新页面后重试':(data.error||'本地服务请求失败');submit.disabled=false;return;}
    if(!data.ok){errBox.textContent=data.error||'操作失败';submit.disabled=false;return;}
    const card=document.querySelector('.main');
    card.innerHTML='<h1>设置完成</h1><p class="desc">正在打开记录页面...</p><p class="warn">如未自动跳转，请点击任务栏右下角 ClipVault 图标 → 右键 → 搜索历史 / 设置</p>';
    let polled=0;const poll=async()=>{try{const r=await fetch('/api/status');const d=await r.json();if(d.ok&&d.recordURL){location.href=d.recordURL;return;}}catch(_){}if(++polled>30){card.innerHTML='<h1>设置完成</h1><p class="desc">记录页面未自动打开。</p><p class="warn">请点击任务栏右下角 ClipVault 图标 → 右键 → 搜索历史 / 设置</p>';return;}setTimeout(poll,500);};poll();
  }catch(_){errBox.textContent='无法连接本地服务，请确认 ClipVault 仍在运行';submit.disabled=false;}
}
document.querySelectorAll('form').forEach(form=>form.addEventListener('submit',submitCredentials));
submit.addEventListener('click',submitCredentials);
</script></body></html>`))
