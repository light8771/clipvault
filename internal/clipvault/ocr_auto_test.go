package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
	"clipboard_ext/internal/clipvault/ocr"
)

func autoTestServer(t *testing.T) (*Store, *SearchServer) {
	t.Helper()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server, _, _ := newMaskTestServer(t, store)
	server.env.DataDir = store.dataDir
	return store, server
}
func autoTestProfile(t *testing.T, store *Store, provider, base string) ocrProfile {
	t.Helper()
	p := ocrProfile{ID: newOCRID(), Name: "自动测试", Config: ocr.DefaultConfig(provider)}
	if provider == ocr.ChatCompletions {
		p.Model = "synthetic-vision"
	}
	if base != "" {
		p.BaseURL = base
	}
	if err := store.saveOCRSettings(context.Background(), ocrSettings{Version: 1, ActiveID: p.ID, Profiles: []ocrProfile{p}}); err != nil {
		t.Fatal(err)
	}
	return p
}
func autoRequest(s *SearchServer, method string, input any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	s.handleOCRAuto(w, httptest.NewRequest(method, "/api/v1/ocr/auto", bytes.NewReader(raw)))
	return w
}
func autoInput(t *testing.T, store *Store, p ocrProfile, enabled, consent bool) map[string]any {
	t.Helper()
	_, rev, err := store.readOCRAuto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"enabled": enabled, "consent": consent, "fingerprint": p.fingerprint(), "revision": rev}
}
func autoEnable(t *testing.T, store *Store, s *SearchServer, p ocrProfile) {
	t.Helper()
	w := autoRequest(s, "PUT", autoInput(t, store, p, true, true))
	if w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
}
func autoImage(t *testing.T, store *Store, sensitive bool) int64 {
	t.Helper()
	snapshot := compositeFixture(t, `C:\synthetic\unused.png`)
	id, err := store.saveWithID(context.Background(), ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, Sensitive: sensitive})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func autoWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("automatic OCR did not reach expected state")
}
func TestAutoOCRSettingsValidation(t *testing.T) {
	store, s := autoTestServer(t)
	p, rev, err := store.readOCRAuto(context.Background())
	if err != nil || p.Enabled {
		t.Fatal("must default off")
	}
	if w := autoRequest(s, "PUT", map[string]any{"enabled": true, "revision": rev}); w.Code != 400 {
		t.Fatal("accepted no default")
	}
	if w := autoRequest(s, "PUT", map[string]any{"revision": rev}); w.Code != 400 {
		t.Fatal("accepted missing enabled flag")
	}
	cloud := autoTestProfile(t, store, ocr.Bailian, "")
	if w := autoRequest(s, "PUT", autoInput(t, store, cloud, true, true)); w.Code != 400 {
		t.Fatal("accepted missing Bailian key")
	}
	cloud = autoTestProfile(t, store, ocr.ChatCompletions, "https://ocr.example/v1")
	if w := autoRequest(s, "PUT", autoInput(t, store, cloud, true, false)); w.Code != 400 {
		t.Fatal("accepted no cloud consent")
	}
	input := autoInput(t, store, cloud, true, true)
	input["fingerprint"] = "stale"
	if w := autoRequest(s, "PUT", input); w.Code != 409 {
		t.Fatal("accepted stale connection")
	}
	input = autoInput(t, store, cloud, true, true)
	input["revision"] = "stale"
	if w := autoRequest(s, "PUT", input); w.Code != 409 {
		t.Fatal("accepted stale policy")
	}
	input = autoInput(t, store, cloud, true, true)
	input["unknown"] = true
	if w := autoRequest(s, "PUT", input); w.Code != 400 {
		t.Fatal("accepted unknown input")
	}
	autoEnable(t, store, s, cloud)
	p, _, err = store.readOCRAuto(context.Background())
	if err != nil || !p.Enabled || p.Fingerprint != cloud.fingerprint() {
		t.Fatal("policy not saved")
	}
	if w := autoRequest(s, "PUT", autoInput(t, store, cloud, false, false)); w.Code != 200 {
		t.Fatal("disable should not require consent")
	}
	if w := autoRequest(s, "GET", nil); !strings.Contains(w.Body.String(), `"effective":false`) {
		t.Fatal("still effective")
	}
}

