package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"clipboard_ext/internal/clipvault/ocr"
)

func TestOCRDraftTestDoesNotSaveConfiguration(t *testing.T) {
	headers := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"code":100,"data":"Synthetic draft test"}`))
	}))
	defer upstream.Close()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, _, _ := newMaskTestServer(t, store)
	ctx := context.Background()
	_, revision, err := store.readOCRSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	draft := ocrConfigInput{Revision: revision, Config: ocr.DefaultConfig(ocr.Umi), APIKey: "synthetic-draft-key"}
	draft.BaseURL = upstream.URL
	invoke := func(d ocrConfigInput) int {
		raw, _ := json.Marshal(map[string]any{"draft": d})
		w := httptest.NewRecorder()
		server.handleOCRTest(w, httptest.NewRequest("POST", "/api/v1/ocr/test", bytes.NewReader(raw)))
		return w.Code
	}
	if code := invoke(draft); code != 200 {
		t.Fatalf("new draft test: %d", code)
	}
	if <-headers != "Bearer synthetic-draft-key" {
		t.Fatal("draft credential was not used")
	}
	cfg, after, err := store.readOCRSettings(ctx)
	if err != nil || after != revision || len(cfg.Profiles) != 0 {
		t.Fatal("test persisted a draft")
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM clipboard_items`).Scan(&count); err != nil || count != 0 {
		t.Fatal("test persisted history")
	}

	draft.Name = "Saved test connection"
	p, err := makeOCRProfile(draft, nil, -1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.saveOCRSettings(ctx, ocrSettings{Version: 1, ActiveID: p.ID, Profiles: []ocrProfile{p}}); err != nil {
		t.Fatal(err)
	}
	_, revision, err = store.readOCRSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	draft.ID, draft.Revision, draft.APIKey = p.ID, revision, ""
	draft.TimeoutSeconds++ // Test an unsaved edit while reusing the saved credential.
	if code := invoke(draft); code != 200 {
		t.Fatalf("edited draft test: %d", code)
	}
	if <-headers != "Bearer synthetic-draft-key" {
		t.Fatal("unchanged target lost saved credential")
	}
	cfg, after, err = store.readOCRSettings(ctx)
	if err != nil || after != revision || cfg.Profiles[0].TimeoutSeconds != p.TimeoutSeconds {
		t.Fatal("test saved edited parameters")
	}
	draft.RequestPath = "/other-target"
	if code := invoke(draft); code != 400 {
		t.Fatalf("credential forwarded to changed target: %d", code)
	}
	if len(headers) != 0 {
		t.Fatal("rejected request reached upstream")
	}
	draft.ClearKey = true
	if code := invoke(draft); code != 200 {
		t.Fatalf("explicitly cleared draft: %d", code)
	}
	if <-headers != "" {
		t.Fatal("clear-key draft forwarded saved credential")
	}
	_, after, err = store.readOCRSettings(ctx)
	if err != nil || after != revision {
		t.Fatal("clear-key test changed settings")
	}
	draft.Revision = "stale"
	if code := invoke(draft); code != 409 {
		t.Fatalf("stale draft accepted: %d", code)
	}
	draft.Revision = revision
	draft.Config = ocr.DefaultConfig(ocr.Bailian)
	if code := invoke(draft); code != 400 {
		t.Fatalf("cloud draft without consent: %d", code)
	}
	w := httptest.NewRecorder()
	server.handleOCRTest(w, httptest.NewRequest("POST", "/api/v1/ocr/test", bytes.NewBufferString(`{"connectionId":"old","fingerprint":"old"}`)))
	if w.Code != 400 {
		t.Fatal("old test request contract was accepted")
	}
}
