package clipvault

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeDiagnosticMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, markup string
		warn         bool
	}{
		{"document metadata", `<meta name="generator" content="editor"><meta http-equiv="Content-Type" content="text/html; charset=utf-8"><p>kept</p>`, false},
		{"inactive code", `<script>run()</script><template><img src="C:\hidden.png"></template><p>kept</p>`, false},
		{"unused base and empty style", `<base href="https://example.com"><style> </style><p>kept</p>`, false},
		{"link metadata", `<link rel="icon" href="https://example.com/icon"><a target="_blank" rel="noopener" ping="https://example.com/ping" href="https://example.com">kept</a>`, false},
		{"browser styles", `<span style="color: red; user-select: text; cursor: auto; -webkit-text-stroke-width: 0px; orphans: 2; widows: 2">kept</span>`, false},
		{"empty image", `<p>kept<img src=" " loading="lazy" decoding="async"></p>`, false},
		{"image variants", `<p>kept<img src="C:\image.png" srcset="C:\large.png 2x" sizes="100vw" fetchpriority="high"></p>`, false},
		{"image variants before source", `<p>kept<img srcset="C:\large.png 2x" loading="lazy" src="C:\image.png"></p>`, false},
		{"source-less image variants", `<p>kept<img srcset="C:\large.png 2x"></p>`, false},
		{"stylesheet", `<link rel="stylesheet" href="https://example.com/theme.css"><p>kept</p>`, false},
		{"visual CSS loss", `<p style="color: red; background: url(https://example.com/image.png)">kept</p>`, false},
		{"embedded content", `<p>kept<iframe src="https://example.com"></iframe></p>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Image.Enabled = true
			reads := 0
			snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"HTML Format": []byte(tc.markup)}, nil, func(path string, _ int) ([]byte, error) {
				reads++
				if path != `C:\image.png` {
					t.Fatalf("unexpected resource read: %q", path)
				}
				return managedImageTestPNG(t), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if snapshot == nil || snapshot.Content == nil || snapshot.Text != "kept" || (len(snapshot.Content.Issues) > 0) != tc.warn {
				t.Fatalf("unexpected result: %+v", snapshot)
			}
			if strings.Contains(tc.markup, `src="C:\image.png"`) && reads != 1 {
				t.Fatal("image fallback was not captured")
			}
			for _, rep := range snapshot.Content.Representations {
				if rep.Format == compositeHTML && strings.Contains(tc.markup, "color: red") && !strings.Contains(string(rep.Data), "color: red") {
					t.Fatal("safe styling was discarded with unrelated CSS")
				}
			}
			if _, _, err := validateComposite(snapshot.Content); err != nil {
				t.Fatalf("captured markup is not valid for import: %v", err)
			}
		})
	}
}

func TestCompositeFallbackDiagnostics(t *testing.T) {
	for _, htmlAvailable := range []bool{false, true} {
		raw := map[string][]byte{}
		if htmlAvailable {
			raw["HTML Format"] = []byte("<p>kept</p>")
		}
		issues := []domain.ContentIssue{{Format: "WeChat_RichEdit_Format", Reason: "data_unavailable"}}
		snapshot, err := buildCompositeSnapshot(DefaultConfig(), &ClipboardSnapshot{Format: domain.FormatUnicodeText, Text: "kept"}, nil, raw, issues, readCompositeImage)
		if err != nil || snapshot == nil || snapshot.Content == nil {
			t.Fatalf("capture failed: %v", err)
		}
		if len(snapshot.Content.Issues) != 1 || len(issues) != 1 {
			t.Fatalf("fallback warning mismatch: %v", snapshot.Content.Issues)
		}
	}
	raw := map[string][]byte{
		"WeChat_RichEdit_Format":     []byte("invalid"),
		"QQ_Unicode_RichEdit_Format": []byte(`<QQRichEditFormat><EditElement type="0">kept</EditElement></QQRichEditFormat>`),
	}
	snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, raw, nil, readCompositeImage)
	if err != nil || snapshot == nil || len(snapshot.Content.Issues) != 0 || snapshot.Text != "kept" {
		t.Fatalf("successful adapter fallback reported loss: %+v %v", snapshot, err)
	}
}

func TestCompositeMissingResourcesRetainDiagnostic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"HTML Format": []byte(`<p>kept<img src="C:\missing.png"><img src="C:\missing.png"></p>`)}, nil, func(string, int) ([]byte, error) { return nil, errors.New("missing") })
	if err != nil || snapshot == nil || len(snapshot.Content.Issues) != 1 || snapshot.Content.Issues[0].Reason != "resource_unavailable" {
		t.Fatalf("missing image diagnostic: %+v %v", snapshot, err)
	}
}

func TestCompositeResourceLimitStillReportsLoss(t *testing.T) {
	var markup strings.Builder
	markup.WriteString("<p>kept")
	for i := 0; i <= maxCompositeAssets; i++ {
		fmt.Fprintf(&markup, `<img src="C:\%d.png">`, i)
	}
	markup.WriteString("</p>")
	cfg := DefaultConfig()
	cfg.Image.Enabled = true
	reads := 0
	snapshot, err := buildCompositeSnapshot(cfg, nil, nil, map[string][]byte{"HTML Format": []byte(markup.String())}, nil, func(string, int) ([]byte, error) {
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.Set(0, 0, color.RGBA{R: uint8(reads), A: 255})
		reads++
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			return nil, err
		}
		return encoded.Bytes(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || reads != maxCompositeAssets || len(snapshot.Content.Assets) != maxCompositeAssets || len(snapshot.Content.Issues) != 1 || snapshot.Content.Issues[0].Reason != "resource_limit" {
		t.Fatalf("asset limit did not preserve content and report loss: reads=%d snapshot=%+v", reads, snapshot)
	}
}

func TestCompositeImportPreservesHTMLAndIsolatesPreview(t *testing.T) {
	for _, markup := range []string{
		`<p style="color: red; cursor: url(https://example.com/a)">kept</p>`,
		`<script>run()</script><p>kept</p>`,
		`<template><img src="C:\secret.png"></template><p>kept</p>`,
		`<link rel="icon" href="https://example.com/icon"><p>kept</p>`,
		`<p style="color: red; background: image-set('https://example.com/a' 1x)">kept</p>`,
	} {
		c := &domain.CompositeContent{Kinds: []string{"text"}, Representations: []domain.ContentRepresentation{{Format: compositeHTML, Data: []byte(markup)}}}
		if _, _, err := validateComposite(c); err != nil {
			t.Fatal(err)
		}
		preview := compositePreviewHTML(c, func(string) string { return "" })
		if strings.Contains(preview, "<script") || strings.Contains(preview, "style=") || strings.Contains(preview, "<template") || strings.Contains(preview, "<link") {
			t.Fatalf("unsafe preview: %s", preview)
		}
		if string(c.Representations[0].Data) != markup {
			t.Fatal("import modified source")
		}
	}
}
