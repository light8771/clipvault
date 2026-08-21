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
	"sync"
	"time"
	"unicode/utf8"
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
	Lang              string
	MinRecoveryRunes  int
	MaxRecoveryBytes  int
	MinSecondaryRunes int
	MaxSecondaryBytes int
	ThemeMode         string
	PrimaryLight      string
	PrimaryLightHover string
	PrimaryDark       string
	PrimaryDarkHover  string
}

// startupGatePalette mirrors the primary/primary-hover pairs defined for each
// preset in web/app/appearance-theme.js, so the standalone gate page follows
// the skin the user picked for the main application.
type startupGatePalette struct {
	light      string
	lightHover string
	dark       string
	darkHover  string
}

var startupGatePalettes = map[string]startupGatePalette{
	"mint":     {"#14B8A6", "#0D9488", "#2DD4BF", "#14B8A6"},
	"ocean":    {"#0284C7", "#0369A1", "#38BDF8", "#0EA5E9"},
	"sunrise":  {"#EA580C", "#C2410C", "#FB923C", "#F97316"},
	"graphite": {"#475569", "#334155", "#94A3B8", "#64748B"},
}

func startupGateThemeColors(store *Store) (mode string, palette startupGatePalette) {
	mode = "system"
	palette = startupGatePalettes["mint"]
	if store == nil {
		return mode, palette
	}
	cfg, err := store.GetConfig()
	if err != nil {
		return mode, palette
	}
	switch cfg.Theme.Mode {
	case "light", "dark":
		mode = cfg.Theme.Mode
	}
	if cfg.Theme.Preset == "custom" {
		if primary, ok := normalizeStartupGateHex(cfg.Theme.Primary); ok {
			return mode, startupGatePalette{
				light:      primary,
				lightHover: blendStartupGateHex(primary, false, 0.12),
				dark:       primary,
				darkHover:  blendStartupGateHex(primary, true, 0.12),
			}
		}
		return mode, palette
	}
	if preset, ok := startupGatePalettes[cfg.Theme.Preset]; ok {
		palette = preset
	}
	return mode, palette
}

func normalizeStartupGateHex(hex string) (string, bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return "", false
	}
	for _, c := range hex[1:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return "", false
		}
	}
	return hex, true
}

// blendStartupGateHex moves a #RRGGBB color toward white (lighten=true) or
// black by ratio, producing the hover variant for custom theme colors.
func blendStartupGateHex(hex string, lighten bool, ratio float64) string {
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return hex
	}
	target := 0
	if lighten {
		target = 255
	}
	mix := func(c int) int { return c + int(float64(target-c)*ratio) }
	return fmt.Sprintf("#%02X%02X%02X", mix(r), mix(g), mix(b))
}

// StartupGate blocks normal application startup until the mandatory recovery
// and secondary credentials exist, or a damaged configuration has been repaired
// after proving possession of the recovery password.
//
// 密码设置完成后门禁不立即关闭：主流程在主服务就绪后调用 SetRecordURL
// 把记录页地址写入门禁，前端轮询启动状态拿到后自动跳转，避免用户
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
	// i18n 在门禁期独立构建（store 已打开且官方词条已 Seed）；主服务
	// 就绪后会用完整服务覆盖全局模板指针。
	i18n *I18nService

	recordURL  string
	recordDone chan struct{}
	recordOnce sync.Once
}

func NewStartupGate(store *Store, dataDir string, mode startupGateMode) *StartupGate {
	gate := &StartupGate{
		store:      store,
		dataDir:    dataDir,
		mode:       mode,
		done:       make(chan struct{}),
		recordDone: make(chan struct{}),
	}
	if store != nil {
		if svc, err := NewI18nService(store, nil, nil); err == nil {
			gate.i18n = svc
			// 门禁页 {{T}} 渲染依赖包级服务指针；主流程随后会覆盖注册。
			templateI18nService.Store(svc)
		}
	}
	return gate
}

