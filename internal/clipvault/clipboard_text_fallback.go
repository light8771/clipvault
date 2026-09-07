package clipvault

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

func extractCFHTMLText(raw []byte) (string, error) {
	raw = bytes.TrimRight(raw, "\x00")
	if len(raw) == 0 {
		return "", errors.New("empty HTML clipboard payload")
	}
	var fragment []byte
	start, hasStart := cfHTMLOffset(raw, "StartFragment")
	end, hasEnd := cfHTMLOffset(raw, "EndFragment")
	if hasStart && hasEnd && start >= 0 && end >= start && end <= len(raw) {
		fragment = raw[start:end]
	} else {
		const startMarker = "<!--StartFragment-->"
		const endMarker = "<!--EndFragment-->"
		if markerStart := bytes.Index(raw, []byte(startMarker)); markerStart >= 0 {
			markerStart += len(startMarker)
			markerEnd := bytes.Index(raw[markerStart:], []byte(endMarker))
			if markerEnd >= 0 {
				fragment = raw[markerStart : markerStart+markerEnd]
			}
		}
	}
	if fragment == nil {
		startHTML, hasStartHTML := cfHTMLOffset(raw, "StartHTML")
		endHTML, hasEndHTML := cfHTMLOffset(raw, "EndHTML")
		if hasStartHTML && hasEndHTML && startHTML >= 0 && endHTML >= startHTML && endHTML <= len(raw) {
			fragment = raw[startHTML:endHTML]
		} else if hasCFHTMLHeader(raw) {
			return "", errors.New("HTML clipboard payload has invalid fragment offsets")
		} else {
			// Some applications publish a plain HTML fragment under the registered
			// format without a CF_HTML header. Keep this narrow interoperability
			// fallback, but never treat a malformed protocol header as content.
			fragment = raw
		}
	}
	doc, err := html.Parse(bytes.NewReader(bytes.TrimPrefix(fragment, []byte("\xef\xbb\xbf"))))
	if err != nil {
		return "", fmt.Errorf("parse HTML clipboard fragment: %w", err)
	}
	var out strings.Builder
	appendHTMLText(&out, doc)
	text := normalizeExtractedText(out.String())
	if text == "" {
		return "", errors.New("HTML clipboard fragment contains no text")
	}
	return text, nil
}

func hasCFHTMLHeader(raw []byte) bool {
	header := raw[:min(len(raw), 4096)]
	for _, key := range [][]byte{
		[]byte("Version:"), []byte("StartHTML:"), []byte("EndHTML:"),
		[]byte("StartFragment:"), []byte("EndFragment:"),
	} {
		if bytes.Contains(header, key) {
			return true
		}
	}
	return false
}

func cfHTMLOffset(raw []byte, key string) (int, bool) {
	prefix := []byte(key + ":")
	for _, line := range bytes.Split(raw[:min(len(raw), 4096)], []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, prefix) {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(string(line[len(prefix):])))
		return value, err == nil
	}
	return 0, false
}

var htmlBlockElements = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"br": true, "div": true, "dl": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hr": true, "li": true, "main": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "tr": true, "ul": true,
}

func appendHTMLText(out *strings.Builder, node *html.Node) {
	if node.Type == html.ElementNode {
		switch strings.ToLower(node.Data) {
		case "script", "style", "noscript", "template", "svg", "canvas":
			return
		}
		if htmlBlockElements[strings.ToLower(node.Data)] {
			appendTextSeparator(out, '\n')
		}
	}
	if node.Type == html.TextNode {
		out.WriteString(node.Data)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		appendHTMLText(out, child)
	}
	if node.Type == html.ElementNode && htmlBlockElements[strings.ToLower(node.Data)] {
		appendTextSeparator(out, '\n')
	}
}

func appendTextSeparator(out *strings.Builder, separator byte) {
	value := out.String()
	if value == "" || value[len(value)-1] == separator {
		return
	}
	out.WriteByte(separator)
}

type rtfState struct {
	skip      bool
	star      bool
	fontTable bool
	uc        int
	font      int
	codePage  int
}

type rtfFontEncoding struct {
	charset     int
	codePage    int
	hasCharset  bool
	hasCodePage bool
}

