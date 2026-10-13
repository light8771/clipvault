package clipvault

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestCompositeRTFControlWords(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      compositeRTFFeatures
	}{
		{"escaped examples", `{\rtf1 \\pict \\object \\objdata \\trowd HYPERLINK}`, compositeRTFFeatures{}},
		{"binary examples", fmt.Sprintf(`{\rtf1 kept\bin%d %s}`, len(`\pict\object`), `\pict\object`), compositeRTFFeatures{}},
		{"picture", `{\rtf1{\pict\pngblip 00}}`, compositeRTFFeatures{image: true}},
		{"table", `{\rtf1\trowd cell\cell\row}`, compositeRTFFeatures{table: true}},
		{"object", `{\rtf1{\object{\objdata 00}}}`, compositeRTFFeatures{object: true}},
		{"link", `{\rtf1{\field{\*\fldinst HYPERLINK "https://example.com"}{\fldrslt title}}}`, compositeRTFFeatures{link: true}},
		{"other field", `{\rtf1{\field{\*\fldinst HYPERLINKED "example"}{\fldrslt title}}}`, compositeRTFFeatures{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got, err := inspectCompositeRTF([]byte(tc.raw))
			if err != nil || got != tc.want {
				t.Fatalf("features=%+v want=%+v err=%v", got, tc.want, err)
			}
		})
	}
	cfg := DefaultConfig()
	cfg.Image.Enabled = false
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"Rich Text Format": []byte(`{\rtf1 \\pict \\object \\objdata \\trowd HYPERLINK}`)}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Content == nil || !slices.Equal(snapshot.Content.Kinds, []string{"text"}) || len(snapshot.Content.Issues) != 0 {
		t.Fatalf("literal RTF code was discarded or misclassified: %+v", snapshot)
	}
}

func TestCompositeHTMLTableSelection(t *testing.T) {
	for _, markup := range []string{`<tr><td>north</td><td>south</td></tr>`, `<td>north</td>`, `<!-- selection --> <tbody><tr><th>north</th></tr></tbody>`} {
		snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"HTML Format": []byte(markup)}, nil, readCompositeImage)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot == nil || snapshot.Content == nil || !slices.Contains(snapshot.Content.Kinds, "table") || !strings.Contains(snapshot.Text, "north") {
			t.Fatalf("table selection lost: %s => %+v", markup, snapshot)
		}
		var stored string
		for _, rep := range snapshot.Content.Representations {
			if rep.Format == compositeHTML {
				stored = string(rep.Data)
			}
		}
		if stored != markup {
			t.Fatalf("table structure lost: %s", stored)
		}
	}
}

func TestCompositeLiteralAssetReferenceIsNotDependency(t *testing.T) {
	snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"HTML Format": []byte(`<p title="clipvault-asset:example">documentation</p>`)}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Text != "documentation" {
		t.Fatalf("literal attribute prevented capture: %+v", snapshot)
	}
}

func TestCompositeHTMLTableTextSeparatesCells(t *testing.T) {
	markup := []byte(`<tr><th>name</th><th>count</th></tr><tr><td>apple</td><td>2</td></tr>`)
	want := "name\tcount\napple\t2"
	text, err := extractCFHTMLText(markup)
	if err != nil || text != want {
		t.Fatalf("text fallback=%q err=%v", text, err)
	}
	snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"HTML Format": markup}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Text != want || compositePasteText(snapshot.Content) != want {
		t.Fatalf("table cells merged: %+v", snapshot)
	}
}

func TestCompositeHTMLTextExcludesDocumentMetadata(t *testing.T) {
	snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{
		"HTML Format": []byte(`<html><head><title>document metadata</title></head><body><p>visible body</p></body></html>`),
	}, nil, readCompositeImage)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Text != "visible body" || compositePasteText(snapshot.Content) != "visible body" {
		t.Fatalf("document metadata leaked into search or plain-text paste: %+v", snapshot)
	}
}
