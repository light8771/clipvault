package clipvault

import (
	"bytes"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type compositeByteEdit struct {
	start, end int
	value      []byte
}

var compositeHTMLOffsetField = regexp.MustCompile(`(?m)^(?:StartHTML|EndHTML|StartFragment|EndFragment|StartSelection|EndSelection):[ \t]*(-?[0-9]+)`)

// Only saved image references change on restore. Every other byte, including
// unfamiliar CSS, SVG, malformed browser markup and CF_HTML context, survives.
func restoreCompositeHTMLPayload(raw []byte, paths map[string]string) ([]byte, error) {
	return restoreCompositeMarkupPayload(raw, paths, false)
}

func restoreCompositeMarkupPayload(raw []byte, paths map[string]string, richEdit bool) ([]byte, error) {
	if len(paths) == 0 {
		return append([]byte(nil), raw...), nil
	}
	var edits []compositeByteEdit
	tag, attr := "img", "src"
	if richEdit {
		tag, attr = "editelement", "filepath"
	}
	tokens := html.NewTokenizer(bytes.NewReader(raw))
	offset := 0
	for {
		typ := tokens.Next()
		tokenRaw := append([]byte(nil), tokens.Raw()...)
		start := offset
		offset += len(tokenRaw)
		if typ == html.ErrorToken {
			break
		}
		if typ != html.StartTagToken && typ != html.SelfClosingTagToken {
			continue
		}
		token := tokens.Token()
		if token.Data != tag {
			continue
		}
		var source string
		for _, a := range token.Attr {
			if a.Key == attr {
				source = a.Val
				break
			}
		}
		path, ok := paths[source]
		if !ok {
			continue
		}
		if path == "" {
			return nil, fmt.Errorf("missing restoration resource")
		}
		valueStart, valueEnd, found := compositeAttributeRange(tokenRaw, attr)
		if !found {
			return nil, fmt.Errorf("invalid image source attribute")
		}
		u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
		replacement := u.String()
		if richEdit {
			replacement = path
		}
		edits = append(edits, compositeByteEdit{start + valueStart, start + valueEnd, []byte(`"` + html.EscapeString(replacement) + `"`)})
	}
	if len(edits) == 0 {
		return append([]byte(nil), raw...), nil
	}
	// Header offsets are positions in the original byte stream. Updating their
	// decimal fields can itself change header length, so converge the edits.
	fields := compositeHTMLOffsetField.FindAllSubmatchIndex(cfHTMLHeader(raw), -1)
	for _, f := range fields {
		edits = append(edits, compositeByteEdit{f[2], f[3], append([]byte(nil), raw[f[2]:f[3]]...)})
	}
	slices.SortFunc(edits, func(a, b compositeByteEdit) int { return a.start - b.start })
	for attempt := 0; attempt < 16; attempt++ {
		changed := false
		for _, f := range fields {
			original, err := strconv.Atoi(string(raw[f[2]:f[3]]))
			if err != nil || original < 0 {
				continue
			}
			if original > len(raw) {
				return nil, fmt.Errorf("invalid CF_HTML offset")
			}
			position := original
			for _, edit := range edits {
				if edit.end <= original {
					position += len(edit.value) - (edit.end - edit.start)
				} else if edit.start < original {
					return nil, fmt.Errorf("CF_HTML offset intersects resource")
				}
			}
			value := []byte(fmt.Sprintf("%0*d", f[3]-f[2], position))
			for i := range edits {
				if edits[i].start == f[2] && !bytes.Equal(edits[i].value, value) {
					edits[i].value = value
					changed = true
				}
			}
		}
		if !changed {
			var out bytes.Buffer
			cursor := 0
			for _, edit := range edits {
				if edit.start < cursor {
					return nil, fmt.Errorf("overlapping HTML edits")
				}
				out.Write(raw[cursor:edit.start])
				out.Write(edit.value)
				cursor = edit.end
			}
			out.Write(raw[cursor:])
			return out.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("CF_HTML offsets did not converge")
}

// Scan attribute boundaries instead of matching text inside another attribute's
// quoted value (e.g. data-example="src='not-an-image'").
func compositeAttributeRange(raw []byte, target string) (int, int, bool) {
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }
	i := 1
	for i < len(raw) && !space(raw[i]) && raw[i] != '/' && raw[i] != '>' {
		i++
	}
	for i < len(raw) {
		for i < len(raw) && (space(raw[i]) || raw[i] == '/') {
			i++
		}
		if i >= len(raw) || raw[i] == '>' {
			break
		}
		start := i
		for i < len(raw) && !space(raw[i]) && raw[i] != '=' && raw[i] != '>' && raw[i] != '/' {
			i++
		}
		name := string(raw[start:i])
		for i < len(raw) && space(raw[i]) {
			i++
		}
		if i >= len(raw) {
			break
		}
		if raw[i] != '=' {
			if i == start {
				i++
			}
			continue
		}
		i++
		for i < len(raw) && space(raw[i]) {
			i++
		}
		start = i
		if i < len(raw) && (raw[i] == '\'' || raw[i] == '"') {
			quote := raw[i]
			i++
			for i < len(raw) && raw[i] != quote {
				i++
			}
			if i < len(raw) {
				i++
			}
		} else {
			for i < len(raw) && !space(raw[i]) && raw[i] != '>' {
				i++
			}
		}
		if strings.EqualFold(name, target) {
			return start, i, true
		}
	}
	return 0, 0, false
}

func compositeSourceURL(raw []byte) string {
	for _, line := range strings.Split(string(cfHTMLHeader(raw)), "\n") {
		if strings.HasPrefix(line, "SourceURL:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "SourceURL:"))
		}
	}
	return ""
}
