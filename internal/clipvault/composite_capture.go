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

// Derive HTML for search, preview and resource discovery. The original
// application payload is retained independently for restoration.
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
		if truncated, _ := primary.Meta["truncated"].(bool); truncated {
			issues = append(append([]domain.ContentIssue(nil), issues...), domain.ContentIssue{Format: "CF_HDROP", Reason: "resource_limit"})
		}
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
		for _, existing := range c.Issues {
			if existing.Format == format && existing.Reason == reason {
				return
			}
		}
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
			for _, resource := range r.Resources {
				size += len(resource.Source) + len(resource.AssetID)
			}
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
	for _, name := range []string{"HTML Format", compositeWeChat, compositeQQ} {
		format := name
		if name == "HTML Format" {
			format = compositeHTML
		}
		if data := raw[name]; len(data) > 0 {
			add(format, append([]byte(nil), data...))
		}
	}

	// Rich formats are opaque recovery payloads. Parsing for search/preview
	// never determines whether their original bytes are retained.
	if rtf := raw["Rich Text Format"]; len(rtf) > 0 {
		add(compositeRTF, append([]byte(nil), rtf...))
	}

	// A bitmap accompanying HTML is an alternative representation of this same
	// record, not a separate history item or an extra ordered document part.
	if imageSnapshot != nil {
		add(compositePNG, imageSnapshot.ImageBytes)
	}
	// Original representations take priority over optional resource backups.
	budget := maxCompositeBytes
	for _, rep := range c.Representations {
		budget -= len(rep.Data)
	}
	cachedLoad := cachedCompositeImageLoader(loadImage)
	for index, rep := range c.Representations {
		if rep.Format != compositeHTML && rep.Format != compositeWeChat && rep.Format != compositeQQ {
			continue
		}
		markup, err := compositeMarkup(rep)
		if err != nil {
			continue
		}
		source := rep.Format
		if source == compositeHTML {
			source = "HTML Format"
		}
		resources, assets := captureCompositeHTMLResources(cfg, markup, cachedLoad, budget, c.Assets, func(_ string, reason string) { issue(source, reason) })
		c.Representations[index].Resources = resources
		for _, resource := range resources {
			budget -= len(resource.Source) + len(resource.AssetID)
		}
		for _, asset := range assets {
			budget -= len(asset.Data)
		}
		c.Assets = append(c.Assets, assets...)
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
