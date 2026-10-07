package clipvault

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"clipboard_ext/internal/clipvault/domain"
	"golang.org/x/net/html"
)

const maxCompositeBytes = 64 << 20
const maxCompositeAssets = 128
const assetReferencePrefix = "clipvault-asset:"
const compositeHTML = "text/html"
const compositeText = "text/plain"
const compositeRTF = "text/rtf"
const compositePNG = "image/png"
const compositeFiles = "application/x-clipvault-files"

// Keep passive rich formatting, while removing executable markup and external
// subresources. This also runs on imported packages before they can be stored.
func sanitizeCompositeHTML(doc *html.Node) bool {
	return sanitizeCompositeHTMLWithSourceImages(doc, false)
}

func sanitizeCompositeHTMLWithSourceImages(doc *html.Node, sourceImages bool) bool {
	changed := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == html.ElementNode && slices.Contains([]string{"script", "style", "iframe", "object", "embed", "svg", "math", "template", "link", "meta", "base", "form", "input", "button", "video", "audio", "source"}, child.Data) {
				n.RemoveChild(child)
				// CF_HTML commonly carries a charset declaration. Dropping that
				// metadata from a UTF-8 package does not lose user content.
				charsetOnly := child.Data == "meta" && len(child.Attr) == 1 && child.Attr[0].Key == "charset"
				changed = changed || !charsetOnly
			} else {
				walk(child)
			}
			child = next
		}
		attrs := n.Attr[:0]
		for _, a := range n.Attr {
			keep := a.Namespace == ""
			switch a.Key {
			case "src":
				keep = keep && n.Data == "img" && (sourceImages || a.Val == "" || strings.HasPrefix(a.Val, assetReferencePrefix))
			case "href":
				u, err := url.Parse(strings.TrimSpace(a.Val))
				keep = keep && err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "mailto" || strings.HasPrefix(a.Val, "#"))
			case "style":
				keep = keep && safeCompositeStyle(a.Val)
			case "alt", "title", "colspan", "rowspan", "width", "height", "align", "valign", "color", "face", "size", "dir", "lang", "start", "type":
			default:
				keep = false
			}
			if keep {
				attrs = append(attrs, a)
			} else {
				changed = true
			}
		}
		n.Attr = attrs
	}
	walk(doc)
	return changed
}

// A property allowlist rules out network-bearing CSS such as image-set(),
// which cannot be secured by searching only for the spelling "url".
func safeCompositeStyle(style string) bool {
	value := strings.ToLower(style)
	if strings.ContainsAny(value, "\\@") || strings.Contains(value, "url") || strings.Contains(value, "expression") || strings.Contains(value, "behavior") {
		return false
	}
	for _, declaration := range strings.Split(value, ";") {
		if strings.TrimSpace(declaration) == "" {
			continue
		}
		property, _, ok := strings.Cut(declaration, ":")
		if !ok {
			return false
		}
		switch strings.TrimSpace(property) {
		case "color", "background-color", "font", "font-family", "font-size", "font-weight", "font-style", "font-variant", "line-height", "letter-spacing", "word-spacing",
			"text-align", "text-decoration", "text-indent", "text-transform", "vertical-align", "white-space", "word-break", "overflow-wrap",
			"border", "border-color", "border-style", "border-width", "border-collapse", "border-spacing", "border-top", "border-right", "border-bottom", "border-left",
			"padding", "padding-top", "padding-right", "padding-bottom", "padding-left", "margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
			"width", "height", "max-width", "max-height", "min-width", "min-height", "table-layout", "list-style-type":
		default:
			return false
		}
	}
	return true
}

// Evaluate representations individually so anchored user rules still match.
// Concatenating formats would turn a matching "^secret$" into a non-match.
func (filter *FilterEngine) CheckComposite(c *domain.CompositeContent) FilterDecision {
	if c == nil {
		return FilterDecision{}
	}
	for _, r := range c.Representations {
		var text string
		switch r.Format {
		case compositeText, compositeHTML, compositeFiles:
			text = string(r.Data)
		case compositeRTF:
			text, _ = parseRTFText(r.Data)
		}
		if decision := filter.CheckText(text); text != "" && decision.Sensitive {
			return decision
		}
		if r.Format == compositeHTML {
			text, _ := extractCFHTMLText(r.Data)
			if decision := filter.CheckText(text); text != "" && decision.Sensitive {
				return decision
			}
		}
		if r.Format == compositeFiles {
			var paths []string
			if json.Unmarshal(r.Data, &paths) == nil {
				for _, path := range paths {
					if decision := filter.CheckText(path); decision.Sensitive {
						return decision
					}
				}
			}
		}
	}
	return FilterDecision{}
}