func extractRTFText(raw []byte) (string, error) {
	raw = bytes.TrimRight(raw, "\x00")
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{\\rtf")) {
		return "", errors.New("invalid RTF clipboard payload")
	}
	const defaultRTFCodePage = 1252
	documentCodePage := defaultRTFCodePage
	defaultFont := -1
	fontEncodings := make(map[int]rtfFontEncoding)
	state := rtfState{uc: 1, font: -1, codePage: documentCodePage}
	stack := []rtfState{state}
	var out strings.Builder
	var ansi []byte
	skipFallback := 0
	var pendingHighSurrogate rune
	flushPendingSurrogate := func() {
		if pendingHighSurrogate != 0 {
			out.WriteRune(unicode.ReplacementChar)
			pendingHighSurrogate = 0
		}
	}
	writeUTF16Unit := func(unit rune) {
		switch {
		case unit >= 0xD800 && unit <= 0xDBFF:
			flushPendingSurrogate()
			pendingHighSurrogate = unit
		case unit >= 0xDC00 && unit <= 0xDFFF:
			if pendingHighSurrogate == 0 {
				out.WriteRune(unicode.ReplacementChar)
				return
			}
			out.WriteRune(utf16.DecodeRune(pendingHighSurrogate, unit))
			pendingHighSurrogate = 0
		default:
			flushPendingSurrogate()
			out.WriteRune(unit)
		}
	}
	flushANSI := func() error {
		if len(ansi) == 0 {
			return nil
		}
		if !state.skip && skipFallback == 0 {
			flushPendingSurrogate()
			decoded, err := decodeRTFCodePage(ansi, state.codePage)
			if err != nil {
				return err
			}
			out.WriteString(decoded)
		}
		ansi = ansi[:0]
		return nil
	}
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '{':
			if err := flushANSI(); err != nil {
				return "", err
			}
			flushPendingSurrogate()
			stack = append(stack, state)
			i++
		case '}':
			if err := flushANSI(); err != nil {
				return "", err
			}
			flushPendingSurrogate()
			if len(stack) <= 1 {
				return "", errors.New("unbalanced RTF group")
			}
			state = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			state.codePage = activeRTFCodePage(state.font, defaultFont, documentCodePage, fontEncodings)
			i++
		case '\\':
			i++
			if i >= len(raw) {
				break
			}
			symbol := raw[i]
			if symbol == '\'' {
				if i+2 >= len(raw) {
					return "", errors.New("truncated RTF hex escape")
				}
				value, err := strconv.ParseUint(string(raw[i+1:i+3]), 16, 8)
				if err != nil {
					return "", errors.New("invalid RTF hex escape")
				}
				if skipFallback > 0 {
					skipFallback--
				} else if !state.skip {
					ansi = append(ansi, byte(value))
				}
				i += 3
				continue
			}
			if err := flushANSI(); err != nil {
				return "", err
			}
			switch symbol {
			case '\\', '{', '}':
				if skipFallback > 0 {
					skipFallback--
				} else if !state.skip {
					flushPendingSurrogate()
					out.WriteByte(symbol)
				}
				i++
				continue
			case '*':
				state.star = true
				i++
				continue
			case '~':
				if skipFallback > 0 {
					skipFallback--
				} else if !state.skip {
					flushPendingSurrogate()
					out.WriteRune('\u00a0')
				}
				i++
				continue
			case '-':
				if skipFallback > 0 {
					skipFallback--
				} else if !state.skip {
					flushPendingSurrogate()
					out.WriteRune('\u00ad')
				}
				i++
				continue
			case '_':
				if skipFallback > 0 {
					skipFallback--
				} else if !state.skip {
					flushPendingSurrogate()
					out.WriteRune('\u2011')
				}
				i++
				continue
			}
			// Every remaining non-letter is an unsupported control symbol.
			// Consume it so malformed or future RTF cannot stall the parser.
			if !isASCIIAlpha(symbol) {
				i++
				continue
			}
			start := i
			for i < len(raw) && isASCIIAlpha(raw[i]) {
				i++
			}
			word := string(raw[start:i])
			sign := 1
			if i < len(raw) && raw[i] == '-' {
				sign = -1
				i++
			}
			numberStart := i
			for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
				i++
			}
			hasNumber := i > numberStart
			number := 0
			if hasNumber {
				parsed, err := strconv.Atoi(string(raw[numberStart:i]))
				if err != nil {
					return "", errors.New("invalid RTF numeric parameter")
				}
				number = parsed
				number *= sign
			}
			if i < len(raw) && raw[i] == ' ' {
				i++
			}
			if word == "fonttbl" {
				state.skip = true
				state.fontTable = true
				state.star = false
				continue
			}
			if state.star {
				state.skip = true
				state.fontTable = false
				state.star = false
				continue
			}
			if state.fontTable {
				switch word {
				case "f":
					if hasNumber {
						state.font = number
					}
				case "fcharset":
					if hasNumber && state.font >= 0 {
						fontEncoding := fontEncodings[state.font]
						fontEncoding.charset = number
						fontEncoding.hasCharset = true
						fontEncodings[state.font] = fontEncoding
					}
				case "cpg":
					if hasNumber && state.font >= 0 {
						fontEncoding := fontEncodings[state.font]
						fontEncoding.codePage = number
						fontEncoding.hasCodePage = true
						fontEncodings[state.font] = fontEncoding
					}
				}
				continue
			}
			if isRTFDestination(word) {
				state.skip = true
				continue
			}
			switch word {
			case "uc":
				if hasNumber && number >= 0 && number <= 16 {
					state.uc = number
				}
			case "ansi":
				if !state.skip {
					documentCodePage = 1252
					state.codePage = activeRTFCodePage(state.font, defaultFont, documentCodePage, fontEncodings)
				}
			case "mac":
				if !state.skip {
					documentCodePage = 10000
					state.codePage = activeRTFCodePage(state.font, defaultFont, documentCodePage, fontEncodings)
				}
			case "pc":
				if !state.skip {
					documentCodePage = 437
					state.codePage = activeRTFCodePage(state.font, defaultFont, documentCodePage, fontEncodings)
				}
			case "pca":
				if !state.skip {
					documentCodePage = 850
					state.codePage = activeRTFCodePage(state.font, defaultFont, documentCodePage, fontEncodings)
				}
			case "ansicpg":
				if hasNumber && !state.skip {
					documentCodePage = number
					state.codePage = activeRTFCodePage(state.font, defaultFont, documentCodePage, fontEncodings)
				}
			case "deff":
				if hasNumber && !state.skip {
					defaultFont = number
					state.codePage = activeRTFCodePage(state.font, defaultFont, documentCodePage, fontEncodings)
				}
			case "f":
				if hasNumber {
					state.font = number
					state.codePage = activeRTFCodePage(state.font, defaultFont, documentCodePage, fontEncodings)
				}
			case "u":
				if hasNumber && !state.skip {
					value := number
					if value < 0 {
						value += 65536
					}
					writeUTF16Unit(rune(value))
					skipFallback = state.uc
				}
			case "par", "line", "row", "nestrow", "page", "sect", "column":
				if !state.skip {
					flushPendingSurrogate()
					appendTextSeparator(&out, '\n')
				}
			case "tab", "cell", "nestcell":
				if !state.skip {
					flushPendingSurrogate()
					out.WriteByte('\t')
				}
			case "emdash", "endash", "bullet", "lquote", "rquote", "ldblquote", "rdblquote", "emspace", "enspace", "qmspace", "zwj", "zwnj", "ltrmark", "rtlmark", "zwbo", "zwnbo":
				if !state.skip {
					flushPendingSurrogate()
					out.WriteRune(rtfTextControlRune(word))
				}
			case "bin":
				if hasNumber && number > 0 {
					i = min(len(raw), i+number)
				}
			}
		case '\r', '\n':
			// RTF source line breaks are formatting whitespace, not document text.
			i++
		default:
			if skipFallback > 0 {
				skipFallback--
			} else if !state.skip {
				flushPendingSurrogate()
				ansi = append(ansi, raw[i])
			}
			i++
		}
	}
	if err := flushANSI(); err != nil {
		return "", err
	}
	flushPendingSurrogate()
	if len(stack) != 1 {
		return "", errors.New("unbalanced RTF group")
	}
	text := normalizeExtractedText(out.String())
	if text == "" {
		return "", errors.New("RTF clipboard payload contains no text")
	}
	return text, nil
}

