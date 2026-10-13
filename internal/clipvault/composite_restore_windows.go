//go:build windows

package clipvault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
	"golang.org/x/net/html"
)

type clipboardRestoreRepresentation struct {
	format uint32
	data   []byte
}

func prepareClipboardRestoreRepresentations(item StoredItem, dataDir string) ([]clipboardRestoreRepresentation, error) {
	if item.RecordType != domain.HistoryRecordContent {
		return nil, errItemContentUnavailable
	}
	if item.Format != domain.FormatComposite {
		format, data, err := prepareClipboardRestorePayload(item, dataDir)
		if err != nil {
			return nil, err
		}
		return []clipboardRestoreRepresentation{{format, data}}, nil
	}
	text, _, err := validateComposite(item.Content)
	if err != nil {
		return nil, err
	}
	if item.ContentSHA256 == nil || compositeFingerprint(item.Content) != *item.ContentSHA256 || text != item.Text {
		return nil, fmt.Errorf("composite integrity mismatch")
	}
	// Validate the actual Unicode representation before materializing resources.
	// Its UTF-16 byte limit is shared with atomic text restoration.
	pasteText := compositePasteText(item.Content)
	for _, r := range item.Content.Representations {
		if r.Format == compositeText {
			pasteText = string(r.Data)
		}
	}
	textData, err := prepareClipboardText(pasteText)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, asset := range item.Content.Assets {
		dir := filepath.Join(dataDir, "restore-assets")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		// Recovery copies are rebuildable from the encrypted source. Write a new
		// file and rename, never follow an existing resource symlink.
		path := filepath.Join(dir, asset.ID+compositeImageExtension(asset.MediaType))
		if compositeCacheMatches(path, asset.ID, len(asset.Data)) {
			paths[asset.ID] = path
			continue
		}
		file, err := os.CreateTemp(dir, ".asset-*")
		if err != nil {
			return nil, err
		}
		temp := file.Name()
		_, writeErr := file.Write(asset.Data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			os.Remove(temp)
			return nil, fmt.Errorf("write restoration resource: %v %v", writeErr, closeErr)
		}
		if err := os.Rename(temp, path); err != nil {
			os.Remove(temp)
			return nil, err
		}
		paths[asset.ID] = path
	}
	out := []clipboardRestoreRepresentation{}
	add := func(format uint32, data []byte) error {
		if format == 0 {
			return fmt.Errorf("register clipboard format failed")
		}
		out = append(out, clipboardRestoreRepresentation{format, data})
		return nil
	}
	for _, r := range item.Content.Representations {
		switch r.Format {
		case compositeText:
			if err := add(cfUnicodeText, textData); err != nil {
				return nil, err
			}
		case compositeRTF:
			if err := add(registerClipboardFormat("Rich Text Format"), append([]byte(nil), r.Data...)); err != nil {
				return nil, err
			}
		case compositeWeChat, compositeQQ:
			resourcePaths := map[string]string{}
			for _, resource := range r.Resources {
				resourcePaths[resource.Source] = paths[resource.AssetID]
			}
			data, err := restoreCompositeMarkupPayload(r.Data, resourcePaths, true)
			if err != nil {
				return nil, err
			}
			if err = add(registerClipboardFormat(r.Format), data); err != nil {
				return nil, err
			}
		case compositePNG:
			if err := add(clipboardPNGFormat(), r.Data); err != nil {
				return nil, err
			}
		case compositeFiles:
			var files []string
			if err := json.Unmarshal(r.Data, &files); err != nil {
				return nil, err
			}
			if err := validateClipboardFileDropRestore(files); err != nil {
				return nil, err
			}
			out = append(out, clipboardRestoreRepresentation{cfHDrop, encodeClipboardFileDrop(files)})
		case compositeHTML:
			resourcePaths := map[string]string{}
			for _, resource := range r.Resources {
				resourcePaths[resource.Source] = paths[resource.AssetID]
			}
			markup, err := restoreCompositeHTMLPayload(r.Data, resourcePaths)
			if err != nil {
				return nil, err
			}
			if err := add(registerClipboardFormat("HTML Format"), markup); err != nil {
				return nil, err
			}
			if len(paths) > 0 {
				for _, entry := range []struct{ name, root string }{{"WeChat_RichEdit_Format", "WeChatRichEditFormat"}, {"QQ_Unicode_RichEdit_Format", "QQRichEditFormat"}} {
					present := false
					for _, original := range item.Content.Representations {
						present = present || original.Format == entry.name
					}
					if present {
						continue
					}
					if err := add(registerClipboardFormat(entry.name), encodeRichEditHTML(r.Data, resourcePaths, entry.root)); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	// Every rich-only capture also has a predictable plain-text paste option.
	hasText := false
	for _, r := range out {
		hasText = hasText || r.format == cfUnicodeText
	}
	if !hasText {
		if pasteText != "" {
			out = append(out, clipboardRestoreRepresentation{cfUnicodeText, textData})
		}
	}
	return out, nil
}

func compositeCacheMatches(path, id string, size int) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(size) {
		return false
	}
	existing, err := os.ReadFile(path)
	return err == nil && hashBytes(existing) == id
}

func encodeCFHTML(fragment string) []byte {
	prefix := "<html><body><!--StartFragment-->"
	suffix := "<!--EndFragment--></body></html>"
	template := "Version:1.0\r\nStartHTML:%010d\r\nEndHTML:%010d\r\nStartFragment:%010d\r\nEndFragment:%010d\r\n"
	headerLen := len(fmt.Sprintf(template, 0, 0, 0, 0))
	start := headerLen + len(prefix)
	return []byte(fmt.Sprintf(template, headerLen, start+len(fragment)+len(suffix), start, start+len(fragment)) + prefix + fragment + suffix + "\x00")
}

func encodeRichEditHTML(markup []byte, paths map[string]string, root string) []byte {
	doc, err := compositeHTMLDocument(markup)
	if err != nil {
		return []byte("<" + root + "></" + root + ">")
	}
	var out strings.Builder
	out.WriteString("<" + root + ">")
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out.WriteString(`<EditElement type="0"><![CDATA[` + strings.ReplaceAll(text.String(), "]]>", "]]]]><![CDATA[>") + `]]></EditElement>`)
			text.Reset()
		}
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "head" || n.Data == "script" || n.Data == "style" {
				return
			}
			if n.Data == "img" {
				for _, a := range n.Attr {
					if a.Key == "src" {
						if path, ok := paths[a.Val]; ok {
							flush()
							out.WriteString(`<EditElement type="1" filepath="` + html.EscapeString(path) + `"></EditElement>`)
						}
					}
				}
				return
			}
			if n.Data == "br" {
				text.WriteByte('\n')
			}
		}
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if n.Type == html.ElementNode && (n.Data == "td" || n.Data == "th") {
			text.WriteByte('\t')
		}
		if n.Type == html.ElementNode && htmlBlockElements[n.Data] && n.Data != "br" {
			text.WriteByte('\n')
		}
	}
	walk(doc)
	flush()
	out.WriteString("</" + root + ">")
	return []byte(out.String())
}
