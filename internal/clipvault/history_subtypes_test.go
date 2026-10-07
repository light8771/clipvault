package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestHistorySubtypesFilterAndOptions(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	save := func(item ClipboardItem) {
		t.Helper()
		item.RecordType = RecordTypeContent
		if err := store.Save(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	save(ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "**Markdown is plain text**"})
	rich := compositeFixture(t, `C:\image.png`)
	save(ClipboardItem{Kind: rich.Kind, Format: rich.Format, Text: rich.Text, Content: rich.Content})
	save(ClipboardItem{Kind: "image", Format: "CF_HDROP", Text: "C:\\a.JPG\nC:\\b.png"})
	save(ClipboardItem{Kind: "files", Format: "CF_HDROP", Text: `C:\folder.png\README`})
	save(ClipboardItem{Kind: "files", Format: "CF_HDROP", Text: `C:\private.secret`, Sensitive: true})
	mixed, err := buildCompositeSnapshot(DefaultConfig(), &ClipboardSnapshot{Kind: "files", Format: "CF_HDROP", Text: "C:\\x.zip\nC:\\x.docx\nC:\\x.gif"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	save(ClipboardItem{Kind: mixed.Kind, Format: mixed.Format, Text: mixed.Text, Content: mixed.Content})
	for _, tc := range []struct {
		kind, subtype string
		count         int
	}{
		{"text", "plain", 1}, {"text", "rich", 1}, {"image", "ext:png", 2},
		{"image", "ext:jpg", 1}, {"image", "ext:gif", 1}, {"office", "ext:docx", 1},
		{"archive", "ext:zip", 1}, {"files", "ext:", 1}, {"files", "ext:secret", 0},
	} {
		items, count, err := store.Search(ctx, SearchOptions{Kind: tc.kind, Subtype: tc.subtype, Limit: 1})
		if err != nil || count != tc.count || len(items) != min(1, tc.count) {
			t.Fatalf("%s/%s: count=%d len=%d err=%v", tc.kind, tc.subtype, count, len(items), err)
		}
	}
	options, err := store.historySubtypeOptions(ctx, "files", false, 0)
	if err != nil || !slices.Equal(options, []string{"ext:"}) {
		t.Fatalf("public options: %v %v", options, err)
	}
	options, err = store.historySubtypeOptions(ctx, "files", true, 0)
	if err != nil || !slices.Contains(options, "ext:secret") {
		t.Fatalf("private options: %v %v", options, err)
	}
	options, err = store.historySubtypeOptions(ctx, "image", false, 1)
	if err != nil || !slices.Equal(options, []string{"ext:gif"}) {
		t.Fatalf("latest options: %v %v", options, err)
	}
	items, count, err := store.Search(ctx, SearchOptions{Kind: "image", Subtype: "ext:png", Offset: 1, Limit: 1})
	if err != nil || count != 2 || len(items) != 1 || items[0].Kind != "composite" {
		t.Fatalf("pagination: count=%d items=%v err=%v", count, items, err)
	}
	// The primary category must be exactly the union of its subtypes, including
	// mixed file drops whose representative kind belongs to another category.
	save(ClipboardItem{Kind: "files", Format: domain.FormatFileDrop, Text: "C:\\movie.mp4\nC:\\document.pdf"})
	for kind, want := range map[string]int{"text": 2, "image": 3, "video": 1, "archive": 1, "office": 2, "files": 1} {
		_, count, err := store.Search(ctx, SearchOptions{Kind: kind})
		if err != nil || count != want {
			t.Fatalf("primary %s: count=%d want=%d err=%v", kind, count, want, err)
		}
	}
}

func TestHistorySubtypeHTTPVisibilityAndValidation(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), ClipboardItem{RecordType: RecordTypeContent, Kind: "files", Format: domain.FormatFileDrop, Text: `C:\private.secret`, Sensitive: true}); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Security.SecondaryPasswordHash = "configured"
	server := &SearchServer{store: store, cfgMgr: &ConfigManager{cfg: cfg}, auth: NewAuthManager()}
	defer server.auth.Close()
	result := httptest.NewRecorder()
	server.requireAuth(server.handleHistorySubtypes)(result, httptest.NewRequest(http.MethodGet, "/api/v1/items/subtypes?kind=files", nil))
	if result.Code != 200 || strings.Contains(result.Body.String(), "secret") || result.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("sensitive menu leaked: %d %s", result.Code, result.Body.String())
	}
	for _, query := range []string{"kind=image&subtype=ext:zip", "subtype=rich", "kind=text&subtype=ext:md"} {
		result = httptest.NewRecorder()
		server.handleSearch(result, httptest.NewRequest(http.MethodGet, "/api/v1/items?"+query, nil))
		if result.Code != http.StatusBadRequest {
			t.Fatalf("accepted %s: %d", query, result.Code)
		}
	}
	token, err := server.auth.CreateSession(5)
	if err != nil {
		t.Fatal(err)
	}
	server.auth.GrantSecondaryAuth(token, 5)
	for _, readable := range []bool{true, false} {
		if !readable {
			server.auth.RevokeAllSecondaryAuth()
		}
		for _, endpoint := range []string{"/api/v1/items/subtypes?kind=files", "/api/v1/items?kind=files&subtype=ext:secret"} {
			request := httptest.NewRequest(http.MethodGet, endpoint, nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
			result := httptest.NewRecorder()
			if strings.Contains(endpoint, "/subtypes?") {
				server.requireAuth(server.handleHistorySubtypes)(result, request)
			} else {
				server.requireAuth(server.handleSearch)(result, request)
			}
			if result.Code != http.StatusOK || strings.Contains(result.Body.String(), "secret") != readable {
				t.Fatalf("readable=%v %s: %d %s", readable, endpoint, result.Code, result.Body.String())
			}
		}
	}
}

func TestHistoryContentTypesRTFImageRequiresPicture(t *testing.T) {
	content := &domain.CompositeContent{Kinds: []string{"image", "text"},
		Representations: []domain.ContentRepresentation{{Format: compositeRTF, Data: []byte(`{\rtf1 plain RTF}`)}, {Format: compositePNG, Data: []byte{1}}}}
	data, err := historyContentTypesJSON(domain.FormatComposite, "", content)
	if err != nil || stringsContainSubtype(t, data, "embedded") {
		t.Fatalf("unrelated RTF claimed image: %s %v", data, err)
	}
	content.Representations[0].Data = []byte(`{\rtf1{\pict\pngblip 00}}`)
	data, err = historyContentTypesJSON(domain.FormatComposite, "", content)
	if err != nil || !stringsContainSubtype(t, data, "embedded") {
		t.Fatalf("RTF picture missing: %s %v", data, err)
	}
}

func stringsContainSubtype(t *testing.T, data, subtype string) bool {
	t.Helper()
	var types []historyContentType
	if err := json.Unmarshal([]byte(data), &types); err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(types, func(entry historyContentType) bool { return entry.Subtype == subtype })
}

func TestHistorySubtypesSurviveBothImportPaths(t *testing.T) {
	ctx := context.Background()
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	rich := compositeFixture(t, `C:\image.png`)
	if err := source.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: rich.Kind, Format: rich.Format, Text: rich.Text, Content: rich.Content}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []ClipboardItem{
		{RecordType: RecordTypeContent, Kind: "text", Format: domain.FormatUnicodeText, Text: "**plain Markdown**"},
		{RecordType: RecordTypeContent, Kind: "files", Format: domain.FormatFileDrop, Text: "C:\\a.JPG\nC:\\a.zip\nC:\\a.docx\nC:\\a.mp4\nC:\\README"},
		{RecordType: RecordTypeContent, Kind: "files", Format: domain.FormatFileDrop, Text: `C:\long.` + strings.Repeat("字", 80)},
	} {
		if err := source.Save(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(exportAllForTest(t, source))
	if err != nil {
		t.Fatal(err)
	}
	fileOptions, err := source.historySubtypeOptions(ctx, "files", false, 0)
	if err != nil || !slices.Contains(fileOptions, "ext:"+strings.Repeat("字", 80)) {
		t.Fatalf("long extension missing: %v %v", fileOptions, err)
	}
	xlsx, err := source.ExportXLSX(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, importData := range map[string]func(*Store) error{
		"JSON": func(s *Store) error { return s.ImportFullJSONReader(ctx, bytes.NewReader(data)) },
		"XLSX": func(s *Store) error { return s.ImportXLSX(ctx, xlsx) },
	} {
		t.Run(name, func(t *testing.T) {
			target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if err := importData(target); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"text", "image", "video", "archive", "office", "files"} {
				want, err := source.historySubtypeOptions(ctx, kind, false, 0)
				if err != nil {
					t.Fatal(err)
				}
				got, err := target.historySubtypeOptions(ctx, kind, false, 0)
				if err != nil || !slices.Equal(want, got) || len(got) == 0 {
					t.Fatalf("%s options=%v want=%v err=%v", kind, got, want, err)
				}
				for _, subtype := range got {
					_, wantCount, err := source.Search(ctx, SearchOptions{Kind: kind, Subtype: subtype})
					if err != nil {
						t.Fatal(err)
					}
					_, count, err := target.Search(ctx, SearchOptions{Kind: kind, Subtype: subtype})
					if err != nil || count != wantCount || count == 0 {
						t.Fatalf("%s/%s count=%d want=%d err=%v", kind, subtype, count, wantCount, err)
					}
				}
			}
		})
	}
}
