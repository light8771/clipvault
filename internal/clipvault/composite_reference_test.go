package clipvault

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompositeInlineReferenceSyntaxDoesNotReportLoss(t *testing.T) {
	data := managedImageTestPNG(t)
	for _, prefix := range []string{"DATA:image/png;base64,", " \tdata:IMAGE/PNG;BASE64,"} {
		ref := prefix + base64.StdEncoding.EncodeToString(data) + "\r\n "
		raw := []byte(`<img src="` + ref + `">`)
		cfg := DefaultConfig()
		cfg.Image.Enabled = false
		snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"HTML Format": raw}, nil, func(string, int) ([]byte, error) {
			t.Fatal("self-contained data triggered an external load")
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Content.Issues) != 0 {
			t.Fatalf("false loss: %+v", snapshot.Content.Issues)
		}
		if !bytes.Equal(snapshot.Content.Representations[0].Data, raw) {
			t.Fatal("raw bytes changed")
		}
		if preview := compositePreviewHTML(snapshot.Content, func(string) string { return "" }); !strings.Contains(preview, `src="data:image/png;base64,`) {
			t.Fatalf("missing inline preview: %s", preview)
		}
	}
}

func TestCompositeSourceURLDoesNotReadDocumentBody(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write(managedImageTestPNG(t)) }))
	defer server.Close()
	raw := []byte("<pre>copied text\nSourceURL:" + server.URL + "/page\n</pre>")
	if source := compositeSourceURL(raw); source != "" {
		t.Fatalf("body became source metadata: %q", source)
	}
	load, release := newCompositeImageLoader(context.Background(), raw)
	defer release()
	if _, err := load(server.URL+"/private", 1<<20); err == nil {
		t.Fatal("body granted private-origin access")
	}
	if hits.Load() != 0 {
		t.Fatal("request reached private origin")
	}
	valid := []byte("Version:1.0\r\nSourceURL:https://example.test/page\r\n<html><pre>\nSourceURL:" + server.URL + "\n</pre></html>")
	if source := compositeSourceURL(valid); source != "https://example.test/page" {
		t.Fatalf("valid header lost: %q", source)
	}
}

func TestCompositeProtocolWordsRemainDocumentText(t *testing.T) {
	for _, raw := range []string{
		"<p>Version:1.0</p><pre>\nStartFragment:0\nEndFragment:1\nSourceURL:http://localhost/\n</pre>",
		"copied text\nSourceURL:http://localhost/\n<p>body</p>",
	} {
		if hasCFHTMLHeader([]byte(raw)) {
			t.Fatal("document mistaken for header")
		}
		if compositeSourceURL([]byte(raw)) != "" {
			t.Fatal("document supplied source metadata")
		}
		got, err := extractCFHTMLFragment([]byte(raw))
		if err != nil || string(got) != raw {
			t.Fatalf("document truncated: %q, %v", got, err)
		}
		if _, ok := cfHTMLOffset([]byte(raw), "StartFragment"); ok {
			t.Fatal("document supplied offsets")
		}
	}
}
