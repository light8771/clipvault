package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func compositeFixture(t *testing.T, path string) *ClipboardSnapshot {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 80, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	snapshot, err := buildCompositeSnapshot(cfg, &ClipboardSnapshot{Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "hello world"}, nil,
		map[string][]byte{"HTML Format": []byte(`<html><head><meta charset="utf-8"></head><body><div>hello<img src="` + path + `"><table><tr><td>world</td></tr></table><a href="https://example.com">link</a><img src="` + path + `"></div></body></html>`)}, nil,
		func(string, int) ([]byte, error) { return buf.Bytes(), nil })
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestCompositeCaptureAndIntegrity(t *testing.T) {
	a := compositeFixture(t, `C:\temp\first.png`)
	b := compositeFixture(t, `D:\different\second.png`)
	if a.SHA256 != b.SHA256 {
		t.Fatal("temporary filenames changed content identity")
	}
	if len(a.Content.Assets) != 1 {
		t.Fatal("repeated resource was not deduplicated")
	}
	if len(a.Content.Issues) != 0 {
		t.Fatalf("harmless metadata marked incomplete: %v", a.Content.Issues)
	}
	if !slices.Equal(a.Content.Kinds, []string{"image", "link", "table", "text"}) {
		t.Fatalf("kinds %v", a.Content.Kinds)
	}
	filter, err := NewFilterEngine(SensitiveConfig{CustomPatterns: []string{"link"}})
	if err != nil {
		t.Fatal(err)
	}
	if !filter.CheckComposite(a.Content).Sensitive {
		t.Fatal("alternate text missing from privacy matching")
	}
	preview := compositePreviewHTML(a.Content, func(string) string { return "data:image/png;base64,AA==" })
	if strings.Contains(preview, "first.png") || !strings.Contains(preview, "<table>") {
		t.Fatalf("preview %s", preview)
	}
	a.Content.Assets[0].Data[0] ^= 1
	if _, _, err := validateComposite(a.Content); err == nil {
		t.Fatal("accepted corrupted asset")
	}
}

func TestCompositePartialCaptureAndSanitization(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	raw := map[string][]byte{"HTML Format": []byte(`<div onclick="bad()">kept<script>bad()</script><img src="https://example.com/image.png" srcset="https://example.com/other.png"><iframe src="https://example.com"></iframe><span style="background:url(https://example.com)">text</span></div>`)}
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, raw, nil, func(string, int) ([]byte, error) { return nil, errors.New("missing") })
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Content.Issues) == 0 || !strings.Contains(snapshot.Text, "kept") {
		t.Fatal("partial capture lost text or diagnostics")
	}
	for _, r := range snapshot.Content.Representations {
		if r.Format == compositeHTML && (bytes.Contains(r.Data, []byte("https://")) || bytes.Contains(r.Data, []byte("onclick")) || bytes.Contains(r.Data, []byte("<script"))) {
			t.Fatalf("unsafe recovery HTML: %s", r.Data)
		}
	}
	if _, err := readCompositeImage("https://example.com/image.png", 1024); err == nil {
		t.Fatal("accepted network resource")
	}
}

func TestCompositeAlternativeRepresentationsAndDecodeFallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	plain := &ClipboardSnapshot{Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "text and bitmap"}
	bitmap := &ClipboardSnapshot{Kind: domain.KindImage, Format: domain.FormatImageFile, ImageBytes: managedImageTestPNG(t)}
	snapshot, err := buildCompositeSnapshot(cfg, plain, bitmap, nil, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Content.Representations) != 2 || !slices.Equal(snapshot.Content.Kinds, []string{"image", "text"}) {
		t.Fatalf("alternative formats lost: %+v", snapshot.Content)
	}
	snapshot, err = buildCompositeSnapshot(cfg, plain, nil, map[string][]byte{"Rich Text Format": []byte("invalid rtf")}, nil, readCompositeImage)
	if err != nil || snapshot.Text != plain.Text || len(snapshot.Content.Issues) != 1 {
		t.Fatalf("bad format discarded valid text: %+v %v", snapshot, err)
	}
	raw := map[string][]byte{
		"HTML Format":            []byte("Version:1.0\r\nStartHTML:99999\r\nEndHTML:100000\r\nStartFragment:99999\r\nEndFragment:100000\r\n"),
		"WeChat_RichEdit_Format": []byte(`<WeChatRichEditFormat><EditElement type="0">recovered</EditElement></WeChatRichEditFormat>`),
	}
	snapshot, err = buildCompositeSnapshot(cfg, nil, nil, raw, nil, readCompositeImage)
	if err != nil || snapshot == nil || snapshot.Text != "recovered" {
		t.Fatalf("application fallback after bad HTML: %+v %v", snapshot, err)
	}
}

