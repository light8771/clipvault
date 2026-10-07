package clipvault

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"slices"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
	"golang.org/x/net/html"
)

// An adapter converts registered application formats to portable HTML; their
// session markers and opaque OLE handles are deliberately not replayed.
func richEditHTML(raw []byte) ([]byte, error) {
	var doc struct {
		XMLName  xml.Name
		Elements []struct {
			Type string `xml:"type,attr"`
			Path string `xml:"filepath,attr"`
			Text string `xml:",chardata"`
		} `xml:"EditElement"`
	}
	if err := xml.Unmarshal(bytes.TrimRight(raw, "\x00"), &doc); err != nil {
		return nil, err
	}
	if doc.XMLName.Local != "WeChatRichEditFormat" && doc.XMLName.Local != "QQRichEditFormat" {
		return nil, errors.New("unsupported rich edit document")
	}
	var out strings.Builder
	for _, e := range doc.Elements {
		switch e.Type {
		case "0":
			out.WriteString(html.EscapeString(e.Text))
		case "1":
			out.WriteString(`<img src="` + html.EscapeString(e.Path) + `">`)
		default:
			return nil, errors.New("unsupported rich edit element")
		}
	}
	return []byte("<div>" + strings.ReplaceAll(out.String(), "\n", "<br>") + "</div>"), nil
}

