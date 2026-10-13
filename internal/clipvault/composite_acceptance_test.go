package clipvault

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeKeepsImageOnlyRTF(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	rtf := []byte(`{\rtf1{\pict\pngblip ` + hex.EncodeToString(managedImageTestPNG(t)) + `}}`)
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"Rich Text Format": rtf}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Text != "" || !slices.Equal(snapshot.Content.Kinds, []string{"image"}) {
		t.Fatalf("image-only rich document lost: %+v", snapshot)
	}
	if _, err := extractRTFText(rtf); err == nil {
		t.Fatal("image-only RTF claimed plain text")
	}
	filter, err := NewFilterEngine(SensitiveConfig{CustomPatterns: []string{"^$"}})
	if err != nil {
		t.Fatal(err)
	}
	if filter.CheckComposite(snapshot.Content).Sensitive {
		t.Fatal("missing text was treated as an empty textual representation")
	}
}

func TestCompositeIndexesEveryTextRepresentation(t *testing.T) {
	cfg := DefaultConfig()
	paths, _ := json.Marshal([]string{`C:\contracts\invoice.pdf`})
	snapshot, err := buildCompositeSnapshot(cfg,
		&ClipboardSnapshot{Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: `C:\contracts\invoice.pdf`}, nil,
		map[string][]byte{compositeText: []byte("short note"), "HTML Format": []byte("<p>rich-only detail</p>"), "Rich Text Format": []byte(`{\rtf1 rtf-only detail}`)}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(snapshot.Content.Kinds, "text") || !slices.Contains(snapshot.Content.Kinds, "office") {
		t.Fatalf("missing kinds: %v", snapshot.Content.Kinds)
	}
	for _, rule := range []string{"^rich-only detail$", "^rtf-only detail$", `^C:\\contracts\\invoice\.pdf$`} {
		filter, err := NewFilterEngine(SensitiveConfig{CustomPatterns: []string{rule}})
		if err != nil {
			t.Fatal(err)
		}
		if !filter.CheckComposite(snapshot.Content).Sensitive {
			t.Fatalf("anchored privacy rule failed on alternate representation: %s", rule)
		}
	}
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"short", "rich-only", "rtf-only", "invoice.pdf"} {
		_, total, err := store.Search(context.Background(), SearchOptions{Query: query, Limit: 10})
		if err != nil || total != 1 {
			t.Fatalf("search %s: %d %v", query, total, err)
		}
	}
	// Empty or whitespace-only text must not erase another representation's kinds.
	c := &domain.CompositeContent{Representations: []domain.ContentRepresentation{{Format: compositeFiles, Data: paths}, {Format: compositeHTML, Data: []byte("<p>visible</p>")}, {Format: compositeText, Data: []byte(" ")}}}
	text, kinds, err := compositeTextAndKinds(c)
	if err != nil || !strings.Contains(text, "visible") || !slices.Contains(kinds, "text") {
		t.Fatalf("projection lost content: %q %v %v", text, kinds, err)
	}
	withoutPlain := &domain.CompositeContent{Representations: []domain.ContentRepresentation{{Format: compositeHTML, Data: []byte("<p>paste once</p>")}, {Format: compositeRTF, Data: []byte(`{\rtf1 alternate text}`)}}}
	if got := compositePasteText(withoutPlain); got != "paste once" {
		t.Fatalf("paste fallback merged alternatives: %q", got)
	}
}

func TestCompositeDoesNotReadResourcesInDiscardedMarkup(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	reads := 0
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"HTML Format": []byte(`<p>kept</p><template><img src="C:\private.png"></template><object><img src="C:\other.png"></object>`)}, nil, func(string, int) ([]byte, error) { reads++; return managedImageTestPNG(t), nil })
	if err != nil {
		t.Fatal(err)
	}
	if reads != 0 || len(snapshot.Content.Assets) != 0 || snapshot.Text != "kept" {
		t.Fatalf("discarded subtree captured: reads=%d", reads)
	}
}

func TestCompositeImageSettingDoesNotRewriteRTF(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image.Enabled = false
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"Rich Text Format": []byte(`{\rtf1 kept{\pict\pngblip 89504e47}}`)}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Text != "kept" || !slices.Contains(snapshot.Content.Kinds, "image") {
		t.Fatalf("disabled image retained: %+v", snapshot)
	}
	if len(snapshot.Content.Issues) != 0 {
		t.Fatal("original RTF reported content loss")
	}
}

func TestCompositePreviewExcludesSourceStyles(t *testing.T) {
	for _, style := range []string{`color:red`, `background: image-set("https://example.com/a.png" 1x)`, `content: image-set("https://example.com/a.png" 1x)`} {
		markup := []byte(`<p style='` + style + `'>text</p>`)
		c := &domain.CompositeContent{Representations: []domain.ContentRepresentation{{Format: compositeHTML, Data: markup}}, Kinds: []string{"text"}}
		if _, _, err := validateComposite(c); err != nil {
			t.Fatal(err)
		}
		preview := compositePreviewHTML(c, func(string) string { return "" })
		if strings.Contains(preview, "style") || strings.Contains(preview, "image-set") {
			t.Fatalf("unsafe preview: %s", preview)
		}
	}
}
