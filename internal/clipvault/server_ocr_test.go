package clipvault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
	"clipboard_ext/internal/clipvault/ocr"
)

func TestOCRHandlerCacheEditsAndAuthorization(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := compositeFixture(t, `C:\temp\ocr.png`)
	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, MetaJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	item := items[0]
	images, err := itemOCRImages(item, Environment{DataDir: store.dataDir})
	if err != nil {
		t.Fatal(err)
	}
	var selected ocrImage
	for _, candidate := range images {
		if len(candidate.data) > 0 {
			selected = candidate
			break
		}
	}
	_, hash, err := ocr.Prepare(selected.data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ocrProfile{ID: newOCRID(), Name: "测试百炼", Config: ocr.DefaultConfig(ocr.Bailian)}
	raw, _ := json.Marshal(ocrSettings{Version: 1, ActiveID: cfg.ID, Profiles: []ocrProfile{cfg}})
	if _, err := store.db.Exec(`INSERT INTO settings(key,value) VALUES ('ocr',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	edited := "保留的用户修订"
	record := domain.ImageOCR{ImageID: selected.ID, ImageHash: hash, CacheKey: cfg.cacheKey(hash), Original: "模型原文", Edited: &edited, Model: cfg.Model, RecognizedAt: time.Now().UnixMilli()}
	if err := store.updateImageOCR(ctx, item.ID, func([]domain.ImageOCR) ([]domain.ImageOCR, error) { return []domain.ImageOCR{record}, nil }); err != nil {
		t.Fatal(err)
	}
	server, _, session := newMaskTestServer(t, store)
	server.env.DataDir = store.dataDir
	url := "http://clipvault.local/api/v1/items/ocr?id=" + strconv.FormatInt(item.ID, 10) + "&image=" + selected.ID
	invoke := func(method, body string) *httptest.ResponseRecorder {
		if method == "POST" {
			var input map[string]any
			_ = json.Unmarshal([]byte(body), &input)
			input["connectionId"], input["fingerprint"] = cfg.ID, cfg.fingerprint()
			raw, _ := json.Marshal(input)
			body = string(raw)
		}
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		w := httptest.NewRecorder()
		server.handleOCR(w, r)
		return w
	}
	if w := invoke("POST", `{"consent":false}`); w.Code != 400 {
		t.Fatalf("consent: %d", w.Code)
	}
	if w := invoke("POST", `{"consent":true}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"cached":true`) {
		t.Fatalf("cached: %d %s", w.Code, w.Body.String())
	}
	if w := invoke("POST", `{"consent":true,"force":true}`); w.Code != 502 {
		t.Fatalf("missing key: %d", w.Code)
	}
	stored, _ := store.GetByID(ctx, item.ID)
	if stored.OCR[0].Text() != edited || stored.OCR[0].Original != record.Original {
		t.Fatal("retry replaced previous results")
	}
	if w := invoke("PATCH", `{"text":""}`); w.Code != 200 {
		t.Fatalf("empty edit: %d %s", w.Code, w.Body.String())
	}
	stored, _ = store.GetByID(ctx, item.ID)
	if stored.OCR[0].Edited == nil || stored.OCR[0].Text() != "" {
		t.Fatal("explicit blank edit lost")
	}
	server.ocrMu.Lock()
	server.ocrTasks[item.ID] = ocrTask{Running: true}
	server.ocrMu.Unlock()
	if w := invoke("POST", `{"consent":true}`); w.Code != 409 {
		t.Fatalf("duplicate request: %d", w.Code)
	}
	if _, err := store.db.Exec(`UPDATE clipboard_items SET sensitive=true WHERE id=?`, item.ID); err != nil {
		t.Fatal(err)
	}
	if w := invoke("GET", ""); w.Code != 403 {
		t.Fatalf("sensitive access: %d", w.Code)
	}
}

func TestOCRRevisionForcesFullBackupWithoutLosingConcurrentEdits(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: "测试"}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	wm := BackupWatermark{LastItemID: items[0].ID, LastFullAt: time.Now().UTC().Format(time.RFC3339)}
	if err := store.SetBackupWatermark(ctx, "dir", wm); err != nil {
		t.Fatal(err)
	}
	change := func() {
		t.Helper()
		if err := store.updateImageOCR(ctx, items[0].ID, func(records []domain.ImageOCR) ([]domain.ImageOCR, error) { return records, nil }); err != nil {
			t.Fatal(err)
		}
	}
	change()
	scope, fallback, err := resolveBackupScope(store, "dir", ExportScope{Mode: "incremental"}, 7, false)
	if err != nil || scope.Mode != "full" || !fallback {
		t.Fatalf("scope: %+v %v %v", scope, fallback, err)
	}
	change()
	if _, err := updateBackupWatermark(store, "dir", scope, items[0].ID); err != nil {
		t.Fatal(err)
	}
	next, fallback, err := resolveBackupScope(store, "dir", ExportScope{Mode: "incremental"}, 7, false)
	if err != nil || next.Mode != "full" || !fallback {
		t.Fatal("concurrent edit was acknowledged without backup")
	}
	if _, err := updateBackupWatermark(store, "dir", next, items[0].ID); err != nil {
		t.Fatal(err)
	}
	next, _, err = resolveBackupScope(store, "dir", ExportScope{Mode: "incremental"}, 7, false)
	if err != nil || next.Mode != "incremental" {
		t.Fatalf("unchanged revision: %+v %v", next, err)
	}
}
