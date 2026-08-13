package clipvault

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// RecoveryServer is a minimal HTTP server for database recovery.
type RecoveryServer struct {
	dataDir  string
	server   *http.Server
	url      string
	done     chan struct{}
	success  bool
	token    string
	port     int
	once     sync.Once
	opMu     sync.Mutex
	complete bool
	timer    *time.Timer
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
	mux.HandleFunc("GET /appearance", rs.handleAppearance)
	mux.HandleFunc("GET /appearance/logo", rs.handleAppearanceLogo)
	mux.HandleFunc("GET /appearance/favicon", rs.handleAppearanceFavicon)
	mux.HandleFunc("GET /appearance/background", rs.handleAppearanceBackground)
	mux.HandleFunc("POST /api/recovery/recover", rs.handleRecover)
	mux.HandleFunc("POST /api/recovery/new", rs.handleNew)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("recovery server bind: %w", err)
	}
	rs.port = ln.Addr().(*net.TCPAddr).Port
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
		if err := rs.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			rs.fail()
		}
	}()
	rs.timer = time.AfterFunc(15*time.Minute, func() {
		rs.fail()
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

func (rs *RecoveryServer) fail() {
	rs.opMu.Lock()
	defer rs.opMu.Unlock()
	if rs.complete {
		return
	}
	rs.complete = true
	rs.finish(false)
}

func (rs *RecoveryServer) secureHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src 'self'; media-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
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

func (rs *RecoveryServer) handleAppearance(w http.ResponseWriter, r *http.Request) {
	appearance, err := ReadRecoveryAppearance(rs.dataDir)
	if err != nil {
		appearance = defaultRecoveryAppearance()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(appearance)
}

func (rs *RecoveryServer) handleAppearanceLogo(w http.ResponseWriter, r *http.Request) {
	rs.serveAppearanceAsset(w, r, "logo")
}

func (rs *RecoveryServer) handleAppearanceFavicon(w http.ResponseWriter, r *http.Request) {
	rs.serveAppearanceAsset(w, r, "favicon")
}

func (rs *RecoveryServer) handleAppearanceBackground(w http.ResponseWriter, r *http.Request) {
	rs.serveAppearanceAsset(w, r, "background")
}

func (rs *RecoveryServer) serveAppearanceAsset(w http.ResponseWriter, r *http.Request, kind string) {
	appearance, err := ReadRecoveryAppearance(rs.dataDir)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	contentType, fileName := "", ""
	version := int64(0)
	switch kind {
	case "logo":
		contentType, fileName = appearance.Brand.LogoContentType, brandLogoAsset
		version = appearance.Brand.LogoVersion
		if !isBrandContentType(contentType, kind) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	case "favicon":
		contentType, fileName = appearance.Brand.FaviconContentType, brandFaviconAsset
		version = appearance.Brand.FaviconVersion
		if !isBrandContentType(contentType, kind) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	case "background":
		contentType, fileName = appearance.Background.ContentType, backgroundMediaAsset
		version = appearance.Background.Version
		if !isBackgroundContentType(contentType) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !assetVersionMatches(r, version) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	fileName = assetFileName(fileName, version)
	file, err := os.Open(filepath.Join(rs.dataDir, fileName))
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, fileName, time.Time{}, file)
}

func (rs *RecoveryServer) handleRecover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSONRequest(w, r, &req, 8<<10); err != nil {
		writeRecoveryJSON(w, false, "请求格式无效")
		return
	}
	rs.opMu.Lock()
	defer rs.opMu.Unlock()
	if rs.complete {
		writeRecoveryJSON(w, false, "恢复操作已完成")
		return
	}
	if err := RecoverWithPassword(rs.dataDir, req.Password); err != nil {
		writeRecoveryJSON(w, false, "恢复失败：密码不正确，或恢复文件已经损坏")
		return
	}
	rs.complete = true
	writeRecoveryJSON(w, true, "")
	rs.finish(true)
}

func (rs *RecoveryServer) handleNew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSONRequest(w, r, &req, 8<<10); err != nil || strings.TrimSpace(req.Confirmation) != "CREATE NEW" {
		writeRecoveryJSON(w, false, "确认文字不匹配")
		return
	}
	rs.opMu.Lock()
	defer rs.opMu.Unlock()
	if rs.complete {
		writeRecoveryJSON(w, false, "恢复操作已完成")
		return
	}
	if err := CreateNewDatabase(rs.dataDir); err != nil {
		writeRecoveryJSON(w, false, "创建新数据库失败，原数据未被覆盖")
		return
	}
	rs.complete = true
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
