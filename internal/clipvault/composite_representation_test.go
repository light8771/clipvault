package clipvault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositePreviewPreservesFilesAlongsideText(t *testing.T) {
	for _, mode := range []string{"files", "text", "html"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			paths := []string{`C:\files\report.txt`, `C:\files\image.png`}
			raw := map[string][]byte{}
			if mode == "text" {
				raw[compositeText] = []byte("visible note")
			} else if mode == "html" {
				raw["HTML Format"] = []byte("<p>visible note</p>")
			}
			snapshot, err := buildCompositeSnapshot(DefaultConfig(), &ClipboardSnapshot{Format: domain.FormatFileDrop, Text: strings.Join(paths, "\n")}, nil, raw, nil, readCompositeImage)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content}); err != nil {
				t.Fatal(err)
			}
			items, err := exportAll(ctx, store.db)
			if err != nil {
				t.Fatal(err)
			}
			server, _, session := newMaskTestServer(t, store)
			request := httptest.NewRequest(http.MethodGet, "/?id="+strconv.FormatInt(items[0].ID, 10), nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			result := httptest.NewRecorder()
			server.handleCompositePreview(result, request)
			var body struct {
				HTML  string   `json:"html"`
				Text  string   `json:"text"`
				Files []string `json:"files"`
			}
			if result.Code != http.StatusOK {
				t.Fatalf("preview: %d %s", result.Code, result.Body.String())
			}
			if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(body.Files, paths) || strings.Contains(body.Text, paths[0]) {
				t.Fatalf("missing or duplicated file paths: %+v", body)
			}
			if mode != "files" && body.Text != "visible note" {
				t.Fatalf("lost text: %+v", body)
			}
			if mode == "html" && !strings.Contains(body.HTML, "visible note") {
				t.Fatal("lost rich text")
			}
		})
	}
}

func TestCompositeUnsupportedRTFObjectRetainsVisibleText(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"Rich Text Format": []byte(`{\rtf1 visible note{\object{\objdata 00}}}`)}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Text != "visible note" || snapshot.Content == nil || len(snapshot.Content.Issues) != 1 {
		t.Fatalf("lost readable text: %+v", snapshot)
	}
	for _, repr := range snapshot.Content.Representations {
		if repr.Format == compositeRTF {
			t.Fatal("unsupported object was retained")
		}
	}
}

func TestCompositePreviewBitmapFallback(t *testing.T) {
	ctx := context.Background()
	for _, embedded := range []bool{false, true} {
		t.Run(strconv.FormatBool(embedded), func(t *testing.T) {
			store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			cfg := DefaultConfig()
			cfg.Image.Enabled = true
			markup := "<p>visible text</p>"
			if embedded {
				markup += `<img src="C:\example.png">`
			}
			png := managedImageTestPNG(t)
			snapshot, err := buildCompositeSnapshot(cfg, nil, &ClipboardSnapshot{Format: domain.FormatImageFile, ImageBytes: png}, map[string][]byte{"HTML Format": []byte(markup)}, nil, func(string, int) ([]byte, error) { return png, nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content}); err != nil {
				t.Fatal(err)
			}
			items, err := exportAll(ctx, store.db)
			if err != nil {
				t.Fatal(err)
			}
			server, _, session := newMaskTestServer(t, store)
			request := httptest.NewRequest(http.MethodGet, "/?id="+strconv.FormatInt(items[0].ID, 10), nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			result := httptest.NewRecorder()
			server.handleCompositePreview(result, request)
			var body struct {
				HTML   string   `json:"html"`
				Images []string `json:"images"`
			}
			if result.Code != http.StatusOK {
				t.Fatalf("preview: %d %s", result.Code, result.Body.String())
			}
			if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body.HTML, "visible text") {
				t.Fatal("text disappeared")
			}
			want := 1
			if embedded {
				want = 0
			}
			if len(body.Images) != want {
				t.Fatalf("bitmap count=%d want=%d", len(body.Images), want)
			}
		})
	}
}

func TestCompositePreviewDoesNotExposeCombinedSearchText(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{
		compositeText:      []byte("one visible sentence"),
		"HTML Format":      []byte("<div><br></div>"),
		"Rich Text Format": []byte(`{\rtf1 one visible sentence\par alternate searchable detail}`),
	}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot.Text, "alternate searchable detail") {
		t.Fatal("alternate search text lost")
	}
	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	server, _, session := newMaskTestServer(t, store)
	request := httptest.NewRequest(http.MethodGet, "/?id="+strconv.FormatInt(items[0].ID, 10), nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	result := httptest.NewRecorder()
	server.handleCompositePreview(result, request)
	var body struct {
		HTML string `json:"html"`
		Text string `json:"text"`
	}
	if result.Code != http.StatusOK {
		t.Fatalf("preview: %d", result.Code)
	}
	if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Text != "one visible sentence" {
		t.Fatalf("search projection shown to user: %q", body.Text)
	}
	if body.HTML != "" {
		t.Fatalf("empty markup hides the text fallback: %q", body.HTML)
	}
}

func TestCompositePreviewDistinguishesEmptyMarkupFromImages(t *testing.T) {
	for _, tc := range []struct {
		name, markup, image string
		visible             bool
	}{
		{"empty tags", "<div><span></span><br></div>", "", false},
		{"whitespace", "<p> \n&nbsp;\t</p>", "", false},
		{"head only", "<html><head><title>metadata</title></head><body></body></html>", "", false},
		{"missing image", `<img src="clipvault-asset:example">`, "", false},
		{"image only", `<img src="clipvault-asset:example">`, "data:image/png;base64,AA==", true},
		{"text", "<p>visible</p>", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := &domain.CompositeContent{Representations: []domain.ContentRepresentation{{Format: compositeHTML, Data: []byte(tc.markup)}}}
			preview := compositePreviewHTML(content, func(string) string { return tc.image })
			if (preview != "") != tc.visible {
				t.Fatalf("unexpected preview: %q", preview)
			}
		})
	}
}
