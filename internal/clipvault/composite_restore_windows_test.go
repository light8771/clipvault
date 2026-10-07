//go:build windows

package clipvault

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeRestoreOnlyRewritesImageReferences(t *testing.T) {
	id := strings.Repeat("a", 64)
	ref := assetReferencePrefix + id
	markup, err := compositeRestoreHTML([]byte(`<html><body><p>`+ref+`</p><img src="`+ref+`"></body></html>`), map[string]string{id: `C:\paste\a&b.png`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markup, "<p>"+ref+"</p>") || !strings.Contains(markup, `src="file:///C:/paste/a&amp;b.png"`) || strings.Contains(markup, "<html>") {
		t.Fatalf("incorrect HTML rewrite: %s", markup)
	}
}

func TestCompositeRichEditPreservesTableCellSeparators(t *testing.T) {
	for _, root := range []string{"WeChatRichEditFormat", "QQRichEditFormat"} {
		markup, err := richEditHTML(encodeRichEditHTML([]byte(`<table><tr><th>name</th><th>count</th></tr><tr><td>apple</td><td>2</td></tr></table>`), nil, root))
		if err != nil {
			t.Fatal(err)
		}
		text, err := extractCFHTMLText(markup)
		if err != nil || text != "name\tcount\napple\t2" {
			t.Fatalf("%s text=%q err=%v", root, text, err)
		}
	}
}

func TestCompositeRestoreRepresentationsAndCacheLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := compositeFixture(t, `C:\temporary\source.png`)
	if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	before := clipboardSequenceNumber()
	representations, err := prepareClipboardRestoreRepresentations(items[0], dir)
	if err != nil {
		t.Fatal(err)
	}
	if clipboardSequenceNumber() != before {
		t.Fatal("preparation modified the clipboard")
	}
	formats := map[uint32][]byte{}
	for _, r := range representations {
		formats[r.format] = r.data
	}
	if len(formats[cfUnicodeText]) == 0 {
		t.Fatal("plain text fallback absent")
	}
	fragment, err := extractCFHTMLFragment(formats[registerClipboardFormat("HTML Format")])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fragment, []byte("file:///")) || bytes.Contains(fragment, []byte(assetReferencePrefix)) {
		t.Fatalf("unresolved recovery HTML: %s", fragment)
	}
	for _, name := range []string{"WeChat_RichEdit_Format", "QQ_Unicode_RichEdit_Format"} {
		converted, err := richEditHTML(formats[registerClipboardFormat(name)])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(converted, []byte("restore-assets")) {
			t.Fatal("rich-edit adapter omitted images")
		}
	}
	assetPath := filepath.Join(dir, "restore-assets", snapshot.Content.Assets[0].ID+".png")
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(assetPath); !os.IsNotExist(err) {
		t.Fatalf("deleted resource remains: %v", err)
	}
	items[0].Content.Representations[0].Data = []byte("tampered")
	if _, err := prepareClipboardRestoreRepresentations(items[0], dir); err == nil {
		t.Fatal("tampered package accepted")
	}
}

func TestCompositeMixedFilesAndRichEdit(t *testing.T) {
	cfg := DefaultConfig()
	files := &ClipboardSnapshot{Kind: domain.KindFiles, Format: domain.FormatFileDrop, Text: "C:\\first.png\nC:\\second.pdf"}
	snapshot, err := buildCompositeSnapshot(cfg, files, nil, nil, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Kind != domain.KindComposite || strings.Join(snapshot.Content.Kinds, ",") != "files,image,office" {
		t.Fatalf("mixed file kinds: %+v", snapshot)
	}
	raw := []byte(`<WeChatRichEditFormat><EditElement type="0"><![CDATA[before]]></EditElement><EditElement type="1" filepath="C:\temp\image.png"/><EditElement type="0"><![CDATA[after]]></EditElement></WeChatRichEditFormat>`)
	markup, err := richEditHTML(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markup), `before<img src="C:\temp\image.png">after`) {
		t.Fatalf("order lost: %s", markup)
	}
}