func TestCompositeStoreSearchAndTransfers(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := compositeFixture(t, `C:\temp\image.png`)
	for i := 0; i < 2; i++ {
		if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, MetaJSON: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range snapshot.Content.Kinds {
		items, total, err := store.Search(ctx, SearchOptions{Kind: kind, Limit: 10})
		if err != nil || total != 2 || len(items) != 2 {
			t.Fatalf("filter %s: total %d err %v", kind, total, err)
		}
		if !items[0].ConsecutiveDuplicate {
			t.Fatal("composite duplicate missing")
		}
		for _, r := range items[0].Content.Representations {
			if len(r.Data) > 0 {
				t.Fatal("list loaded binary payload")
			}
		}
		condition, args, apiErr := dataAPIFilterFields["kind"].compileOp("kind", "eq", json.RawMessage(`"`+kind+`"`))
		if apiErr != nil {
			t.Fatal(apiErr)
		}
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM clipboard_items WHERE "+condition, args...).Scan(&count); err != nil || count != 2 {
			t.Fatalf("DSL %s: %d %v", kind, count, err)
		}
	}
	original, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(original[0].Content.Assets) != 1 {
		t.Fatal("full read lost resources")
	}
	full, err := store.getPublicClipboardItem(ctx, original[1].ID, 1)
	if err != nil || len(full.Content.Assets) != 1 {
		t.Fatalf("native restore did not load full content: %v", err)
	}
	if _, err := store.getPublicClipboardItem(ctx, original[0].ID, 1); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("native restore ignored latest window: %v", err)
	}
	svc := NewExternalReadService(store, Environment{DataDir: store.dataDir})
	stats, err := svc.Stats(ctx, ReadAccess{AllowSensitive: true}, StatsRequest{Bucket: "day"})
	if err != nil || stats.Total != 2 || len(stats.ByKind) != 4 {
		t.Fatalf("multi-kind stats: %+v %v", stats, err)
	}
	for _, bucket := range stats.ByKind {
		if bucket.Count != 2 {
			t.Fatalf("type membership counted incorrectly: %+v", bucket)
		}
	}
	export := exportAllForTest(t, store)
	jsonData, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	xlsx, err := store.ExportXLSX(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, importData := range map[string]func(*Store) error{
		"JSON":         func(s *Store) error { return s.ImportFullJSON(ctx, jsonData) },
		"XLSX":         func(s *Store) error { return s.ImportXLSX(ctx, xlsx) },
		"materialized": func(s *Store) error { return s.importExportData(ctx, *export) },
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
			items, err := exportAll(ctx, target.db)
			if err != nil || len(items) != 2 {
				t.Fatalf("roundtrip %d %v", len(items), err)
			}
			if compositeJSONString(items[0].Content) != compositeJSONString(original[0].Content) {
				t.Fatal("roundtrip changed recoverable content")
			}
		})
	}
}

func TestCompositeSensitivePreviewAndRestoreRequireAuthorization(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := compositeFixture(t, `C:\secret\image.png`)
	if err := store.Save(context.Background(), ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, Sensitive: true}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(context.Background(), store.db)
	if err != nil {
		t.Fatal(err)
	}
	server, _, session := newMaskTestServer(t, store)
	if _, err := store.getPublicClipboardItem(context.Background(), items[0].ID, 0); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("native restore exposed sensitive package: %v", err)
	}
	for name, handler := range map[string]http.HandlerFunc{"preview": server.handleCompositePreview, "restore": server.handleRestoreItem} {
		request := httptest.NewRequest(http.MethodPost, "/?id="+strconv.FormatInt(items[0].ID, 10), nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		result := httptest.NewRecorder()
		handler(result, request)
		if result.Code != http.StatusForbidden || strings.Contains(result.Body.String(), "hello") {
			t.Fatalf("%s bypass: %d %s", name, result.Code, result.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	result := httptest.NewRecorder()
	server.handleSearch(result, request)
	if result.Code != http.StatusOK || strings.Contains(result.Body.String(), "hello") || strings.Contains(result.Body.String(), "representations") || strings.Contains(result.Body.String(), snapshot.Content.Assets[0].ID) {
		t.Fatalf("masked composite leaked: %s", result.Body.String())
	}
}