func TestAutoOCRConnectionChangeRequiresFreshConsent(t *testing.T) {
	store, s := autoTestServer(t)
	original := autoTestProfile(t, store, ocr.Umi, "")
	autoEnable(t, store, s, original)
	changed := original
	changed.TimeoutSeconds++
	settings := ocrSettings{Version: 1, ActiveID: original.ID, Profiles: []ocrProfile{changed}}
	if err := store.saveOCRSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if w := autoRequest(s, "GET", nil); !strings.Contains(w.Body.String(), `"effective":false`) {
		t.Fatal("changed parameters still enabled")
	}
	settings.Profiles[0] = original
	if err := store.saveOCRSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if w := autoRequest(s, "GET", nil); !strings.Contains(w.Body.String(), `"effective":false`) {
		t.Fatal("reverted connection silently resumed")
	}
	autoEnable(t, store, s, original)
	if w := autoRequest(s, "GET", nil); !strings.Contains(w.Body.String(), `"effective":true`) {
		t.Fatal("reconfirmation did not resume")
	}
}

func TestAutoOCRNewCaptureCacheAndSensitiveSkips(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":100,"data":"自动识别测试"}`))
	}))
	defer upstream.Close()
	store, s := autoTestServer(t)
	p := autoTestProfile(t, store, ocr.Umi, upstream.URL)
	old := autoImage(t, store, false)
	autoEnable(t, store, s, p)
	s.autoOCR = newAutoOCRWorker(s)
	defer s.autoOCR.stop()
	if calls.Load() != 0 {
		t.Fatal("startup scanned history")
	}
	id := autoImage(t, store, false)
	s.autoOCR.offer(id)
	autoWait(t, func() bool { item, _ := store.GetByID(context.Background(), id); return len(item.OCR) == 1 })
	item, _ := store.GetByID(context.Background(), id)
	if item.OCR[0].Original != "自动识别测试" || calls.Load() != 1 {
		t.Fatalf("result/cache %d %+v", calls.Load(), item.OCR)
	}
	item, _ = store.GetByID(context.Background(), old)
	if len(item.OCR) != 0 {
		t.Fatal("historical record processed")
	}
	duplicate := autoImage(t, store, false)
	s.autoOCR.offer(duplicate)
	autoWait(t, func() bool { item, _ := store.GetByID(context.Background(), duplicate); return len(item.OCR) == 1 })
	if calls.Load() != 1 {
		t.Fatal("duplicate image bypassed cache")
	}
	sensitive := autoImage(t, store, true)
	s.autoOCR.process(context.Background(), sensitive, p.fingerprint())
	item, _ = store.GetByID(context.Background(), sensitive)
	if len(item.OCR) != 0 {
		t.Fatal("sensitive item processed")
	}
	fileID, err := store.saveWithID(context.Background(), ClipboardItem{RecordType: RecordTypeContent, Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: `C:\synthetic\not-opened.png`})
	if err != nil {
		t.Fatal(err)
	}
	s.autoOCR.process(context.Background(), fileID, p.fingerprint())
	s.ocrMu.Lock()
	task := s.ocrTasks[fileID]
	s.ocrMu.Unlock()
	if task.message != nil || task.Running {
		t.Fatal("external image path was opened")
	}
}

