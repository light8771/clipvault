package clipvault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestHandleItemCopyTextExpandsManagedImagePath(t *testing.T) {
	dataDir := t.TempDir()
	storedPath := "images/2026/09/capture.png"
	fullPath := filepath.Join(dataDir, filepath.FromSlash(storedPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(context.Background(), ClipboardItem{
		RecordType:    RecordTypeContent,
		Kind:          "image",
		Format:        "CLIPVAULT_IMAGE_FILE",
		Text:          storedPath,
		ByteLen:       len("png"),
		ContentSHA256: hashString("png"),
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(context.Background(), SearchOptions{Limit: 1, IncludeSensitive: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("load saved item: items=%d err=%v", len(items), err)
	}

	server := &SearchServer{store: store, env: Environment{DataDir: dataDir}}
	result := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/items/copy-text?id="+strconv.FormatInt(items[0].ID, 10), nil)
	server.handleItemCopyText(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := fullPath
	if !samePath(body.Text, want) {
		t.Fatalf("copy text=%q, want absolute path %q", body.Text, want)
	}
}

func TestResolveManagedImagePathAcceptsOnlyCanonicalRelativeStorageFormat(t *testing.T) {
	dataDir := t.TempDir()
	server := &SearchServer{env: Environment{DataDir: dataDir}}
	valid := "images/2026/09/capture.png"
	got, err := resolveManagedImagePath(server.env, valid)
	if err != nil || !samePath(got, filepath.Join(dataDir, filepath.FromSlash(valid))) {
		t.Fatalf("canonical path rejected: path=%q err=%v", got, err)
	}
	for _, invalid := range []string{
		"2026/09/capture.png",
		"images/../../outside.png",
		"images/2026/../capture.png",
		`images/2026\capture.png`,
		filepath.Join(dataDir, "images", "capture.png"),
		" Images/2026/09/capture.png ",
	} {
		if _, err := resolveManagedImagePath(server.env, invalid); err == nil {
			t.Fatalf("non-canonical managed image path %q was accepted", invalid)
		}
	}
}

func TestBuildManagedImageStoredPathUsesCanonicalSlashFormat(t *testing.T) {
	got := buildManagedImageStoredPath("2026", "09", "capture.png")
	if got != "images/2026/09/capture.png" {
		t.Fatalf("stored path=%q", got)
	}
}
