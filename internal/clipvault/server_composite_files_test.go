package clipvault

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func saveCompositeFileRecord(t *testing.T, store *Store, paths []string, sensitive bool) int64 {
	t.Helper()
	snapshot, err := buildCompositeSnapshot(DefaultConfig(), &ClipboardSnapshot{Format: domain.FormatFileDrop, Text: strings.Join(paths, "\n")},
		nil, map[string][]byte{compositeText: []byte(`C:\not-a-recorded-file.txt`)}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Format != domain.FormatComposite {
		t.Fatalf("expected composite file fixture, got %s", snapshot.Format)
	}
	ctx := context.Background()
	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format,
		Text: snapshot.Text, Content: snapshot.Content, Sensitive: sensitive}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := store.db.QueryRowContext(ctx, "SELECT MAX(id) FROM clipboard_items").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCompositeFileOperationsUseRecordedRepresentation(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "image.png"), filepath.Join(dir, "report.pdf"), filepath.Join(dir, "missing.txt")}
	payloads := []string{"png contents", "pdf contents"}
	for i, body := range payloads {
		if err := os.WriteFile(paths[i], []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id := saveCompositeFileRecord(t, store, paths, false)
	server := newFreeTierTestServer(t, store, newUnlicensedManager(t))
	for i, path := range paths {
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/?item_id=%d&file_index=%d", id, i), nil)
		got, sensitive, err := server.resolveRecordedFile(request)
		if err != nil || got != path || sensitive {
			t.Fatalf("file %d resolved %q, sensitive=%v, err=%v", i, got, sensitive, err)
		}
		check := httptest.NewRecorder()
		server.handleFileCheck(check, request)
		var metadata struct{ Exists bool }
		if err := json.Unmarshal(check.Body.Bytes(), &metadata); err != nil {
			t.Fatal(err)
		}
		if check.Code != http.StatusOK || metadata.Exists != (i < len(payloads)) {
			t.Fatalf("check %d: %d %s", i, check.Code, check.Body.String())
		}
		if i == len(payloads) {
			continue
		}
		for _, download := range []bool{false, true} {
			serve := httptest.NewRecorder()
			req := request.Clone(request.Context())
			if download {
				req.URL.RawQuery += "&download=1"
			}
			server.handleFileServe(serve, req)
			if serve.Code != http.StatusOK || serve.Body.String() != payloads[i] {
				t.Fatalf("content %d: %d %s", i, serve.Code, serve.Body.String())
			}
			if attachment := strings.HasPrefix(serve.Header().Get("Content-Disposition"), "attachment"); attachment != (download || i == 1) {
				t.Fatalf("unexpected disposition: %v", serve.Header())
			}
		}
		hash := httptest.NewRecorder()
		server.handleFileHash(hash, request)
		var hashes struct{ MD5, SHA256 string }
		if err := json.Unmarshal(hash.Body.Bytes(), &hashes); err != nil {
			t.Fatal(err)
		}
		if hash.Code != http.StatusOK || hashes.MD5 != fmt.Sprintf("%x", md5.Sum([]byte(payloads[i]))) || hashes.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(payloads[i]))) {
			t.Fatalf("hash %d: %d %s", i, hash.Code, hash.Body.String())
		}
	}
	for _, index := range []int{-1, len(paths)} {
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/?item_id=%d&file_index=%d", id, index), nil)
		if _, _, err := server.resolveRecordedFile(request); err == nil {
			t.Fatalf("accepted index %d outside canonical file representation", index)
		}
	}
}

func TestCompositeFileAuthorizationAndFreeWindow(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := saveCompositeFileRecord(t, store, []string{path}, true)
	server, auth, session := newMaskTestServer(t, store)
	defer auth.Close()
	otherSession, err := auth.CreateSession(10)
	if err != nil {
		t.Fatal(err)
	}
	request := func(token string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/?item_id=%d&file_index=0", id), nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		r.Header.Set("X-ClipVault-Secondary-Token", token)
		return r
	}
	for name, handler := range map[string]http.HandlerFunc{"check": server.handleFileCheck, "content": server.handleFileServe,
		"hash": server.handleFileHash, "directory": server.handleFileOpenDir} {
		t.Run(name, func(t *testing.T) {
			for _, boundSession := range []string{"", otherSession} {
				token := ""
				if boundSession != "" {
					token, err = auth.GenerateSecondaryToken(boundSession)
					if err != nil {
						t.Fatal(err)
					}
				}
				w := httptest.NewRecorder()
				handler(w, request(token))
				if w.Code != http.StatusForbidden {
					t.Fatalf("unauthorized response: %d %s", w.Code, w.Body.String())
				}
			}
			// Do not launch Explorer from an automated test.
			if name == "directory" {
				return
			}
			token, err := auth.GenerateSecondaryToken(session)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []int{http.StatusOK, http.StatusForbidden} {
				w := httptest.NewRecorder()
				handler(w, request(token))
				if w.Code != want {
					t.Fatalf("one-time response: %d %s, want %d", w.Code, w.Body.String(), want)
				}
			}
		})
	}
	server.license = newUnlicensedManager(t)
	texts := make([]string, FreeTierVisibleLimit)
	for i := range texts {
		texts[i] = fmt.Sprintf("newer %d", i)
	}
	saveFreeTierItems(t, store, texts)
	for _, handler := range []http.HandlerFunc{server.handleFileCheck, server.handleFileServe, server.handleFileHash, server.handleFileOpenDir} {
		w := httptest.NewRecorder()
		handler(w, request(""))
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("free-window response: %d %s", w.Code, w.Body.String())
		}
	}
	server.license = nil
	if got, sensitive, err := server.resolveRecordedFile(request("")); err != nil || got != path || !sensitive {
		t.Fatalf("ungated lookup: %q %v %v", got, sensitive, err)
	}
	server.license = newUnlicensedManager(t)
	id = saveCompositeFileRecord(t, store, []string{path}, false)
	if err := store.Save(context.Background(), ClipboardItem{RecordType: RecordTypeEvent, EventCode: domain.EventClipboardEmpty}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.handleFileCheck(w, request(""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"exists":true`) {
		t.Fatalf("visible file response: %d %s", w.Code, w.Body.String())
	}
}
