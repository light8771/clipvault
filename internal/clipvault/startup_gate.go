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
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type startupGateMode string

const (
	startupGateSetCredentials startupGateMode = "set_credentials"
	startupGateRepairConfig   startupGateMode = "repair_config"
)

// StartupGate blocks normal application startup until the mandatory recovery
// and secondary credentials exist, or a damaged configuration has been repaired
// after proving possession of the recovery password.
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
}

func NewStartupGate(store *Store, dataDir string, mode startupGateMode) *StartupGate {
	return &StartupGate{store: store, dataDir: dataDir, mode: mode, done: make(chan struct{})}
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
			if !originMatchesRequest(r.Header.Get("Origin"), r.Host, g.port) ||
				subtle.ConstantTimeCompare([]byte(r.Header.Get("X-ClipVault-Startup-Token")), []byte(g.token)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (g *StartupGate) handleIndex(w http.ResponseWriter, _ *http.Request) {
	title := "首次启动：设置恢复密码和二次密码"
	description := "应用在完成此步骤前不会开始记录剪贴板。恢复密码用于在重装系统、迁移电脑或 Windows DPAPI 密钥失效时解密数据库密钥；二次密码用于保护敏感数据和安全操作。请分别妥善保管。"
	button := "设置并开始使用"
	switch g.mode {
	case startupGateRepairConfig:
		title = "配置损坏：验证恢复密码"
		description = "数据库仍可打开，但安全配置无法解析。为防止密码保护被静默关闭，请输入数据恢复密码；验证成功后将恢复最近一次有效配置。"
		button = "验证并修复配置"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = startupGateTemplate.Execute(w, map[string]string{"Title": title, "Description": description, "Button": button, "Mode": string(g.mode)})
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
		removeErr := os.Remove(filepath.Join(g.dataDir, "clipvault.recovery"))
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
:root{color-scheme:light dark;font-family:"Segoe UI","Microsoft YaHei",sans-serif}body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0f172a;color:#e2e8f0}.card{width:min(560px,calc(100vw - 40px));box-sizing:border-box;padding:32px;border:1px solid #334155;border-radius:18px;background:#1e293b;box-shadow:0 24px 70px #0008}h1{font-size:24px;margin:0 0 14px}p{line-height:1.75;color:#cbd5e1;margin:0 0 22px}.warn{color:#fbbf24}input,button{width:100%;box-sizing:border-box;border-radius:10px;padding:12px 14px;font:inherit}input{margin:0 0 12px;border:1px solid #475569;background:#0f172a;color:#fff}button{border:0;background:#10b981;color:#052e24;font-weight:700;cursor:pointer}.error{min-height:24px;color:#fca5a5;margin-top:12px}.hidden{display:none}
</style></head><body><main class="card"><h1>{{.Title}}</h1><p>{{.Description}}</p><p class="warn">请妥善离线保存：如果 Windows 密钥和恢复密码同时丢失，数据库将无法恢复。</p><form id="form"><input id="password" type="password" autocomplete="new-password" placeholder="恢复密码（至少 12 个字符）" required>{{if eq .Mode "set_credentials"}}<input id="confirm" type="password" autocomplete="new-password" placeholder="再次输入恢复密码" required><input id="secondaryPassword" type="password" autocomplete="new-password" placeholder="二次密码（至少 8 个字符）" required><input id="secondaryConfirm" type="password" autocomplete="new-password" placeholder="再次输入二次密码" required>{{end}}<button type="submit">{{.Button}}</button></form><div class="error" id="error"></div></main><script>
const token=location.hash.slice(1);history.replaceState(null,'',location.pathname);const form=document.getElementById('form'),err=document.getElementById('error');form.addEventListener('submit',async e=>{e.preventDefault();err.textContent='';const button=form.querySelector('button');button.disabled=true;try{const password=document.getElementById('password')?.value||'';const confirm=document.getElementById('confirm')?.value||'';const secondaryPassword=document.getElementById('secondaryPassword')?.value||'';const secondaryConfirm=document.getElementById('secondaryConfirm')?.value||'';const res=await fetch('/api/complete',{method:'POST',headers:{'Content-Type':'application/json','X-ClipVault-Startup-Token':token},body:JSON.stringify({password,confirm,secondaryPassword,secondaryConfirm})});const data=await res.json();if(!data.ok){err.textContent=data.error||'操作失败';button.disabled=false;return}document.querySelector('.card').innerHTML='<h1>设置完成</h1><p>ClipVault 正在启动，可以关闭此页面。</p>'}catch(_){err.textContent='无法连接本地服务';button.disabled=false}});
</script></body></html>`))