func encodeComposite(c *domain.CompositeContent) (any, error) {
	if c == nil {
		return nil, nil
	}
	data, err := json.Marshal(c)
	return string(data), err
}

func compositeJSONString(c *domain.CompositeContent) string {
	if c == nil {
		return ""
	}
	data, _ := json.Marshal(c)
	return string(data)
}
func compositeJSONValue(c *domain.CompositeContent) any {
	if c == nil {
		return nil
	}
	return compositeJSONString(c)
}

// The fingerprint includes all recoverable representations and resources, but
// excludes diagnostics. Canonical ordering makes format enumeration and local
// resource filenames irrelevant without collapsing formatting differences.
func compositeFingerprint(c *domain.CompositeContent) string {
	copy := *c
	copy.Issues = nil
	// An absent and an empty resource list represent the same recoverable
	// content. Normalize the wire shape before hashing, without mutating input.
	copy.Assets = append([]domain.ContentAsset{}, c.Assets...)
	data, _ := json.Marshal(copy)
	return hashBytes(data)
}

func validateComposite(c *domain.CompositeContent) (string, int, error) {
	if c == nil || len(c.Representations) == 0 || len(c.Representations) > 5 || len(c.Assets) > maxCompositeAssets || len(c.Issues) > 128 {
		return "", 0, errors.New("invalid composite package")
	}
	total := 0
	assets := map[string]bool{}
	formats := map[string]bool{}
	last := ""
	for _, a := range c.Assets {
		if a.MediaType != "image/png" || !validContentSHA256(a.ID) || a.ID <= last || hashBytes(a.Data) != a.ID {
			return "", 0, errors.New("invalid composite asset")
		}
		last = a.ID
		assets[a.ID] = true
		total += len(a.Data)
		if total > maxCompositeBytes {
			return "", 0, errors.New("composite package too large")
		}
		if err := validateManagedImagePNG(a.Data); err != nil {
			return "", 0, err
		}
	}
	last = ""
	for _, r := range c.Representations {
		if r.Format <= last || formats[r.Format] || len(r.Data) == 0 {
			return "", 0, errors.New("invalid composite representations")
		}
		last = r.Format
		formats[r.Format] = true
		total += len(r.Data)
		if total > maxCompositeBytes {
			return "", 0, errors.New("composite package too large")
		}
		if r.Format != compositePNG && r.Format != compositeRTF && (!utf8.Valid(r.Data) || bytes.IndexByte(r.Data, 0) >= 0) {
			return "", 0, errors.New("invalid composite text encoding")
		}
		if r.Format == compositePNG {
			if err := validateManagedImagePNG(r.Data); err != nil {
				return "", 0, err
			}
		}
		if r.Format == compositeRTF {
			_, features, err := inspectCompositeRTF(r.Data)
			if features.object {
				return "", 0, errors.New("unsupported RTF object")
			}
			if err != nil {
				return "", 0, err
			}
		}
		if r.Format == compositeHTML {
			doc, err := html.Parse(bytes.NewReader(r.Data))
			if err != nil {
				return "", 0, err
			}
			if sanitizeCompositeHTML(doc) {
				return "", 0, errors.New("unsafe composite HTML")
			}
			var check func(*html.Node) error
			check = func(n *html.Node) error {
				for _, a := range n.Attr {
					if n.Type == html.ElementNode && n.Data == "img" && a.Key == "src" && strings.HasPrefix(a.Val, assetReferencePrefix) && !assets[strings.TrimPrefix(a.Val, assetReferencePrefix)] {
						return errors.New("missing composite resource")
					}
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					if err := check(child); err != nil {
						return err
					}
				}
				return nil
			}
			if err := check(doc); err != nil {
				return "", 0, err
			}
		}
	}
	for _, issue := range c.Issues {
		if len(issue.Format) > 255 || len(issue.Reason) > 100 || issue.Reason == "" {
			return "", 0, errors.New("invalid composite issue")
		}
	}
	text, kinds, err := compositeTextAndKinds(c)
	if err != nil || len(kinds) == 0 || !slices.Equal(kinds, c.Kinds) {
		return "", 0, errors.New("composite type projection mismatch")
	}
	return text, total, nil
}

