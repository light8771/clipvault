package clipvault

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
	"golang.org/x/net/html"
)

func TestCompositePreservesOriginalHTML(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`<div class="source" style="unknown-future-property:keep;--custom:value"><svg><path d="M0 0L16 16"/></svg><button onclick="run()">body</button><script>run()</script></div>`),
		[]byte(`<tr><td title=""quoted"label">table</td></tr>`),
		[]byte("Version:1.0\r\nStartHTML:broken\r\n"),
	} {
		snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"HTML Format": raw}, nil, readCompositeImage)
		if err != nil || snapshot == nil {
			t.Fatalf("capture: %v", err)
		}
		if len(snapshot.Content.Issues) != 0 {
			t.Fatalf("preview limitations became losses: %+v", snapshot.Content.Issues)
		}
		if !bytes.Equal(snapshot.Content.Representations[0].Data, raw) {
			t.Fatal("original HTML altered")
		}
		if _, _, err := validateComposite(snapshot.Content); err != nil {
			t.Fatal(err)
		}
		before := compositeJSONString(snapshot.Content)
		preview := compositePreviewHTML(snapshot.Content, func(string) string { return "" })
		for _, unsafe := range []string{"onclick", "<script", "<svg", "style=", "unknown-future-property"} {
			if strings.Contains(preview, unsafe) {
				t.Fatalf("unsafe preview: %s", preview)
			}
		}
		if before != compositeJSONString(snapshot.Content) {
			t.Fatal("preview mutated original")
		}
	}
}

func TestCompositeImageResourceBytesPreserved(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var animated, photo bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 20}}); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&photo, frame, nil); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{animated.Bytes(), photo.Bytes()} {
		snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"HTML Format": []byte(`<img src="C:\original.image">`)}, nil, func(string, int) ([]byte, error) { return data, nil })
		if err != nil || snapshot == nil || len(snapshot.Content.Assets) != 1 {
			t.Fatalf("capture: %v", err)
		}
		if !bytes.Equal(snapshot.Content.Assets[0].Data, data) {
			t.Fatal("original image was transcoded")
		}
		if _, _, err := validateComposite(snapshot.Content); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompositeInlineImageDoesNotDependOnBackup(t *testing.T) {
	data := managedImageTestPNG(t)
	raw := []byte(`<img src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(data) + `">`)
	cfg := DefaultConfig()
	cfg.Image.Enabled = false
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"HTML Format": raw}, nil, func(string, int) ([]byte, error) { t.Fatal("inline bytes do not need fetching"); return nil, nil })
	if err != nil || snapshot == nil {
		t.Fatalf("capture: %v", err)
	}
	if len(snapshot.Content.Issues) != 0 || len(snapshot.Content.Assets) != 0 || !bytes.Equal(snapshot.Content.Representations[0].Data, raw) {
		t.Fatal("self-contained source marked incomplete")
	}
	preview := compositePreviewHTML(snapshot.Content, func(string) string { return "" })
	if !strings.Contains(preview, "data:image/png;base64,") {
		t.Fatal("safe inline image lost from preview")
	}
}

func TestCompositePreservesOriginalRTF(t *testing.T) {
	for _, raw := range [][]byte{[]byte("{\\rtf1 body{\\object{\\objdata 00}}}\x00"), []byte("opaque malformed rtf\x00")} {
		snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"Rich Text Format": raw}, nil, readCompositeImage)
		if err != nil || snapshot == nil {
			t.Fatalf("capture: %v", err)
		}
		if !bytes.Equal(snapshot.Content.Representations[0].Data, raw) || len(snapshot.Content.Issues) != 0 {
			t.Fatal("raw RTF lost")
		}
	}
}

func FuzzCompositePreviewDoesNotMutateOriginal(f *testing.F) {
	for _, seed := range []string{`<svg onload="run()"><foreignObject><img src="https://example.com/"></foreignObject></svg>`, `<button onclick="run()" style="background:url(https://example.com)">text</button>`, `<td>cell</td>`, `<a href="javascript:run()">link</a>`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, markup string) {
		if len(markup) > 256<<10 {
			t.Skip()
		}
		c := &domain.CompositeContent{Representations: []domain.ContentRepresentation{{Format: compositeHTML, Data: []byte(markup)}}}
		before := compositeJSONString(c)
		preview := compositePreviewHTML(c, func(string) string { return "" })
		doc, err := html.Parse(strings.NewReader(preview))
		if err != nil {
			t.Fatal(err)
		}
		var check func(*html.Node)
		check = func(n *html.Node) {
			for _, a := range n.Attr {
				if strings.HasPrefix(a.Key, "on") || a.Key == "style" || a.Key == "srcdoc" || a.Key == "src" && !strings.HasPrefix(a.Val, "data:image/") {
					t.Fatalf("active preview attribute: %+v", a)
				}
			}
			if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "iframe" || n.Data == "object" || n.Data == "svg" || n.Data == "link" || n.Data == "meta") {
				t.Fatalf("active preview element: %s", n.Data)
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				check(child)
			}
		}
		check(doc)
		if compositeJSONString(c) != before {
			t.Fatal("preview mutated source")
		}
	})
}