func TestAutoOCRDisableCancelsAndDiscardsQueue(t *testing.T) {
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body any
		_ = json.NewDecoder(r.Body).Decode(&body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer upstream.Close()
	store, s := autoTestServer(t)
	p := autoTestProfile(t, store, ocr.Umi, upstream.URL)
	autoEnable(t, store, s, p)
	s.autoOCR = newAutoOCRWorker(s)
	defer s.autoOCR.stop()
	first := autoImage(t, store, false)
	s.autoOCR.offer(first)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request not started")
	}
	second := autoImage(t, store, false)
	s.autoOCR.offer(second)
	// A manual task must not overlap the active automatic task.
	if s.beginOCRTask(first) {
		t.Fatal("manual task overlapped")
	}
	w := autoRequest(s, "PUT", autoInput(t, store, p, false, false))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	autoEnable(t, store, s, p)
	autoWait(t, func() bool {
		s.ocrMu.Lock()
		defer s.ocrMu.Unlock()
		return !s.ocrTasks[first].Running && len(s.autoOCR.queue) == 0
	})
	if calls.Load() != 1 {
		t.Fatal("queued item survived disable/re-enable")
	}
	for _, id := range []int64{first, second} {
		item, _ := store.GetByID(context.Background(), id)
		if len(item.OCR) != 0 {
			t.Fatal("cancelled result was saved")
		}
	}
}

func TestAutoOCRFailurePreservesRecordAndManualCollision(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer upstream.Close()
	store, s := autoTestServer(t)
	p := autoTestProfile(t, store, ocr.Umi, upstream.URL)
	autoEnable(t, store, s, p)
	worker := &autoOCRWorker{server: s}
	id := autoImage(t, store, false)
	if !s.beginOCRTask(id) {
		t.Fatal("manual claim failed")
	}
	worker.process(context.Background(), id, p.fingerprint())
	if calls.Load() != 0 {
		t.Fatal("automatic request collided")
	}
	s.endOCRTask(id, nil)
	worker.process(context.Background(), id, p.fingerprint())
	item, err := store.GetByID(context.Background(), id)
	if err != nil || item.Content == nil || len(item.OCR) != 0 {
		t.Fatal("failed OCR damaged original")
	}
	s.ocrMu.Lock()
	task := s.ocrTasks[id]
	s.ocrMu.Unlock()
	if task.message == nil || task.Running || calls.Load() != 1 {
		t.Fatal("failure state missing or unexpected retry")
	}
	if _, err := store.db.Exec(`DELETE FROM clipboard_items WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	worker.process(context.Background(), id, p.fingerprint())
	if calls.Load() != 1 {
		t.Fatal("deleted item sent")
	}
}

func TestAutoOCRQueueIsBoundedAndDisabledIsNoOp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &autoOCRWorker{root: ctx, queue: make(chan autoOCRJob, 32)}
	w.offer(1)
	if len(w.queue) != 0 {
		t.Fatal("disabled queue accepted")
	}
	w.enabled = true
	for id := int64(1); id <= 1000; id++ {
		w.offer(id)
	}
	if len(w.queue) != 32 {
		t.Fatal("queue not bounded")
	}
}

func TestAutoOCRPerRecordImageLimit(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"code":101,"data":""}`))
	}))
	defer upstream.Close()
	store, s := autoTestServer(t)
	p := autoTestProfile(t, store, ocr.Umi, upstream.URL)
	autoEnable(t, store, s, p)
	html := "<html><body>"
	for i := 0; i < 10; i++ {
		html += fmt.Sprintf(`<img src="C:\synthetic\%d.png">`, i)
	}
	html += "</body></html>"
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	counter := byte(0)
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"HTML Format": []byte(html)}, nil, func(string, int) ([]byte, error) {
		counter++
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{counter, 80, 0, 255})
		var buf bytes.Buffer
		err := png.Encode(&buf, img)
		return buf.Bytes(), err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Content.Assets) != 10 {
		t.Fatal("fixture needs ten different images")
	}
	id, err := store.saveWithID(context.Background(), ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content})
	if err != nil {
		t.Fatal(err)
	}
	worker := &autoOCRWorker{server: s}
	worker.process(context.Background(), id, p.fingerprint())
	item, err := store.GetByID(context.Background(), id)
	if err != nil || len(item.OCR) != 8 || calls.Load() != 8 {
		t.Fatalf("limit failed: results=%d calls=%d err=%v", len(item.OCR), calls.Load(), err)
	}
	for _, record := range item.OCR {
		if !record.Empty || record.Original != "" {
			t.Fatal("empty recognition not retained")
		}
	}
}