func buildCompositeSnapshot(cfg Config, primary, imageSnapshot *ClipboardSnapshot, raw map[string][]byte, issues []domain.ContentIssue, loadImage func(string, int) ([]byte, error)) (*ClipboardSnapshot, error) {
	mixedFiles := false
	if primary != nil && primary.Format == domain.FormatFileDrop {
		kinds := map[string]bool{}
		for _, path := range splitNonEmptyLines(primary.Text) {
			kinds[fileExtKind(path)] = true
		}
		mixedFiles = len(kinds) > 1
	}
	if !mixedFiles && len(raw) == 0 && len(issues) == 0 && (primary == nil || imageSnapshot == nil || primary.Format == domain.FormatImageFile) {
		return primary, nil
	}
	c := &domain.CompositeContent{Representations: []domain.ContentRepresentation{}, Assets: []domain.ContentAsset{}, Issues: append([]domain.ContentIssue{}, issues...)}
	issue := func(format, reason string) {
		if len(c.Issues) < 128 {
			c.Issues = append(c.Issues, domain.ContentIssue{Format: format, Reason: reason})
		}
	}
	add := func(format string, data []byte) bool {
		if len(data) == 0 {
			return false
		}
		size := len(data)
		for _, r := range c.Representations {
			size += len(r.Data)
		}
		for _, a := range c.Assets {
			size += len(a.Data)
		}
		if size > maxCompositeBytes {
			issue(format, "resource_limit")
			return false
		}
		c.Representations = append(c.Representations, domain.ContentRepresentation{Format: format, Data: data})
		return true
	}
	if primary != nil {
		switch primary.Format {
		case domain.FormatUnicodeText:
			add(compositeText, []byte(primary.Text))
		case domain.FormatFileDrop:
			data, _ := json.Marshal(splitNonEmptyLines(primary.Text))
			add(compositeFiles, data)
		}
	}
	if text := raw[compositeText]; len(text) > 0 && (primary == nil || primary.Format != domain.FormatUnicodeText) {
		add(compositeText, text)
	}
	markup := raw["HTML Format"]
	if len(markup) > 0 {
		var err error
		markup, err = extractCFHTMLFragment(markup)
		if err != nil {
			issue("HTML Format", "decode_failed")
			markup = nil
		}
	}
	if len(markup) == 0 {
		for _, name := range []string{"WeChat_RichEdit_Format", "QQ_Unicode_RichEdit_Format"} {
			if data := raw[name]; len(data) > 0 {
				converted, err := richEditHTML(data)
				if err != nil {
					issue(name, "unsupported_structure")
					continue
				}
				markup = converted
				break
			}
		}
	}
	if len(markup) > 0 {
		doc, err := parseCompositeHTMLFragment(markup)
		if err != nil {
			return nil, err
		}
		// Remove discarded subtrees before resolving resources: a hidden image
		// in a template/object must not cause an unrelated file to be captured.
		if sanitizeCompositeHTMLWithSourceImages(doc, true) {
			issue("HTML Format", "unsafe_markup_removed")
		}
		assets := map[string]bool{}
		loaded := map[string]string{}
		used := 0
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "img" {
				for i, a := range n.Attr {
					if a.Key != "src" {
						continue
					}
					if ref, ok := loaded[a.Val]; ok {
						n.Attr[i].Val = ref
						continue
					}
					if !cfg.Image.Enabled {
						issue("HTML Format", "images_disabled")
						n.Attr[i].Val = ""
						continue
					}
					if len(c.Assets) >= maxCompositeAssets || used >= maxCompositeBytes/2 {
						issue("HTML Format", "resource_limit")
						n.Attr[i].Val = ""
						continue
					}
					data, err := loadImage(a.Val, min(configuredCompositeImageLimit(cfg), maxCompositeBytes/2-used))
					if err != nil {
						issue("HTML Format", "resource_unavailable")
						n.Attr[i].Val = ""
						continue
					}
					id := hashBytes(data)
					ref := assetReferencePrefix + id
					loaded[a.Val] = ref
					n.Attr[i].Val = ref
					if !assets[id] {
						assets[id] = true
						used += len(data)
						c.Assets = append(c.Assets, domain.ContentAsset{ID: id, MediaType: "image/png", Data: data})
					}
				}
			}
			if n.Type == html.ElementNode && (n.Data == "object" || n.Data == "embed" || n.Data == "iframe") {
				issue("HTML Format", "embedded_object_unsupported")
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(doc)
		var serialized bytes.Buffer
		if err := html.Render(&serialized, doc); err != nil {
			return nil, err
		}
		if !add(compositeHTML, serialized.Bytes()) {
			c.Assets = nil
		}
	}
	if rtf := bytes.TrimRight(raw["Rich Text Format"], "\x00"); len(rtf) > 0 {
		text, features, err := inspectCompositeRTF(rtf)
		if features.object {
			issue("Rich Text Format", "embedded_object_unsupported")
			if err == nil && !slices.ContainsFunc(c.Representations, func(r domain.ContentRepresentation) bool { return r.Format == compositeText }) {
				add(compositeText, []byte(text))
			}
		} else if err != nil {
			issue("Rich Text Format", "decode_failed")
		} else if !cfg.Image.Enabled && features.image {
			issue("Rich Text Format", "images_disabled")
			if !slices.ContainsFunc(c.Representations, func(r domain.ContentRepresentation) bool { return r.Format == compositeText }) {
				add(compositeText, []byte(text))
			}
		} else {
			add(compositeRTF, rtf)
		}
	}
	// A bitmap accompanying HTML is an alternative representation of this same
	// record, not a separate history item or an extra ordered document part.
	if imageSnapshot != nil {
		add(compositePNG, imageSnapshot.ImageBytes)
	}
	if len(c.Representations) == 0 {
		return primary, nil
	}
	slices.SortFunc(c.Representations, func(a, b domain.ContentRepresentation) int { return strings.Compare(a.Format, b.Format) })
	slices.SortFunc(c.Assets, func(a, b domain.ContentAsset) int { return strings.Compare(a.ID, b.ID) })
	text, kinds, err := compositeTextAndKinds(c)
	if err != nil {
		return nil, err
	}
	c.Kinds = kinds
	if cfg.MaxTextChars > 0 && textRuneLen(text) > cfg.MaxTextChars {
		return nil, &clipboardTextLimitError{CharLen: textRuneLen(text), Limit: cfg.MaxTextChars}
	}
	_, size, err := validateComposite(c)
	if err != nil {
		return nil, err
	}
	return &ClipboardSnapshot{Kind: domain.KindComposite, Format: domain.FormatComposite, Text: text, ByteLen: size, CharLen: textRuneLen(text), SHA256: compositeFingerprint(c), Content: c}, nil
}

func configuredCompositeImageLimit(cfg Config) int {
	if cfg.Image.MaxBytes > 0 {
		return cfg.Image.MaxBytes
	}
	return defaultImageMaxBytes
}
