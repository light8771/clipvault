package clipvault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"clipboard_ext/internal/clipvault/ocr"
)

func TestOCRConnectionSettingsIsolationAndReset(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, _, _ := newMaskTestServer(t, store)
	invoke := func(method string, input any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(input)
		w := httptest.NewRecorder()
		server.handleOCRConfig(w, httptest.NewRequest(method, "/api/v1/ocr/config", bytes.NewReader(raw)))
		return w
	}
	ctx := context.Background()
	_, revision, err := store.readOCRSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := ocrConfigInput{Config: ocr.DefaultConfig(ocr.Bailian), Name: "云端测试", Revision: revision, APIKey: "synthetic-key"}
	w := invoke("PUT", first)
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-key") || strings.Contains(w.Body.String(), "protectedKey") {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	cfg, revision, _ := store.readOCRSettings(ctx)
	p := cfg.Profiles[0]
	secret, err := ocrSecret(p)
	if err != nil || string(secret) != "synthetic-key" {
		t.Fatal("key not retained")
	}
	clear(secret)
	if p.ProtectedKey == "" || cfg.ActiveID != p.ID {
		t.Fatal("missing protected key or default")
	}
	if w = invoke("PUT", first); w.Code != 409 {
		t.Fatalf("stale save %d", w.Code)
	}
	first.Revision = revision
	first.ID = p.ID
	first.APIKey = ""
	first.BaseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
	if w = invoke("PUT", first); w.Code != 400 {
		t.Fatalf("reused key across endpoints %d", w.Code)
	}
	first.ClearKey = true
	if w = invoke("PUT", first); w.Code != 200 {
		t.Fatalf("clear key %d %s", w.Code, w.Body.String())
	}
	cfg, revision, _ = store.readOCRSettings(ctx)
	if cfg.Profiles[0].ProtectedKey != "" {
		t.Fatal("key not cleared")
	}
	if _, err = store.selectedOCR(ctx, p.ID, p.fingerprint()); err == nil {
		t.Fatal("accepted old consent fingerprint")
	}
	second := ocrConfigInput{Config: ocr.DefaultConfig(ocr.Umi), Name: "本机测试", Revision: revision, MakeActive: true}
	if w = invoke("PUT", second); w.Code != 200 {
		t.Fatalf("second save %d %s", w.Code, w.Body.String())
	}
	cfg, revision, _ = store.readOCRSettings(ctx)
	if len(cfg.Profiles) != 2 || cfg.Profiles[1].ProtectedKey != "" || cfg.ActiveID != cfg.Profiles[1].ID {
		t.Fatal("connection isolation failed")
	}
	clone := cfg.Profiles[0]
	clone.ID = cfg.Profiles[1].ID
	if cfg.Profiles[0].cacheKey("image") == clone.cacheKey("image") {
		t.Fatal("cache crosses profiles")
	}
	if w = invoke("DELETE", ocrConfigInput{ID: cfg.ActiveID, Revision: revision}); w.Code != 409 {
		t.Fatal("deleted active profile without replacement")
	}
	if w = invoke("DELETE", ocrConfigInput{ID: p.ID, Revision: revision}); w.Code != 200 {
		t.Fatalf("delete %d", w.Code)
	}
	if _, err = store.db.Exec(`UPDATE settings SET value='{"model":"obsolete"}' WHERE key='ocr'`); err != nil {
		t.Fatal(err)
	}
	if w = invoke("GET", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"needsReset":true`) {
		t.Fatal("legacy config not rejected")
	}
	_, revision, err = store.readOCRSettings(ctx)
	if err != errOCRSettings {
		t.Fatal(err)
	}
	second.Revision = revision
	if w = invoke("PUT", second); w.Code != 409 {
		t.Fatal("silently replaced old configuration")
	}
	if w = invoke("POST", ocrConfigInput{Revision: revision, Action: "reset"}); w.Code != 200 {
		t.Fatalf("explicit reset %d", w.Code)
	}
	cfg, _, err = store.readOCRSettings(ctx)
	if err != nil || len(cfg.Profiles) != 0 {
		t.Fatalf("reset %+v %v", cfg, err)
	}
}

func TestOCRConnectionSampleUsesSyntheticImage(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Base64 string `json:"base64"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
		}
		raw, err := base64.StdEncoding.DecodeString(body.Base64)
		if err != nil || !bytes.Equal(raw, ocrTestCard) {
			t.Error("test uploaded something other than built-in image")
		}
		if _, err = png.Decode(bytes.NewReader(raw)); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"code":100,"data":"ClipVault OCR TEST\n12345 ABC xyz"}`))
	}))
	defer upstream.Close()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, _, _ := newMaskTestServer(t, store)
	p := ocrProfile{ID: newOCRID(), Name: "Umi 测试", Config: ocr.DefaultConfig(ocr.Umi)}
	p.BaseURL = upstream.URL
	if err = store.saveOCRSettings(context.Background(), ocrSettings{Version: 1, ActiveID: p.ID, Profiles: []ocrProfile{p}}); err != nil {
		t.Fatal(err)
	}
	currentRevision := func() string {
		_, revision, err := store.readOCRSettings(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	invoke := func(revision string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"draft": ocrConfigInput{ID: p.ID, Revision: revision, Config: p.Config}})
		w := httptest.NewRecorder()
		server.handleOCRTest(w, httptest.NewRequest("POST", "/api/v1/ocr/test", bytes.NewReader(raw)))
		return w
	}
	if w := invoke("stale"); w.Code != 409 || calls.Load() != 0 {
		t.Fatalf("stale upload %d calls %d", w.Code, calls.Load())
	}
	if w := invoke(currentRevision()); w.Code != 200 || calls.Load() != 1 || !strings.Contains(w.Body.String(), "12345") {
		t.Fatalf("sample %d %s", w.Code, w.Body.String())
	}
	var count int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM clipboard_items`).Scan(&count); err != nil || count != 0 {
		t.Fatal("sample wrote history")
	}
	server.ocrLocalMu.Lock()
	if w := invoke(currentRevision()); w.Code != 502 || calls.Load() != 1 {
		t.Fatal("concurrent Umi request accepted")
	}
	server.ocrLocalMu.Unlock()
	p.Config = ocr.DefaultConfig(ocr.Bailian)
	if err = store.saveOCRSettings(context.Background(), ocrSettings{Version: 1, ActiveID: p.ID, Profiles: []ocrProfile{p}}); err != nil {
		t.Fatal(err)
	}
	if w := invoke(currentRevision()); w.Code != 400 {
		t.Fatal("cloud test did not require consent")
	}
}
