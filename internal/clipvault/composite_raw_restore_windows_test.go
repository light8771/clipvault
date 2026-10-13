//go:build windows

package clipvault

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCompositeRawCFHTMLStoreRestore(t *testing.T) {
	fragment := `<div data-origin="keep" style="future-style: x; background: conic-gradient(red,blue)"><button onclick="run()">按钮文字</button><svg><path d="M0 0L16 16"/></svg><script>run()</script></div>`
	raw := encodeCFHTML(fragment)
	snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"HTML Format": raw}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Save(context.Background(), ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, ContentSHA256: snapshot.SHA256}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(context.Background(), store.db)
	if err != nil || len(items) != 1 {
		t.Fatalf("load: %v", err)
	}
	before := compositeJSONString(items[0].Content)
	preview := compositePreviewHTML(items[0].Content, func(string) string { return "" })
	if !strings.Contains(preview, "按钮文字") || strings.Contains(preview, "onclick") || strings.Contains(preview, "<script") {
		t.Fatalf("preview: %s", preview)
	}
	restored, err := prepareClipboardRestoreRepresentations(items[0], store.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range restored {
		if r.format == registerClipboardFormat("HTML Format") {
			found = true
			if !bytes.Equal(r.data, raw) {
				t.Fatal("CF_HTML bytes changed")
			}
		}
	}
	if !found || compositeJSONString(items[0].Content) != before {
		t.Fatal("source mutated or lost")
	}
}

func TestCompositeResourceRewritePreservesContextAndOffsets(t *testing.T) {
	fragment := `<button data-example=" src='do-not-touch'" onclick="keep()">text</button><IMG data-note=" src='wrong'" SRC='C:\original.png' style="future:keep"><img src="https://example.com/unsaved.png">`
	raw := encodeCFHTML(fragment)
	// Preserve the original header and document context, changing only the
	// resource value and the byte offsets affected by its changed length.
	result, err := restoreCompositeHTMLPayload(raw, map[string]string{`C:\original.png`: `C:\restore\a&b.png`})
	if err != nil {
		t.Fatal(err)
	}
	got, err := extractCFHTMLFragment(result)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(fragment, `SRC='C:\original.png'`, `SRC="file:///C:/restore/a&amp;b.png"`, 1)
	if string(got) != want {
		t.Fatalf("unrelated markup changed: %s", got)
	}
	for _, key := range []string{"StartHTML", "EndHTML", "StartFragment", "EndFragment"} {
		offset, ok := cfHTMLOffset(result, key)
		if !ok || offset < 0 || offset > len(result) {
			t.Fatalf("bad %s", key)
		}
	}
	start, _ := cfHTMLOffset(result, "StartHTML")
	end, _ := cfHTMLOffset(result, "EndHTML")
	if !bytes.HasPrefix(result[start:end], []byte("<html>")) || !bytes.HasSuffix(result[start:end], []byte("</html>")) {
		t.Fatal("document context lost")
	}
	if result[len(result)-1] != 0 {
		t.Fatal("terminator lost")
	}
}