func isRTFDestination(word string) bool {
	switch word {
	case "colortbl", "stylesheet", "info", "pict", "object", "header", "headerf", "headerl", "headerr", "footer", "footerf", "footerl", "footerr", "generator", "datastore", "themedata":
		return true
	default:
		return false
	}
}

func activeRTFCodePage(font, defaultFont, documentCodePage int, fonts map[int]rtfFontEncoding) int {
	if font < 0 {
		font = defaultFont
	}
	fontEncoding, ok := fonts[font]
	if !ok {
		return documentCodePage
	}
	if fontEncoding.hasCodePage {
		return fontEncoding.codePage
	}
	if !fontEncoding.hasCharset || fontEncoding.charset == 1 {
		return documentCodePage
	}
	if codePage, ok := rtfCharsetCodePage(fontEncoding.charset); ok {
		return codePage
	}
	return -1000 - fontEncoding.charset
}

func rtfCharsetCodePage(charset int) (int, bool) {
	switch charset {
	case 0:
		return 1252, true
	case 2:
		return 42, true
	case 77:
		return 10000, true
	case 78:
		return 10001, true
	case 79:
		return 10003, true
	case 80:
		return 10008, true
	case 81:
		return 10002, true
	case 83:
		return 10005, true
	case 84:
		return 10004, true
	case 85:
		return 10006, true
	case 86:
		return 10081, true
	case 87:
		return 10021, true
	case 88:
		return 10029, true
	case 89:
		return 10007, true
	case 128:
		return 932, true
	case 129:
		return 949, true
	case 130:
		return 1361, true
	case 134:
		return 936, true
	case 136:
		return 950, true
	case 161:
		return 1253, true
	case 162:
		return 1254, true
	case 163:
		return 1258, true
	case 177:
		return 1255, true
	case 178:
		return 1256, true
	case 186:
		return 1257, true
	case 204:
		return 1251, true
	case 222:
		return 874, true
	case 238:
		return 1250, true
	case 254:
		return 437, true
	case 255:
		return 850, true
	default:
		return 0, false
	}
}

