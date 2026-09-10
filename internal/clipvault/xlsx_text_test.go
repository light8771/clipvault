package clipvault

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/xuri/excelize/v2"
)

func TestXLSXLongTextEndpointRoundTrip(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := []string{
		strings.Repeat("a", xlsxCellTextLimit),
		strings.Repeat("b", xlsxCellTextLimit+1),
		strings.Repeat("中😀\n", 25000),
		xlsxTextMarker + "literal copied text",
	}
	for _, value := range want {
		insertItem(t, store, time.Now().UTC(), value, false, false)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: store, logger: logger}
	result := httptest.NewRecorder()
	server.handleExportData(result, httptest.NewRequest(http.MethodGet, "/api/v1/transfer/export?format=xlsx", nil))
	if result.Code != http.StatusOK {
		t.Fatalf("export: %d %s", result.Code, result.Body.String())
	}
	decoded, err := decodeImportXLSX(result.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Items) != len(want) {
		t.Fatal("record count changed")
	}
	for i, item := range decoded.Items {
		if item.Text != want[i] {
			t.Fatalf("text %d changed", i)
		}
	}
	book, err := excelize.OpenReader(bytes.NewReader(result.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	for _, sheet := range book.GetSheetList() {
		rows, err := book.GetRows(sheet)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			for _, value := range row {
				if len(utf16.Encode([]rune(value))) > xlsxCellTextLimit {
					t.Fatal("oversized Excel cell")
				}
			}
		}
	}
	target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.ImportXLSX(context.Background(), result.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	got := exportAllForTest(t, target)
	for i, item := range got.Items {
		if item.Text != want[i] {
			t.Fatalf("imported text %d changed", i)
		}
	}
}

func TestXLSXTextChunksRejectCorruption(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	insertItem(t, store, time.Now().UTC(), strings.Repeat("😀", 40000), false, false)
	data, err := store.ExportXLSX(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*excelize.File) error{
		"missing sheet": func(b *excelize.File) error { return b.DeleteSheet(xlsxTextChunksSheet) },
		"missing part":  func(b *excelize.File) error { return b.RemoveRow(xlsxTextChunksSheet, 3) },
		"wrong target":  func(b *excelize.File) error { return b.SetCellStr(xlsxTextChunksSheet, "B2", "999") },
		"wrong order":   func(b *excelize.File) error { return b.SetCellStr(xlsxTextChunksSheet, "D2", "2") },
		"formula":       func(b *excelize.File) error { return b.SetCellFormula(xlsxTextChunksSheet, "F2", "1+1") },
	} {
		t.Run(name, func(t *testing.T) {
			book, err := excelize.OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer book.Close()
			if err := mutate(book); err != nil {
				t.Fatal(err)
			}
			buf, err := book.WriteToBuffer()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeImportXLSX(buf.Bytes()); err == nil {
				t.Fatal("corrupt chunks accepted")
			}
		})
	}
}
