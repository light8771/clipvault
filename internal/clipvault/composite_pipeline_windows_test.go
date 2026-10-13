//go:build windows

package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeBrowserStylesNewStoreRestore(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(strconv.FormatBool(missing), func(t *testing.T) {
			store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			style := "font-variant-caps: normal; display: inline !important; float: none; background: conic-gradient(red 0%, blue 100%); border-image: none; text-shadow: none"
			markup := `<p><button type="button" onclick="run()"><a href="https://example.com/issue" style="` + style + `; caret-color: black">issue details</a></button></p>`
			if missing {
				markup += `<p>extra<img src="C:\missing.png"></p>`
			}
			snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"HTML Format": []byte(markup)}, nil, readCompositeImage)
			if err != nil || snapshot == nil || snapshot.Content == nil {
				t.Fatalf("capture failed: %v", err)
			}
			ctx := context.Background()
			if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, ContentSHA256: snapshot.SHA256}); err != nil {
				t.Fatal(err)
			}
			items, err := exportAll(ctx, store.db)
			if err != nil || len(items) != 1 {
				t.Fatalf("stored items=%d err=%v", len(items), err)
			}
			if (len(items[0].Content.Issues) > 0) != missing {
				t.Fatalf("incorrect persisted diagnostics: %+v", items[0].Content.Issues)
			}
			restored, err := prepareClipboardRestoreRepresentations(items[0], store.dataDir)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, rep := range restored {
				if rep.format != registerClipboardFormat("HTML Format") {
					continue
				}
				found = true
				fragment, err := extractCFHTMLFragment(rep.data)
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{style, `href="https://example.com/issue"`, "issue details"} {
					if !strings.Contains(string(fragment), want) {
						t.Fatalf("restoration lost %q", want)
					}
				}
				if !bytes.Equal(rep.data, []byte(markup)) {
					t.Fatal("restoration changed raw HTML")
				}

			}
			if !found {
				t.Fatal("restoration omitted HTML")
			}
		})
	}
}

func TestCompositeRetryStorageTransferAndRestore(t *testing.T) {
	ctx := context.Background()
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, missing := range []bool{false, true} {
		previous := retryCompositeFixture(t, map[string][]byte{"HTML Format": []byte(`<p>kept<img src="C:\image.png"></p>`)}, []domain.ContentIssue{{Format: "Rich Text Format", Reason: "data_unavailable"}}, missing)
		current := retryCompositeFixture(t, map[string][]byte{"Rich Text Format": []byte(`{\rtf1 kept}`)}, []domain.ContentIssue{{Format: "HTML Format", Reason: "data_unavailable"}}, false)
		merged := mergeCompositeRetry(DefaultConfig(), previous, current)
		if err := source.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: merged.Kind, Format: merged.Format, Text: merged.Text, Content: merged.Content, ContentSHA256: merged.SHA256}); err != nil {
			t.Fatal(err)
		}
	}
	original, err := exportAll(ctx, source.db)
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, store *Store) {
		t.Helper()
		items, err := exportAll(ctx, store.db)
		if err != nil || len(items) != 2 {
			t.Fatalf("items=%d err=%v", len(items), err)
		}
		summaries, _, err := store.Search(ctx, SearchOptions{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		server, _, session := newMaskTestServer(t, store)
		for i, item := range items {
			if compositeJSONString(item.Content) != compositeJSONString(original[i].Content) {
				t.Fatal("transfer changed content or diagnostics")
			}
			index := slices.IndexFunc(summaries, func(s StoredItem) bool { return s.ID == item.ID })
			if index < 0 || !slices.Equal(summaries[index].Content.Issues, item.Content.Issues) {
				t.Fatal("list diagnostics differ from stored content")
			}
			for _, rep := range summaries[index].Content.Representations {
				if len(rep.Data) != 0 {
					t.Fatal("list response contains recovery payload")
				}
			}
			request := httptest.NewRequest(http.MethodGet, "/?id="+strconv.FormatInt(item.ID, 10), nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			response := httptest.NewRecorder()
			server.handleCompositePreview(response, request)
			var preview struct {
				Incomplete bool   `json:"incomplete"`
				Text       string `json:"text"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &preview) != nil || preview.Incomplete != (len(item.Content.Issues) > 0) || preview.Text != "kept" {
				t.Fatalf("preview lost state: status=%d body=%s", response.Code, response.Body.String())
			}
			restored, err := prepareClipboardRestoreRepresentations(item, store.dataDir)
			if err != nil {
				t.Fatal(err)
			}
			formats := map[uint32][]byte{}
			for _, rep := range restored {
				formats[rep.format] = rep.data
			}
			if !bytes.Equal(formats[cfUnicodeText], encodeClipboardText("kept")) || len(formats[registerClipboardFormat("Rich Text Format")]) == 0 {
				t.Fatal("restore lost text or RTF")
			}
			fragment, err := extractCFHTMLFragment(formats[registerClipboardFormat("HTML Format")])
			if err != nil || bytes.Contains(fragment, []byte("clipvault-asset:")) {
				t.Fatal("restore lost HTML or left unresolved assets")
			}
			if len(item.Content.Assets) > 0 && !bytes.Contains(fragment, []byte("file:///")) {
				t.Fatal("restore did not materialize image")
			}
		}
	}
	check(t, source)
	jsonData, err := json.Marshal(exportAllForTest(t, source))
	if err != nil {
		t.Fatal(err)
	}
	xlsx, err := source.ExportXLSX(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"JSON", "XLSX"} {
		t.Run(format, func(t *testing.T) {
			target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if format == "JSON" {
				err = target.ImportFullJSON(ctx, jsonData)
			} else {
				err = target.ImportXLSX(ctx, xlsx)
			}
			if err != nil {
				t.Fatal(err)
			}
			check(t, target)
		})
	}
}
