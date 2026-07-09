package clipvault

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

const recoveryHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ClipVault - Data Recovery</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #1a1a2e; color: #e0e0e0; display: flex; justify-content: center; align-items: center; min-height: 100vh; }
.card { background: #16213e; border-radius: 12px; padding: 40px; max-width: 480px; width: 90%; box-shadow: 0 8px 32px rgba(0,0,0,0.4); }
h1 { font-size: 22px; margin-bottom: 8px; color: #e94560; }
.desc { font-size: 14px; color: #a0a0b0; margin-bottom: 24px; line-height: 1.6; }
label { display: block; font-size: 13px; color: #a0a0b0; margin-bottom: 6px; }
input[type="password"] { width: 100%; padding: 12px 16px; border: 1px solid #30475e; border-radius: 8px; background: #1a1a2e; color: #e0e0e0; font-size: 15px; margin-bottom: 16px; }
input[type="password"]:focus { outline: none; border-color: #e94560; }
.btn { width: 100%; padding: 12px; border: none; border-radius: 8px; font-size: 15px; cursor: pointer; margin-bottom: 12px; transition: opacity 0.2s; }
.btn:hover { opacity: 0.85; }
.btn-primary { background: #e94560; color: #fff; }
.btn-secondary { background: #30475e; color: #e0e0e0; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.divider { text-align: center; margin: 20px 0; color: #555; font-size: 13px; }
.msg { padding: 10px 14px; border-radius: 8px; font-size: 13px; margin-bottom: 16px; display: none; }
.msg.error { background: rgba(233,69,96,0.15); color: #e94560; display: block; }
.msg.success { background: rgba(46,204,113,0.15); color: #2ecc71; display: block; }
.warning { font-size: 12px; color: #e94560; margin-top: 16px; line-height: 1.5; }
</style>
</head>
<body>
<div class="card">
<h1>ClipVault Data Recovery</h1>
<p class="desc">The encryption key could not be loaded. This may happen after a password change, OS reinstall, or DPAPI corruption. Enter your recovery password to restore access, or create a new database.</p>

<div id="msg" class="msg"></div>

<div id="recover-section">
<label for="password">Recovery Password</label>
<input type="password" id="password" placeholder="Enter recovery password" autocomplete="off">
<button class="btn btn-primary" id="recoverBtn" onclick="doRecover()">Recover Database</button>
</div>

<div class="divider">— or —</div>

<button class="btn btn-secondary" id="newBtn" onclick="doNew()">Create New Database</button>
<p class="warning">Warning: Creating a new database will isolate old files. Previous clipboard data will be locked and inaccessible without the recovery password.</p>
</div>

<script>
function showMsg(text, type) {
    var el = document.getElementById('msg');
    el.textContent = text;
    el.className = 'msg ' + type;
}
function setLoading(btnId, loading) {
    document.getElementById(btnId).disabled = loading;
}

async function doRecover() {
    var pwd = document.getElementById('password').value;
    if (!pwd) { showMsg('Please enter recovery password', 'error'); return; }
    setLoading('recoverBtn', true);
    showMsg('Recovering...', 'success');
    try {
        var resp = await fetch('/api/recovery/recover', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({password: pwd})
        });
        var data = await resp.json();
        if (data.ok) {
            showMsg('Recovery successful! Restarting...', 'success');
            setTimeout(function() { location.reload(); }, 2000);
        } else {
            showMsg(data.error || 'Recovery failed', 'error');
            setLoading('recoverBtn', false);
        }
    } catch(e) {
        showMsg('Network error: ' + e.message, 'error');
        setLoading('recoverBtn', false);
    }
}

async function doNew() {
    if (!confirm('Create a new database? Old data will be isolated and locked.')) return;
    setLoading('newBtn', true);
    showMsg('Creating new database...', 'success');
    try {
        var resp = await fetch('/api/recovery/new', { method: 'POST' });
        var data = await resp.json();
        if (data.ok) {
            showMsg('New database created! Restarting...', 'success');
            setTimeout(function() { location.reload(); }, 2000);
        } else {
            showMsg(data.error || 'Failed', 'error');
            setLoading('newBtn', false);
        }
    } catch(e) {
        showMsg('Network error: ' + e.message, 'error');
        setLoading('newBtn', false);
    }
}
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", rs.handleIndex)
	mux.HandleFunc("POST /api/recovery/recover", rs.handleRecover)
	mux.HandleFunc("POST /api/recovery/new", rs.handleNew)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("recovery server bind: %w", err)
	}
	rs.url = "http://" + ln.Addr().String() + "/"
	rs.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = rs.server.Serve(ln)
	}()
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
	if rs.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = rs.server.Shutdown(ctx)
		cancel()
	}
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
		writeRecoveryJSON(w, false, "invalid request: "+err.Error())
		return
	}
	if req.Password == "" {
		writeRecoveryJSON(w, false, "password is required")
		return
	}
	if err := RecoverWithPassword(rs.dataDir, req.Password); err != nil {
		writeRecoveryJSON(w, false, err.Error())
		return
	}
	rs.success = true
	close(rs.done)
	writeRecoveryJSON(w, true, "")
}

func (rs *RecoveryServer) handleNew(w http.ResponseWriter, r *http.Request) {
	if err := CreateNewDatabase(rs.dataDir); err != nil {
		writeRecoveryJSON(w, false, err.Error())
		return
	}
	rs.success = true
	close(rs.done)
	writeRecoveryJSON(w, true, "")
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
