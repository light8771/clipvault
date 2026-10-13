package clipvault

import (
	"bytes"
	"strconv"
	"strings"
)

// Only consecutive protocol fields before markup belong to the CF_HTML header.
// Text inside a copied document must never provide offsets or source metadata.
func cfHTMLHeader(raw []byte) []byte {
	header := raw[:min(len(raw), 4096)]
	if start := bytes.IndexByte(header, '<'); start >= 0 {
		header = header[:start]
	}
	end := 0
	for _, line := range bytes.SplitAfter(header, []byte{'\n'}) {
		key, _, ok := bytes.Cut(bytes.TrimSpace(line), []byte{':'})
		if !ok {
			break
		}
		switch string(key) {
		case "Version", "StartHTML", "EndHTML", "StartFragment", "EndFragment", "StartSelection", "EndSelection", "SourceURL":
			end += len(line)
		default:
			return header[:end]
		}
	}
	return header[:end]
}

func hasCFHTMLHeader(raw []byte) bool { return len(cfHTMLHeader(raw)) > 0 }

func cfHTMLOffset(raw []byte, key string) (int, bool) {
	prefix := []byte(key + ":")
	for _, line := range bytes.Split(cfHTMLHeader(raw), []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, prefix) {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(string(line[len(prefix):])))
		return value, err == nil
	}
	return 0, false
}