func rtfTextControlRune(word string) rune {
	switch word {
	case "emdash":
		return '—'
	case "endash":
		return '–'
	case "bullet":
		return '•'
	case "lquote":
		return '‘'
	case "rquote":
		return '’'
	case "ldblquote":
		return '“'
	case "rdblquote":
		return '”'
	case "emspace":
		return ' '
	case "enspace":
		return ' '
	case "qmspace":
		return ' '
	case "zwj":
		return '‍'
	case "zwnj":
		return '‌'
	case "ltrmark":
		return '‎'
	case "rtlmark":
		return '‏'
	case "zwbo":
		return '​'
	case "zwnbo":
		return '\ufeff'
	default:
		panic("unsupported RTF text control")
	}
}

func decodeRTFCodePage(raw []byte, codePage int) (string, error) {
	if codePage <= -1000 {
		return "", fmt.Errorf("unsupported RTF font charset %d", -1000-codePage)
	}
	var enc encoding.Encoding
	switch codePage {
	case 65001:
		if !utf8.Valid(raw) {
			return "", errors.New("invalid UTF-8 bytes in RTF")
		}
		return string(raw), nil
	case 936:
		enc = simplifiedchinese.GBK
	case 950:
		enc = traditionalchinese.Big5
	case 932:
		enc = japanese.ShiftJIS
	case 949:
		enc = korean.EUCKR
	case 437:
		enc = charmap.CodePage437
	case 850:
		enc = charmap.CodePage850
	case 10000:
		enc = charmap.Macintosh
	case 874:
		enc = charmap.Windows874
	case 1250:
		enc = charmap.Windows1250
	case 1251:
		enc = charmap.Windows1251
	case 1252:
		enc = charmap.Windows1252
	case 1253:
		enc = charmap.Windows1253
	case 1254:
		enc = charmap.Windows1254
	case 1255:
		enc = charmap.Windows1255
	case 1256:
		enc = charmap.Windows1256
	case 1257:
		enc = charmap.Windows1257
	case 1258:
		enc = charmap.Windows1258
	default:
		return "", fmt.Errorf("unsupported RTF code page %d", codePage)
	}
	decoded, _, err := transform.String(enc.NewDecoder(), string(raw))
	return decoded, err
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func normalizeExtractedText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	result := strings.TrimSpace(strings.Join(lines, "\n"))
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return result
}
