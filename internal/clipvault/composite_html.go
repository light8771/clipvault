package clipvault

import (
	"bytes"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
	"golang.org/x/net/html"
)

func compositeHTMLDocument(raw []byte) (*html.Node, error) {
	fragment, err := extractCFHTMLFragment(raw)
	if err != nil {
		return nil, err
	}
	return parseCompositeHTMLFragment(fragment)
}

func compositeResourceID(resources []domain.ContentResource, source string) string {
	for _, resource := range resources {
		if resource.Source == source {
			return resource.AssetID
		}
	}
	return ""
}

// CF_HTML selection fragments can start inside a table. Parsing a bare tr/td
// as an entire document silently drops those elements under HTML parsing rules.
func parseCompositeHTMLFragment(raw []byte) (*html.Node, error) {
	tokens := html.NewTokenizer(bytes.NewReader(raw))
	first := ""
scan:
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			break scan
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := tokens.TagName()
			first = string(name)
			break scan
		case html.TextToken:
			if strings.TrimSpace(string(tokens.Text())) != "" {
				break scan
			}
		}
	}
	prefix, suffix := "", ""
	switch first {
	case "td", "th":
		prefix, suffix = "<table><tbody><tr>", "</tr></tbody></table>"
	case "tr":
		prefix, suffix = "<table><tbody>", "</tbody></table>"
	case "thead", "tbody", "tfoot", "caption", "colgroup":
		prefix, suffix = "<table>", "</table>"
	case "col":
		prefix, suffix = "<table><colgroup>", "</colgroup></table>"
	}
	if prefix != "" {
		raw = []byte(prefix + string(raw) + suffix)
	}
	return html.Parse(bytes.NewReader(raw))
}

func compositeMarkup(r domain.ContentRepresentation) ([]byte, error) {
	if r.Format == compositeWeChat || r.Format == compositeQQ {
		return richEditHTML(r.Data)
	}
	return r.Data, nil
}
