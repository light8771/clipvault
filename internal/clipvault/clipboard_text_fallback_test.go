package clipvault

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExtractCFHTMLTextUsesFragmentAndSkipsActiveContent(t *testing.T) {
	raw := []byte(`Version:1.0
<!--StartFragment--><div>Hello &amp; <b>world</b></div><script>steal()</script><p>第二行</p><!--EndFragment-->`)
	got, err := extractCFHTMLText(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello & world\n第二行" {
		t.Fatalf("HTML text = %q", got)
	}
}

func TestExtractCFHTMLTextUsesHTMLOffsetsWhenFragmentOffsetsAreUnavailable(t *testing.T) {
	body := `<html><body><p>offset body</p></body></html>`
	headerTemplate := "Version:1.0\r\nStartHTML:%010d\r\nEndHTML:%010d\r\nStartFragment:-1\r\nEndFragment:-1\r\n"
	placeholder := fmt.Sprintf(headerTemplate, 0, 0)
	start := len(placeholder)
	header := fmt.Sprintf(headerTemplate, start, start+len(body))
	got, err := extractCFHTMLText([]byte(header + body))
	if err != nil {
		t.Fatal(err)
	}
	if got != "offset body" {
		t.Fatalf("HTML text = %q", got)
	}
}

func TestExtractCFHTMLTextRejectsMalformedProtocolHeader(t *testing.T) {
	raw := []byte("Version:1.0\r\nStartHTML:broken\r\nEndHTML:broken\r\n<p>body</p>")
	got, err := extractCFHTMLText(raw)
	if err == nil {
		t.Fatalf("malformed CF_HTML header was persisted as text: %q", got)
	}
	if strings.Contains(got, "Version") {
		t.Fatalf("protocol header leaked into content: %q", got)
	}
}

func TestExtractRTFTextSupportsUnicodeAndCodePages(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unicode controls", raw: `{\rtf1\ansi\uc1 \u20320?\u22909?\par world}`, want: "你好\nworld"},
		{name: "unicode surrogate pair", raw: `{\rtf1\ansi\uc1 emoji \u-10179?\u-8704?}`, want: "emoji 😀"},
		{name: "GBK hex", raw: `{\rtf1\ansi\ansicpg936 \'c4\'e3\'ba\'c3}`, want: "你好"},
		{name: "Windows-1252 hex", raw: `{\rtf1\ansi\ansicpg1252 caf\'e9}`, want: "café"},
		{name: "Mac Roman hex", raw: `{\rtf1\mac caf\'8e}`, want: "café"},
		{name: "PC code page hex", raw: `{\rtf1\pc caf\'82}`, want: "café"},
		{name: "PCA code page hex", raw: `{\rtf1\pca caf\'82}`, want: "café"},
		{name: "visible punctuation controls", raw: `{\rtf1\ansi \lquote single\rquote\~\ldblquote double\rdblquote}`, want: "‘single’\u00a0“double”"},
		{name: "visible spacing controls", raw: `{\rtf1\ansi left\enspace middle\emspace wide\qmspace end}`, want: "left\u2002middle\u2003wide\u2005end"},
		{name: "visible control symbols", raw: `{\rtf1\ansi a\~b\_c\-d}`, want: "a\u00a0b\u2011c\u00add"},
		{name: "table and page separators", raw: `{\rtf1\ansi one\cell two\row next\page final}`, want: "one\ttwo\nnext\nfinal"},
		{name: "font charset switch", raw: `{\rtf1\ansi\ansicpg1252\deff0{\fonttbl{\f0\fnil\fcharset0 Arial;}{\f1\fnil\fcharset204 Cyrillic;}}\f0 Latin \f1 \'cf\'f0\'e8\'e2\'e5\'f2}`, want: "Latin Привет"},
		{name: "ANSI font charset overrides document code page", raw: `{\rtf1\ansi\ansicpg936{\fonttbl{\f0\fnil\fcharset0 Arial;}}\f0 caf\'e9}`, want: "café"},
		{name: "font code page override", raw: `{\rtf1\ansi\ansicpg1252{\fonttbl{\f2\fnil\fcharset0\cpg936 SimSun;}}\f2 \'c4\'e3\'ba\'c3}`, want: "你好"},
		{name: "skipped destination cannot change encoding", raw: `{\rtf1\ansi\ansicpg1252{\info\ansicpg936}caf\'e9}`, want: "café"},
		{name: "destinations skipped", raw: `{\rtf1{\fonttbl{\f0 Arial;}}visible}`, want: "visible"},
		{name: "source formatting lines ignored", raw: "{\\rtf1\\ansi\r\nfirst \\b bold\\b0\r\n text}", want: "first bold text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := extractRTFText([]byte(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("RTF text = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExtractRTFTextConsumesUnknownControlSymbol(t *testing.T) {
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := extractRTFText([]byte(`{\rtf1 before\?after}`))
		done <- result{text: text, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.text != "beforeafter" {
			t.Fatalf("RTF text = %q, want %q", got.text, "beforeafter")
		}
	case <-time.After(time.Second):
		t.Fatal("RTF parser stalled on an unknown control symbol")
	}
}

func TestExtractRTFTextRejectsUnsupportedCodePage(t *testing.T) {
	got, err := extractRTFText([]byte(`{\rtf1\ansi\ansicpg42 \'cf}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported RTF code page 42") {
		t.Fatalf("unsupported code page result = %q, err=%v", got, err)
	}
}

func TestExtractRTFTextRejectsUnsupportedFontCharset(t *testing.T) {
	got, err := extractRTFText([]byte(`{\rtf1\ansi{\fonttbl{\f1\fnil\fcharset3 Unknown;}}\f1 abc}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported RTF font charset 3") {
		t.Fatalf("unsupported font charset result = %q, err=%v", got, err)
	}
}

func TestExtractRTFTextRejectsOversizedNumericParameter(t *testing.T) {
	got, err := extractRTFText([]byte(`{\rtf1\ansi\ansicpg999999999999999999999 text}`))
	if err == nil || !strings.Contains(err.Error(), "invalid RTF numeric parameter") {
		t.Fatalf("oversized numeric parameter result = %q, err=%v", got, err)
	}
}

func FuzzExtractRTFTextControlSymbols(f *testing.F) {
	f.Add([]byte(`before\?after`))
	f.Add([]byte(`text\:more`))
	f.Add([]byte(`\u20320?`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 4096 {
			t.Skip()
		}
		raw := make([]byte, 0, len(body)+8)
		raw = append(raw, `{\rtf1 `...)
		raw = append(raw, body...)
		raw = append(raw, '}')
		_, _ = extractRTFText(raw)
	})
}

func TestTextFallbackRejectsEmptyOrMalformedPayloads(t *testing.T) {
	for name, run := range map[string]func() error{
		"empty HTML": func() error { _, err := extractCFHTMLText(nil); return err },
		"empty RTF":  func() error { _, err := extractRTFText(nil); return err },
		"bad RTF":    func() error { _, err := extractRTFText([]byte(`{\rtf1 broken`)); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("malformed payload was accepted")
			}
		})
	}
}
