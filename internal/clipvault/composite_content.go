package clipvault

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
const compositeHTML = "text/html"
const compositeText = "text/plain"
const compositeRTF = "text/rtf"
const compositePNG = "image/png"
const compositeWeChat = "WeChat_RichEdit_Format"
const compositeQQ = "QQ_Unicode_RichEdit_Format"

const compositeFiles = "application/x-clipvault-files"

// Evaluate representations individually so anchored user rules still match.
// Concatenating formats would turn a matching "^secret$" into a non-match.
func (filter *FilterEngine) CheckComposite(c *domain.CompositeContent) FilterDecision {
	if c == nil {
		return FilterDecision{}
	}
	for _, r := range c.Representations {
		var text string
		switch r.Format {
		case compositeText, compositeHTML, compositeWeChat, compositeQQ, compositeFiles:
			text = string(r.Data)
		case compositeRTF:
			text, _ = parseRTFText(r.Data)
		}
		if decision := filter.CheckText(text); text != "" && decision.Sensitive {
			return decision
		}
		if r.Format == compositeHTML || r.Format == compositeWeChat || r.Format == compositeQQ {
			markup, _ := compositeMarkup(r)
			text, _ := extractCFHTMLText(markup)
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
// excludes diagnostics. Original bytes (including source URLs and filenames)
// remain part of identity; different recovery payloads must not be collapsed.
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
	if c == nil || len(c.Representations) == 0 || len(c.Representations) > 7 || len(c.Assets) > maxCompositeAssets || len(c.Issues) > 128 {
		return "", 0, errors.New("invalid composite package")
	}
	total := 0
	assets := map[string]bool{}
	formats := map[string]bool{}
	last := ""
	for _, a := range c.Assets {
		if !validContentSHA256(a.ID) || a.ID <= last || hashBytes(a.Data) != a.ID {
			return "", 0, errors.New("invalid composite asset")
		}
		last = a.ID
		assets[a.ID] = true
		total += len(a.Data)
		if total > maxCompositeBytes {
			return "", 0, errors.New("composite package too large")
		}
		if media, err := compositeImageMediaType(a.Data); err != nil || media != a.MediaType {
			return "", 0, errors.New("invalid composite image asset")
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
		if r.Format != compositePNG && r.Format != compositeRTF && r.Format != compositeHTML && r.Format != compositeWeChat && r.Format != compositeQQ && (!utf8.Valid(r.Data) || bytes.IndexByte(r.Data, 0) >= 0) {
			return "", 0, errors.New("invalid composite text encoding")
		}
		if r.Format == compositePNG {
			if err := validateManagedImagePNG(r.Data); err != nil {
				return "", 0, err
			}
		}
		if len(r.Resources) > maxCompositeAssets {
			return "", 0, errors.New("too many composite resource bindings")
		}
		previous := ""
		for _, resource := range r.Resources {
			if (r.Format != compositeHTML && r.Format != compositeWeChat && r.Format != compositeQQ) || resource.Source == "" || resource.Source <= previous || len(resource.Source) > 1<<20 || !assets[resource.AssetID] {
				return "", 0, errors.New("invalid composite resource binding")
			}
			previous = resource.Source
			total += len(resource.Source) + len(resource.AssetID)
			if total > maxCompositeBytes {
				return "", 0, errors.New("composite package too large")
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
	ref = trimCompositeReference(ref)
	var raw []byte
	if isCompositeInlineReference(ref) {
		header, data, ok := strings.Cut(ref, ",")
		header = strings.ToLower(header)
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
		if info.Size() > int64(limit) {
			return nil, ErrImageLimitExceeded
		}
		if !info.Mode().IsRegular() {
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
	if _, err := compositeImageMediaType(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Preview is a fresh allowlisted rendering. Stored recovery markup is never
// inserted into the application DOM; only managed image URLs may load bytes.
func compositePreviewHTML(c *domain.CompositeContent, imageURL func(string) string) string {
	for _, format := range []string{compositeHTML, compositeWeChat, compositeQQ} {
		for _, r := range c.Representations {
			if r.Format == format {
				markup, err := compositeMarkup(r)
				if err != nil {
					continue
				}
				doc, err := compositeHTMLDocument(markup)
				if err != nil {
					continue
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
								if a.Key == "src" {
									id := compositeResourceID(r.Resources, a.Val)
									src := ""
									if id != "" {
										src = imageURL(id)
									} else if isCompositeInlineReference(a.Val) {
										src = compositeInlineImageURL(a.Val)
									}
									if src == "" {
										continue
									}
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
					continue
				}
				return out.String()
			}
		}
	}
	return ""
}
