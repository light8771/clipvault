package clipvault

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
	"golang.org/x/net/html"
)

// Search indexes the union of textual representations. Clipboard restoration
// still uses the original representations, never this combined search text.
func compositeTextAndKinds(c *domain.CompositeContent) (string, []string, error) {
	kinds := map[string]bool{}
	texts := map[string]string{}
	for _, r := range c.Representations {
		var text string
		switch r.Format {
		case compositeText:
			text = string(r.Data)
		case compositeHTML, compositeWeChat, compositeQQ:
			markup, err := compositeMarkup(r)
			if err != nil {
				kinds[domain.KindText] = true
				break
			}
			doc, err := compositeHTMLDocument(markup)
			if err != nil {
				kinds[domain.KindText] = true
				break
			}
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode {
					if slices.Contains([]string{"head", "script", "style", "template", "noscript"}, n.Data) {
						return
					}
					switch n.Data {
					case "img", "svg", "canvas":
						kinds[domain.KindImage] = true
					case "table":
						kinds["table"] = true
					case "a":
						for _, a := range n.Attr {
							if a.Key == "href" && a.Val != "" {
								kinds["link"] = true
							}
						}
					}
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(doc)
			var out strings.Builder
			appendHTMLText(&out, doc)
			text = normalizeExtractedText(out.String())
			if len(kinds) == 0 {
				kinds[domain.KindText] = true
			}
		case compositeRTF:
			rtfText, features, err := inspectCompositeRTF(r.Data)
			if features.table {
				kinds["table"] = true
			}
			if features.link {
				kinds["link"] = true
			}
			if features.image {
				kinds[domain.KindImage] = true
			}
			text = rtfText
			if err != nil {
				text = ""
			}
			if len(kinds) == 0 {
				kinds[domain.KindText] = true
			}
		case compositePNG:
			kinds[domain.KindImage] = true
		case compositeFiles:
			var paths []string
			if err := json.Unmarshal(r.Data, &paths); err != nil || len(paths) == 0 || len(paths) > 1000 {
				return "", nil, errors.New("invalid composite file list")
			}
			for _, path := range paths {
				if strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
					return "", nil, errors.New("invalid composite file path")
				}
				kinds[fileExtKind(path)] = true
			}
			kinds[domain.KindFiles] = true
			text = strings.Join(paths, "\n")
		default:
			return "", nil, fmt.Errorf("unsupported content representation %q", r.Format)
		}
		texts[r.Format] = text
		if r.Format != compositeFiles && strings.TrimSpace(text) != "" {
			kinds[domain.KindText] = true
		}
	}
	parts := []string{}
	seen := map[string]bool{}
	for _, format := range []string{compositeText, compositeHTML, compositeWeChat, compositeQQ, compositeRTF, compositeFiles} {
		text := texts[format]
		key := strings.Join(strings.Fields(text), " ")
		if key != "" && !seen[key] {
			parts = append(parts, text)
			seen[key] = true
		}
	}
	list := make([]string, 0, len(kinds))
	for kind := range kinds {
		list = append(list, kind)
	}
	slices.Sort(list)
	return strings.Join(parts, "\n"), list, nil
}

// A synthesized Unicode fallback comes from one preferred representation;
// combining alternatives here would duplicate content on a plain-text paste.
func compositePasteText(c *domain.CompositeContent) string {
	for _, format := range []string{compositeText, compositeHTML, compositeWeChat, compositeQQ, compositeRTF, compositeFiles} {
		for _, r := range c.Representations {
			if r.Format != format {
				continue
			}
			text, _, err := compositeTextAndKinds(&domain.CompositeContent{Representations: []domain.ContentRepresentation{r}})
			if err == nil && text != "" {
				return text
			}
		}
	}
	return ""
}
