package clipvault

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

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