func readCompositeImage(ref string, limit int) ([]byte, error) {
	var raw []byte
	if strings.HasPrefix(ref, "data:") {
		header, data, ok := strings.Cut(ref, ",")
		if !ok || !strings.HasSuffix(header, ";base64") || !strings.HasPrefix(header, "data:image/") || len(data) > limit*2 {
			return nil, errors.New("unsupported inline image")
		}
		var err error
		raw, err = base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, err
		}
	} else {
		path := ref
		if strings.HasPrefix(strings.ToLower(path), "file:") {
			u, err := url.Parse(path)
			if err != nil || u.Host != "" && u.Host != "localhost" {
				return nil, errors.New("remote resource")
			}
			path = u.Path
			if len(path) > 2 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
		}
		if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
			return nil, errors.New("non-local resource")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > int64(limit) {
			return nil, errors.New("invalid resource file")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, int64(limit)+1))
		if err != nil {
			return nil, err
		}
	}
	if len(raw) > limit {
		return nil, ErrImageLimitExceeded
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if err = validateDecodedImageDimensions(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil {
		return nil, err
	}
	if out.Len() > limit {
		return nil, ErrImageLimitExceeded
	}
	return out.Bytes(), nil
}

// Preview is a fresh allowlisted rendering. Stored recovery markup is never
// inserted into the application DOM; only managed image URLs may load bytes.
func compositePreviewHTML(c *domain.CompositeContent, imageURL func(string) string) string {
	for _, r := range c.Representations {
		if r.Format == compositeHTML {
			doc, err := html.Parse(bytes.NewReader(r.Data))
			if err != nil {
				return ""
			}
			var out strings.Builder
			hasContent := false
			var render func(*html.Node)
			render = func(n *html.Node) {
				if n.Type == html.TextNode {
					hasContent = hasContent || strings.TrimSpace(n.Data) != ""
					out.WriteString(html.EscapeString(n.Data))
					return
				}
				if n.Type == html.ElementNode && slices.Contains([]string{"script", "style", "iframe", "object", "embed", "svg", "math", "template", "head"}, n.Data) {
					return
				}
				allowed := n.Type == html.ElementNode && slices.Contains([]string{"a", "p", "div", "span", "br", "b", "strong", "i", "em", "u", "s", "pre", "code", "blockquote", "ul", "ol", "li", "table", "thead", "tbody", "tfoot", "tr", "td", "th", "h1", "h2", "h3", "h4", "hr", "img"}, n.Data)
				if allowed {
					out.WriteString("<" + n.Data)
					for _, a := range n.Attr {
						if (n.Data == "td" || n.Data == "th") && (a.Key == "colspan" || a.Key == "rowspan") {
							if value, err := strconv.Atoi(a.Val); err == nil && value > 0 && value <= 1000 {
								out.WriteString(fmt.Sprintf(` %s="%d"`, a.Key, value))
							}
						}
						if n.Data == "a" && a.Key == "href" {
							if u, err := url.Parse(a.Val); err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "mailto") {
								out.WriteString(` href="` + html.EscapeString(a.Val) + `" target="_blank" rel="noopener noreferrer"`)
							}
						}
					}
					if n.Data == "img" {
						for _, a := range n.Attr {
							if a.Key == "src" && strings.HasPrefix(a.Val, assetReferencePrefix) {
								src := imageURL(strings.TrimPrefix(a.Val, assetReferencePrefix))
								hasContent = hasContent || src != ""
								out.WriteString(` src="` + html.EscapeString(src) + `"`)
							}
						}
						out.WriteString(` alt=""`)
					}
					out.WriteString(">")
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					render(child)
				}
				if allowed && n.Data != "img" && n.Data != "br" && n.Data != "hr" {
					out.WriteString("</" + n.Data + ">")
				}
			}
			render(doc)
			if !hasContent {
				return ""
			}
			return out.String()
		}
	}
	return ""
}