// t 门禁文案查询；i18n 构建失败的异常场景回退 key 原文。
func (g *StartupGate) t(key string, params map[string]any) string {
	if g.i18n == nil {
		return key
	}
	return g.i18n.T(key, params)
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
	mux.HandleFunc("GET /{$}", g.handleIndex)
	mux.HandleFunc("GET /startup.css", g.handleAsset)
	mux.HandleFunc("GET /startup.js", g.handleAsset)
	mux.HandleFunc("GET /api/v1/startup/status", g.handleStatus)
	mux.HandleFunc("POST /api/v1/startup/session", g.handleSession)
	mux.HandleFunc("POST /api/v1/startup/credentials", g.handleComplete)
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
// 前端轮询启动状态拿到该地址后自动跳转，避免用户停留在已失效的
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
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'")
		if !hostMatchesLoopbackPort(r.Host, g.port) {
			writeStandaloneError(w, http.StatusForbidden, "invalid_host", "invalid host")
			return
		}
		if isMutationMethod(r.Method) {
			if !originMatchesRequest(r.Header.Get("Origin"), r.Host, g.port) || !g.hasStartupCapability(r) {
				writeStandaloneError(w, http.StatusForbidden, "startup_authorization_failed", "startup authorization failed")
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
		writeStandaloneData(w, map[string]any{"ready": true, "recordURL": g.recordURL})
	default:
		writeStandaloneData(w, map[string]any{"ready": false})
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
	title := g.t("startup_page.title_setup", nil)
	description := g.t("startup_page.desc_setup", nil)
	button := g.t("startup_page.button_setup", nil)
	switch g.mode {
	case startupGateRepairConfig:
		title = g.t("startup_page.title_repair", nil)
		description = g.t("startup_page.desc_repair", nil)
		button = g.t("startup_page.button_repair", nil)
	}
	themeMode, palette := startupGateThemeColors(g.store)
	lang := I18nChinese
	if g.i18n != nil {
		lang = g.i18n.CurrentLanguage()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = startupGateTemplate.Execute(w, startupGatePageData{
		Title:             title,
		Description:       description,
		Button:            button,
		Mode:              string(g.mode),
		Lang:              lang,
		MinRecoveryRunes:  minRecoveryPasswordRunes,
		MaxRecoveryBytes:  maxRecoveryPasswordBytes,
		MinSecondaryRunes: minAuthPasswordRunes,
		MaxSecondaryBytes: maxAuthPasswordBytes,
		ThemeMode:         themeMode,
		PrimaryLight:      palette.light,
		PrimaryLightHover: palette.lightHover,
		PrimaryDark:       palette.dark,
		PrimaryDarkHover:  palette.darkHover,
	})
}

func (g *StartupGate) handleAsset(w http.ResponseWriter, r *http.Request) {
	contentType := "text/css; charset=utf-8"
	if r.URL.Path == "/startup.js" {
		contentType = "application/javascript; charset=utf-8"
	}
	serveEmbeddedWebFile(w, "web"+r.URL.Path, contentType)
}

func (g *StartupGate) handleComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password          string `json:"password"`
		Confirm           string `json:"confirm"`
		SecondaryPassword string `json:"secondaryPassword"`
		SecondaryConfirm  string `json:"secondaryConfirm"`
		LockPassword      string `json:"lockPassword"`
		LockConfirm       string `json:"lockConfirm"`
	}
	if err := decodeJSONRequest(w, r, &req, 8<<10); err != nil {
		writeStartupGateJSON(w, false, g.t("startup_page.err_invalid_request", nil))
		return
	}
	g.opMu.Lock()
	defer g.opMu.Unlock()
	if g.complete {
		writeStartupGateJSON(w, false, g.t("startup_page.err_already_done", nil))
		return
	}
	var err error
	switch g.mode {
	case startupGateSetCredentials:
		if req.Password != req.Confirm {
			writeStartupGateJSON(w, false, g.t("startup_page.err_recovery_mismatch", nil))
			return
		}
		if req.SecondaryPassword != req.SecondaryConfirm {
			writeStartupGateJSON(w, false, g.t("startup_page.err_secondary_mismatch", nil))
			return
		}
		if req.LockPassword != req.LockConfirm {
			writeStartupGateJSON(w, false, g.t("startup_page.err_lock_mismatch", nil))
			return
		}
		err = g.setInitialCredentials(req.Password, req.SecondaryPassword, req.LockPassword)
	case startupGateRepairConfig:
		err = VerifyRecoveryPassword(g.dataDir, req.Password)
		if err == nil {
			_, err = g.store.RestoreLastGoodConfig()
		}
	default:
		err = fmt.Errorf("unsupported startup mode")
	}
	if err != nil {
		message := g.t("startup_page.err_repair_wrong_password", nil)
		if g.mode != startupGateRepairConfig {
			var recoveryErr *RecoveryPasswordError
			if errors.As(err, &recoveryErr) && recoveryErr.Code == "weak_password" {
				// 弱密码错误按具体情形给参数化词条。
				if utf8.RuneCountInString(req.Password) > maxRecoveryPasswordBytes {
					message = g.t("startup_page.err_recovery_too_long", nil)
				} else {
					message = g.t("startup_page.err_recovery_rule", map[string]any{"n": minRecoveryPasswordRunes})
				}
			} else {
				message = err.Error()
			}
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

// setInitialCredentials 完成首次启动的三层凭据初始化。锁屏密码可选：
// 传空串表示用户明确跳过（界面留空），此时 Web 界面无锁屏门，
// 搜索历史等非敏感端点对本机进程直接可见——引导页已明示该后果。
func (g *StartupGate) setInitialCredentials(recoveryPassword, secondaryPassword, lockPassword string) error {
	if err := validateRecoveryPassword(recoveryPassword); err != nil {
		return err
	}
	secondaryHash, err := newPasswordHash(secondaryPassword)
	if err != nil {
		return err
	}
	var lockHash string
	if lockPassword != "" {
		lockHash, err = newPasswordHash(lockPassword)
		if err != nil {
			return err
		}
	}
	cfg, err := g.store.GetConfig()
	if err != nil {
		return err
	}
	if cfg.Security.SecondaryPasswordHash != "" {
		return errors.New(g.t("startup_page.err_secondary_initialized", nil))
	}
	if cfg.Security.LockPasswordHash != "" {
		return errors.New(g.t("startup_page.err_lock_initialized", nil))
	}
	hasRecovery, err := HasRecoveryPassword(g.dataDir)
	if err != nil {
		return err
	}
	if hasRecovery {
		return errors.New(g.t("startup_page.err_recovery_initialized", nil))
	}
	if err := SetRecoveryPassword(g.dataDir, g.store.DBKey(), recoveryPassword); err != nil {
		return err
	}
	cfg.Security.SecondaryPasswordHash = secondaryHash
	cfg.Security.LockPasswordHash = lockHash
	if err := g.store.SaveConfig(cfg); err != nil {
		removeErr := os.Remove(activeDatabaseFile(g.dataDir, databaseRecoveryFileName))
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return errors.Join(fmt.Errorf("save credentials: %w", err), fmt.Errorf("rollback recovery password: %w", removeErr))
		}
		return fmt.Errorf("save credentials: %w", err)
	}
	return nil
}

func writeStartupGateJSON(w http.ResponseWriter, ok bool, message string) {
	if !ok {
		writeStandaloneError(w, http.StatusBadRequest, "startup_failed", message)
		return
	}
	writeStandaloneData(w, map[string]any{})
}

func writeStandaloneData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeStandaloneError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

var startupGateTemplate = template.Must(template.New("startup-gate").Funcs(templateFuncMap()).Parse(`<!doctype html>
<html lang="{{.Lang}}" data-theme-mode="{{.ThemeMode}}" data-p-light="{{.PrimaryLight}}" data-p-light-hover="{{.PrimaryLightHover}}" data-p-dark="{{.PrimaryDark}}" data-p-dark-hover="{{.PrimaryDarkHover}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title><link rel="stylesheet" href="/startup.css"></head><body><main class="main" data-mode="{{.Mode}}" data-min-recovery-runes="{{.MinRecoveryRunes}}" data-max-recovery-bytes="{{.MaxRecoveryBytes}}" data-min-secondary-runes="{{.MinSecondaryRunes}}" data-max-secondary-bytes="{{.MaxSecondaryBytes}}">
<div class="shell">
<div class="intro">
<div class="head">
<div class="head-text">
<p class="brand"><span class="brand-mark" aria-hidden="true">CV</span>ClipVault</p>
<h1>{{.Title}}</h1>
<p class="desc">{{.Description}}</p>
</div>
{{if eq .Mode "set_credentials"}}
<ol class="steps">
<li class="is-current" id="step-recovery"><span>{{T "startup_page.step_recovery"}}</span></li>
<li id="step-secondary"><span>{{T "startup_page.step_secondary"}}</span></li>
<li id="step-lock"><span>{{T "startup_page.step_lock"}}</span></li>
<li id="step-finish"><span>{{T "startup_page.step_finish"}}</span></li>
</ol>
{{end}}
</div>
</div>
<section class="panel">
{{if eq .Mode "set_credentials"}}
<form id="recovery-form" class="credential-form" novalidate>
<input type="text" name="username" value="ClipVault recovery password" autocomplete="username" hidden>
<div class="section">
<div class="section-info"><h2 class="section-title">{{T "startup_page.step_recovery"}}</h2><p class="section-hint">{{T "startup_page.recovery_hint"}}</p></div>
<div class="section-fields">
<div class="field"><label for="password">{{T "startup_page.recovery_label"}}</label><div class="wrap"><input id="password" name="password" type="password" autocomplete="new-password" required autofocus><button type="button" class="eye" data-target="password">{{T "startup.show"}}</button></div><div class="msg" id="msg-password"></div></div>
<div class="field"><label for="confirm">{{T "startup_page.recovery_confirm_label"}}</label><div class="wrap"><input id="confirm" name="password-confirmation" type="password" autocomplete="new-password" required><button type="button" class="eye" data-target="confirm">{{T "startup.show"}}</button></div><div class="msg" id="msg-confirm"></div></div>
</div>
</div>
</form>
<form id="secondary-form" class="credential-form" novalidate>
<input type="text" name="username" value="ClipVault secondary password" autocomplete="username" hidden>
<div class="section">
<div class="section-info"><h2 class="section-title">{{T "startup_page.step_secondary"}}</h2><p class="section-hint">{{T "startup_page.secondary_hint"}}</p></div>
<div class="section-fields">
<div class="field"><label for="secondaryPassword">{{T "startup_page.secondary_label"}}</label><div class="wrap"><input id="secondaryPassword" name="password" type="password" autocomplete="new-password" required><button type="button" class="eye" data-target="secondaryPassword">{{T "startup.show"}}</button></div><div class="msg" id="msg-secondaryPassword"></div></div>
<div class="field"><label for="secondaryConfirm">{{T "startup_page.secondary_confirm_label"}}</label><div class="wrap"><input id="secondaryConfirm" name="password-confirmation" type="password" autocomplete="new-password" required><button type="button" class="eye" data-target="secondaryConfirm">{{T "startup.show"}}</button></div><div class="msg" id="msg-secondaryConfirm"></div></div>
</div>
</div>
</form>
<form id="lock-form" class="credential-form" novalidate>
<input type="text" name="username" value="ClipVault lock password" autocomplete="username" hidden>
<div class="section">
<div class="section-info"><h2 class="section-title">{{T "startup_page.lock_title"}}</h2><p class="section-hint">{{T "startup_page.lock_hint"}}</p></div>
<div class="section-fields">
<div class="field"><label for="lockPassword">{{T "startup_page.lock_label"}}</label><div class="wrap"><input id="lockPassword" name="password" type="password" autocomplete="new-password"><button type="button" class="eye" data-target="lockPassword">{{T "startup.show"}}</button></div><div class="msg" id="msg-lockPassword"></div></div>
<div class="field"><label for="lockConfirm">{{T "startup_page.lock_confirm_label"}}</label><div class="wrap"><input id="lockConfirm" name="password-confirmation" type="password" autocomplete="new-password"><button type="button" class="eye" data-target="lockConfirm">{{T "startup.show"}}</button></div><div class="msg" id="msg-lockConfirm"></div></div>
</div>
</div>
</form>
<div class="warn"><p class="warn-title">{{T "startup_page.warn_title"}}</p><p class="warn-copy">{{T "startup_page.warn_copy"}}</p></div>
{{else}}
<form id="recovery-form" class="credential-form" novalidate>
<input type="text" name="username" value="ClipVault recovery password" autocomplete="username" hidden>
<div class="section">
<div class="section-info"><h2 class="section-title">{{T "startup_page.verify_title"}}</h2><p class="section-hint">{{T "startup_page.verify_hint"}}</p></div>
<div class="section-fields is-single">
<div class="field is-full"><label for="password">{{T "startup_page.recovery_label"}}</label><div class="wrap"><input id="password" name="password" type="password" autocomplete="current-password" required autofocus><button type="button" class="eye" data-target="password">{{T "startup.show"}}</button></div><div class="msg" id="msg-password"></div></div>
</div>
</div>
</form>
{{end}}
<div class="actions">
<button type="button" class="submit" id="submit" disabled>{{.Button}}</button>
<div class="error" id="error" role="alert"></div>
<p class="local-note">{{T "startup_page.local_note"}}</p>
</div>
</section>
</div>
</main><script src="/startup.js" defer></script></body></html>`))
