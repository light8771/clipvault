package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"

	"github.com/xuri/excelize/v2"
)

type importDeadlineRecorder struct {
	*httptest.ResponseRecorder
	readDeadline  time.Time
	writeDeadline time.Time
}

func (w *importDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.readDeadline = deadline
	return nil
}

func (w *importDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadline = deadline
	return nil
}

func TestSetImportTransferDeadlines(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	w := &importDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	if err := setImportTransferDeadlines(w, now); err != nil {
		t.Fatalf("setImportTransferDeadlines: %v", err)
	}
	wantRead := now.Add(importReadTimeout)
	if !w.readDeadline.Equal(wantRead) {
		t.Fatalf("read deadline = %v, want %v", w.readDeadline, wantRead)
	}
	wantWrite := now.Add(importReadTimeout)
	if !w.writeDeadline.Equal(wantWrite) {
		t.Fatalf("write deadline = %v, want %v", w.writeDeadline, wantWrite)
	}
}

func TestSetImportProcessingDeadline(t *testing.T) {
	now := time.Date(2026, time.August, 11, 13, 0, 0, 0, time.UTC)
	w := &importDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	if err := setImportProcessingDeadline(w, now); err != nil {
		t.Fatalf("setImportProcessingDeadline: %v", err)
	}
	want := now.Add(importProcessingTimeout)
	if !w.writeDeadline.Equal(want) {
		t.Fatalf("write deadline = %v, want %v", w.writeDeadline, want)
	}
}

func TestSetExportWriteDeadline(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	w := &importDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	if err := setExportWriteDeadline(w, now); err != nil {
		t.Fatalf("setExportWriteDeadline: %v", err)
	}
	want := now.Add(exportWriteTimeout)
	if !w.writeDeadline.Equal(want) {
		t.Fatalf("write deadline = %v, want %v", w.writeDeadline, want)
	}
}

func TestTransferDeadlinesAllowUnsupportedResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	if err := setImportTransferDeadlines(w, time.Now()); err != nil {
		t.Fatalf("setImportTransferDeadlines unsupported writer: %v", err)
	}
	if err := setImportProcessingDeadline(w, time.Now()); err != nil {
		t.Fatalf("setImportProcessingDeadline unsupported writer: %v", err)
	}
	if err := setExportWriteDeadline(w, time.Now()); err != nil {
		t.Fatalf("setExportWriteDeadline unsupported writer: %v", err)
	}
}

func TestExportImportRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// Save some items
	items := []ClipboardItem{
		{RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "hello world", MetaJSON: "{}"},
		{RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "=evil formula", MetaJSON: "{}", Sensitive: true},
		{RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "你好世界", MetaJSON: "{}"},
	}
	for _, item := range items {
		if err := store.Save(ctx, item); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	sourceConfig := DefaultConfig()
	sourceConfig.Sensitive.CustomPatterns = []string{"(?i)api[_-]?key", "^\\d{6}$"}
	if err := store.SaveConfig(sourceConfig); err != nil {
		t.Fatalf("SaveConfig source: %v", err)
	}

	// Create groups
	g1, err := store.CreateGroup(ctx, "Group A", 0)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	g2, err := store.CreateGroup(ctx, "Group B", g1.ID)
	if err != nil {
		t.Fatalf("CreateGroup nested: %v", err)
	}

	// Associate items with groups
	allItems, _, err := store.Search(ctx, SearchOptions{Limit: 100, IncludeSensitive: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(allItems) != 3 {
		t.Fatalf("expected 3 items, got %d", len(allItems))
	}
	if err := store.AddItemToGroup(ctx, allItems[0].ID, g1.ID); err != nil {
		t.Fatalf("AddItemToGroup: %v", err)
	}
	if err := store.AddItemToGroup(ctx, allItems[1].ID, g2.ID); err != nil {
		t.Fatalf("AddItemToGroup: %v", err)
	}

	// Export
	export := exportAllForTest(t, store)
	if len(export.Items) != 3 {
		t.Fatalf("export items: expected 3, got %d", len(export.Items))
	}
	if len(export.Groups) != 2 {
		t.Fatalf("export groups: expected 2, got %d", len(export.Groups))
	}
	if len(export.ItemGroups) != 2 {
		t.Fatalf("export itemGroups: expected 2, got %d", len(export.ItemGroups))
	}
	if export.App != "ClipVault" {
		t.Fatalf("export app: %q", export.App)
	}
	if export.ExportVersion != currentExportVersion {
		t.Fatalf("export version: %d", export.ExportVersion)
	}
	if !sameStrings(export.Settings.Sensitive.CustomPatterns, sourceConfig.Sensitive.CustomPatterns) {
		t.Fatalf("export sensitive settings = %#v", export.Settings.Sensitive)
	}
	t.Logf("[OK] export: %d items, %d groups, %d links", len(export.Items), len(export.Groups), len(export.ItemGroups))

	// Serialize to JSON
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Import into a new store
	dir2 := t.TempDir()
	store2, err := OpenStore(StoreOptions{DataDir: dir2})
	if err != nil {
		t.Fatalf("OpenStore 2: %v", err)
	}
	defer store2.Close()

	cfgBefore := DefaultConfig()
	cfgBefore.Search.ListenAddr = "127.0.0.1:19999"
	cfgBefore.Backup.EmailBackup.SMTPPass = "keep-smtp-pass"
	cfgBefore.Backup.EmailBackup.Password = "keep-export-pass"
	cfgBefore.Sensitive.CustomPatterns = []string{"target-only"}
	if err := store2.SaveConfig(cfgBefore); err != nil {
		t.Fatalf("SaveConfig before import: %v", err)
	}

	if err := store2.ImportFullJSON(ctx, data); err != nil {
		t.Fatalf("ImportFullJSON: %v", err)
	}

	// Verify imported data
	importedItems, total, err := store2.Search(ctx, SearchOptions{Limit: 100, IncludeSensitive: true})
	if err != nil {
		t.Fatalf("Search after import: %v", err)
	}
	if total != 3 {
		t.Fatalf("imported items: expected 3, got %d", total)
	}
	t.Logf("[OK] imported %d items", total)

	// Verify groups
	importedGroups, err := store2.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups after import: %v", err)
	}
	if len(importedGroups) != 2 {
		t.Fatalf("imported groups: expected 2, got %d", len(importedGroups))
	}
	importedGroupIDs := make(map[string]int64, len(importedGroups))
	var importedChild StoredGroup
	for _, group := range importedGroups {
		importedGroupIDs[group.Name] = group.ID
		if group.Name == "Group B" {
			importedChild = group
		}
	}
	if importedChild.ParentID != importedGroupIDs["Group A"] {
		t.Fatalf("imported Group B parent = %d, want Group A", importedChild.ParentID)
	}
	t.Logf("[OK] imported %d groups", len(importedGroups))

	// Verify item-group associations
	for _, item := range importedItems {
		gids, err := store2.GetItemGroups(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetItemGroups: %v", err)
		}
		if len(gids) > 0 {
			t.Logf("[OK] item %d has %d groups", item.ID, len(gids))
		}
	}

	// Verify sensitive item preserved
	for _, item := range importedItems {
		if item.Text == "=evil formula" && !item.Sensitive {
			t.Fatalf("sensitive flag not preserved for '=evil formula'")
		}
	}
	t.Logf("[OK] sensitive flag preserved")

	// Verify only portable sensitive settings are restored.
	cfgAfter, err := store2.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig after import: %v", err)
	}
	if cfgAfter.Search.ListenAddr != cfgBefore.Search.ListenAddr {
		t.Fatalf("import changed listenAddr: got %q, want %q", cfgAfter.Search.ListenAddr, cfgBefore.Search.ListenAddr)
	}
	if cfgAfter.Backup.EmailBackup.SMTPPass != cfgBefore.Backup.EmailBackup.SMTPPass {
		t.Fatal("import changed SMTP password")
	}
	if cfgAfter.Backup.EmailBackup.Password != cfgBefore.Backup.EmailBackup.Password {
		t.Fatal("import changed email backup export password")
	}
	if !sameStrings(cfgAfter.Sensitive.CustomPatterns, sourceConfig.Sensitive.CustomPatterns) {
		t.Fatalf("imported sensitive settings = %#v", cfgAfter.Sensitive)
	}
	t.Logf("[OK] sensitive settings restored without importing credentials")
}

func TestImportRecomputesDuplicateAtTargetBoundary(t *testing.T) {
	ctx := context.Background()
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Save(ctx, ClipboardItem{
		RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "same",
	}); err != nil {
		source.Close()
		t.Fatal(err)
	}
	export := exportAllForTest(t, source)
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	jsonData, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}

	importers := map[string]func(*Store) error{
		"streaming JSON": func(store *Store) error { return store.ImportFullJSON(ctx, jsonData) },
		"materialized":   func(store *Store) error { return store.importExportData(ctx, *export) },
	}
	for name, importData := range importers {
		t.Run(name, func(t *testing.T) {
			store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Save(ctx, ClipboardItem{
				RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText, Text: "same",
			}); err != nil {
				t.Fatal(err)
			}
			if err := importData(store); err != nil {
				t.Fatal(err)
			}
			items, _, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 || !items[0].ConsecutiveDuplicate {
				t.Fatalf("import did not derive target boundary: %+v", items)
			}
		})
	}
}

func TestXLSXRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.Save(ctx, ClipboardItem{RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "=SUM(A1)\nnext line", MetaJSON: "{\"source\":\"xlsx\"}", Sensitive: true}); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sensitive.CustomPatterns = []string{"(?i)secret"}
	if err := source.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	parent, err := source.CreateGroup(ctx, "Parent", 0)
	if err != nil {
		t.Fatal(err)
	}
	child, err := source.CreateGroup(ctx, "Child", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.CreateGroup(ctx, "Empty", 0); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(ctx, source.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.AddItemToGroup(ctx, items[0].ID, child.ID); err != nil {
		t.Fatal(err)
	}
	data, err := source.ExportXLSX(ctx)
	if err != nil {
		t.Fatal(err)
	}

	target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.ImportXLSX(ctx, data); err != nil {
		t.Fatalf("ImportXLSX: %v", err)
	}
	imported, total, err := target.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil || total != 1 || imported[0].Text != "=SUM(A1)\nnext line" || !imported[0].Sensitive {
		t.Fatalf("imported records = %#v, total=%d, err=%v", imported, total, err)
	}
	groups, err := target.ListGroups(ctx)
	if err != nil || len(groups) != 3 || groups[1].ParentID != groups[0].ID {
		t.Fatalf("imported groups = %#v, err=%v", groups, err)
	}
	cfg, err = target.GetConfig()
	if err != nil || !sameStrings(cfg.Sensitive.CustomPatterns, []string{"(?i)secret"}) {
		t.Fatalf("imported sensitive settings = %#v, err=%v", cfg.Sensitive, err)
	}
}

func TestImportXLSXRejectsInvalidWorkbookAtomically(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.ImportXLSX(context.Background(), []byte("not an xlsx")); err == nil {
		t.Fatal("ImportXLSX accepted invalid data")
	}
	_, total, err := store.Search(context.Background(), SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil || total != 0 {
		t.Fatalf("invalid XLSX changed database: total=%d err=%v", total, err)
	}
}

func TestImportXLSXRequiresCurrentManifest(t *testing.T) {
	export := &ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		Settings:      ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{}}},
	}
	data, err := encodeExportXLSX(export)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*excelize.File) error
	}{
		{name: "missing manifest", mutate: func(book *excelize.File) error { return book.DeleteSheet(xlsxManifestSheet) }},
		{name: "wrong app", mutate: func(book *excelize.File) error { return book.SetCellStr(xlsxManifestSheet, "A2", "Other") }},
		{name: "previous version", mutate: func(book *excelize.File) error { return book.SetCellStr(xlsxManifestSheet, "B2", "6") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			book, err := excelize.OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if err := test.mutate(book); err != nil {
				book.Close()
				t.Fatal(err)
			}
			mutated, err := book.WriteToBuffer()
			closeErr := book.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("write=%v close=%v", err, closeErr)
			}
			store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.ImportXLSX(context.Background(), mutated.Bytes()); err == nil {
				t.Fatal("non-current XLSX manifest was accepted")
			}
		})
	}
}

func TestImportFullJSONRequiresSensitiveRules(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	data := []byte(`{"exportVersion":5,"app":"ClipVault"}`)
	err = store.ImportFullJSON(context.Background(), data)
	if err == nil || !strings.Contains(err.Error(), "settings is required") {
		t.Fatalf("missing settings import error = %v", err)
	}
	_, total, err := store.Search(context.Background(), SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil || total != 0 {
		t.Fatalf("missing settings import changed database: total=%d err=%v", total, err)
	}
}

func TestImportRejectsInvalidSensitiveRulesAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	config := DefaultConfig()
	config.Sensitive.CustomPatterns = []string{"^before-import$"}
	if err := store.SaveConfig(config); err != nil {
		t.Fatal(err)
	}
	export := ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "test",
		Items:         []StoredItem{},
		Groups:        []StoredGroup{},
		ItemGroups:    []ItemGroupLink{},
		Settings:      ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{"[invalid"}}},
	}
	jsonData, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	xlsxData, err := encodeExportXLSX(&export)
	if err != nil {
		t.Fatal(err)
	}

	for name, importData := range map[string]func() error{
		"JSON": func() error { return store.ImportFullJSON(ctx, jsonData) },
		"XLSX": func() error { return store.ImportXLSX(ctx, xlsxData) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := importData(); err == nil || !strings.Contains(err.Error(), "invalid sensitive rule") {
				t.Fatalf("import error = %v", err)
			}
			current, err := store.GetConfig()
			if err != nil {
				t.Fatal(err)
			}
			if !sameStrings(current.Sensitive.CustomPatterns, config.Sensitive.CustomPatterns) {
				t.Fatalf("invalid import changed sensitive settings: %#v", current.Sensitive)
			}
		})
	}
}

func TestImportRejectsDuplicateItemGroupLinksAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	export := ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    now.Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "test",
		Items: []StoredItem{{
			ID: 1, CreatedAt: now, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "record", ByteLen: 6, CharLen: 6, ContentSHA256: strPtrOrNil(hashString("record")), MetaJSON: "{}",
			SourceAttribution: "unknown",
		}},
		Groups:     []StoredGroup{{ID: 1, Name: "group", CreatedAt: now}},
		ItemGroups: []ItemGroupLink{{ItemID: 1, GroupID: 1}, {ItemID: 1, GroupID: 1}},
		Settings:   ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{}}},
	}
	jsonData, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	xlsxData, err := encodeExportXLSX(&export)
	if err != nil {
		t.Fatal(err)
	}

	for name, importData := range map[string]func() error{
		"JSON": func() error { return store.ImportFullJSON(ctx, jsonData) },
		"XLSX": func() error { return store.ImportXLSX(ctx, xlsxData) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := importData(); err == nil || !strings.Contains(err.Error(), "duplicate item-group link") {
				t.Fatalf("import error = %v", err)
			}
			_, total, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
			if err != nil || total != 0 {
				t.Fatalf("invalid import changed records: total=%d, err=%v", total, err)
			}
			groups, err := store.ListGroups(ctx)
			if err != nil || len(groups) != 0 {
				t.Fatalf("invalid import changed groups: %#v, err=%v", groups, err)
			}
		})
	}
}

func TestImportRejectsEventGroupLinks(t *testing.T) {
	now := time.Now().UTC()
	export := ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    now.Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "test",
		Items: []StoredItem{{
			ID: 1, CreatedAt: now, RecordType: RecordTypeEvent, MetaJSON: "{}",
			EventCode:         domain.EventClipboardEmpty,
			EventFingerprint:  buildEventFingerprint(domain.EventClipboardEmpty, ClipboardFormatSet{}, "", "", ""),
			SourceAttribution: "unknown",
		}},
		Groups:     []StoredGroup{{ID: 1, Name: "content-only", CreatedAt: now}},
		ItemGroups: []ItemGroupLink{{ItemID: 1, GroupID: 1}},
		Settings:   ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{}}},
	}
	jsonData, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	xlsxData, err := encodeExportXLSX(&export)
	if err != nil {
		t.Fatal(err)
	}

	for name, importData := range map[string][]byte{"JSON": jsonData, "XLSX": xlsxData} {
		t.Run(name, func(t *testing.T) {
			store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if name == "JSON" {
				err = store.ImportFullJSON(context.Background(), importData)
			} else {
				err = store.ImportXLSX(context.Background(), importData)
			}
			if err == nil || !strings.Contains(err.Error(), "event item") {
				t.Fatalf("event/group link import error=%v", err)
			}
		})
	}
}

func TestXLSXRejectsFormulaAndCellOverflow(t *testing.T) {
	now := time.Now().UTC()
	export := &ExportData{Items: []StoredItem{{
		ID: 1, CreatedAt: now, RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "safe", ByteLen: 4, CharLen: 4, ContentSHA256: strPtrOrNil(hashString("safe")), MetaJSON: "{}",
	}}}
	data, err := encodeExportXLSX(export)
	if err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellFormula(xlsxRecordsSheet, "E2", "=1+1"); err != nil {
		t.Fatal(err)
	}
	formulaData, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.ImportXLSX(context.Background(), formulaData.Bytes()); err == nil || !strings.Contains(err.Error(), "not a formula") {
		t.Fatalf("formula workbook error = %v", err)
	}
	if _, total, err := store.Search(context.Background(), SearchOptions{Limit: 10, IncludeSensitive: true}); err != nil || total != 0 {
		t.Fatalf("formula workbook changed database: total=%d, err=%v", total, err)
	}

	book, err = excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStr(xlsxRecordsSheet, "Y2", "unexpected"); err != nil {
		t.Fatal(err)
	}
	extraColumnData, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportXLSX(context.Background(), extraColumnData.Bytes()); err == nil || !strings.Contains(err.Error(), "unexpected column") {
		t.Fatalf("extra column workbook error = %v", err)
	}

	book, err = excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellFormula(xlsxRecordsSheet, "Y2", `=""`); err != nil {
		t.Fatal(err)
	}
	blankFormulaData, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportXLSX(context.Background(), blankFormulaData.Bytes()); err == nil || !strings.Contains(err.Error(), "unexpected column") {
		t.Fatalf("blank formula workbook error = %v", err)
	}

	book, err = excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range []string{"B2", "C2", "D2", "E2", "F2", "G2", "H2", "I2", "J2", "K2"} {
		if err := book.SetCellStr(xlsxRecordsSheet, cell, ""); err != nil {
			t.Fatal(err)
		}
	}
	missingColumnData, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportXLSX(context.Background(), missingColumnData.Bytes()); err == nil || !strings.Contains(err.Error(), "createdAt") {
		t.Fatalf("missing column workbook error = %v", err)
	}

	export.Items[0].Text = strings.Repeat("x", xlsxCellTextLimit+1)
	if _, err := encodeExportXLSX(export); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized cell error = %v", err)
	}
}

func TestXLSXExportEndpoint(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: store, logger: logger}
	result := httptest.NewRecorder()
	server.handleExportData(result, httptest.NewRequest(http.MethodGet, "/api/v1/transfer/export?format=xlsx", nil))
	if result.Code != http.StatusOK {
		t.Fatalf("XLSX export status = %d, body=%s", result.Code, result.Body.String())
	}
	if got := result.Header().Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("XLSX content type = %q", got)
	}
	book, err := excelize.OpenReader(bytes.NewReader(result.Body.Bytes()))
	if err != nil {
		t.Fatalf("XLSX export is not a workbook: %v", err)
	}
	defer book.Close()
	if !sameStrings(book.GetSheetList(), []string{xlsxRecordsSheet, xlsxGroupsSheet, xlsxItemGroupsSheet, xlsxSensitiveRulesSheet, xlsxAssetsSheet, xlsxManifestSheet}) {
		t.Fatalf("XLSX sheets = %#v", book.GetSheetList())
	}
	invalidFormat := httptest.NewRecorder()
	server.handleExportData(invalidFormat, httptest.NewRequest(http.MethodGet, "/api/v1/transfer/export?format=invalid", nil))
	if invalidFormat.Code != http.StatusBadRequest {
		t.Fatalf("invalid export format status = %d", invalidFormat.Code)
	}
	missingFormat := httptest.NewRecorder()
	server.handleExportData(missingFormat, httptest.NewRequest(http.MethodGet, "/api/v1/transfer/export", nil))
	if missingFormat.Code != http.StatusBadRequest {
		t.Fatalf("missing export format status = %d", missingFormat.Code)
	}
}

func TestImportDataReloadsSensitiveSettings(t *testing.T) {
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	sourceConfig := DefaultConfig()
	sourceConfig.Sensitive.CustomPatterns = []string{"(?i)secret", "^token$"}
	if err := source.SaveConfig(sourceConfig); err != nil {
		t.Fatal(err)
	}
	export := exportAllForTest(t, source)
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}

	target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	manager, err := NewConfigManager(target)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: target, cfgMgr: manager, logger: logger}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/import", bytes.NewReader(data))
	request.Header.Set("X-ClipVault-Import-Format", "json")
	result := httptest.NewRecorder()
	server.handleImportData(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("JSON import status = %d, body = %s", result.Code, result.Body.String())
	}
	if !sameStrings(manager.Get().Sensitive.CustomPatterns, sourceConfig.Sensitive.CustomPatterns) {
		t.Fatalf("reloaded sensitive settings = %#v", manager.Get().Sensitive)
	}
}

func TestImportDataSucceedsWhenRuntimeRefreshFails(t *testing.T) {
	ctx := context.Background()
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.Save(ctx, ClipboardItem{RecordType: "content", Kind: "text", Format: "CF_UNICODETEXT", Text: "imported", MetaJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	export := exportAllForTest(t, source)
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}

	for name, listenerErr := range map[string]error{
		"hotkey":  &HotkeyError{Msg: "hotkey registration failed"},
		"runtime": errors.New("runtime refresh failed"),
	} {
		t.Run(name, func(t *testing.T) {
			target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			manager, err := NewConfigManager(target)
			if err != nil {
				t.Fatal(err)
			}
			manager.OnChange(func(Config) error { return listenerErr })
			logger, err := NewDailyLogger(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			server := &SearchServer{store: target, cfgMgr: manager, logger: logger}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/import", bytes.NewReader(data))
			request.Header.Set("X-ClipVault-Import-Format", "json")
			result := httptest.NewRecorder()
			server.handleImportData(result, request)
			if result.Code != http.StatusOK {
				t.Fatalf("import status = %d, body = %s", result.Code, result.Body.String())
			}
			var response struct {
				OK           bool   `json:"ok"`
				HotkeyError  string `json:"hotkeyError"`
				RuntimeError string `json:"runtimeError"`
			}
			if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if !response.OK {
				t.Fatalf("response = %#v", response)
			}
			if name == "hotkey" && response.HotkeyError != listenerErr.Error() {
				t.Fatalf("hotkey error = %q", response.HotkeyError)
			}
			if name == "runtime" && response.RuntimeError != listenerErr.Error() {
				t.Fatalf("runtime error = %q", response.RuntimeError)
			}
			_, total, err := target.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
			if err != nil || total != 1 {
				t.Fatalf("committed import total = %d, err = %v", total, err)
			}
		})
	}
}

func TestImportXLSXReloadsSensitiveSettings(t *testing.T) {
	ctx := context.Background()
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	sourceConfig := DefaultConfig()
	sourceConfig.Sensitive.CustomPatterns = []string{"(?i)secret", "^token$"}
	if err := source.SaveConfig(sourceConfig); err != nil {
		t.Fatal(err)
	}
	data, err := source.ExportXLSX(ctx)
	if err != nil {
		t.Fatal(err)
	}

	target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	manager, err := NewConfigManager(target)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: target, cfgMgr: manager, logger: logger}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/import", bytes.NewReader(data))
	request.Header.Set("X-ClipVault-Import-Format", "xlsx")
	result := httptest.NewRecorder()
	server.handleImportData(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("XLSX import status = %d, body = %s", result.Code, result.Body.String())
	}
	if !sameStrings(manager.Get().Sensitive.CustomPatterns, sourceConfig.Sensitive.CustomPatterns) {
		t.Fatalf("reloaded XLSX sensitive settings = %#v", manager.Get().Sensitive)
	}
}

func TestImportCVExportReloadsSensitiveSettings(t *testing.T) {
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	sourceConfig := DefaultConfig()
	sourceConfig.Sensitive.CustomPatterns = []string{"(?i)secret", "^token$"}
	if err := source.SaveConfig(sourceConfig); err != nil {
		t.Fatal(err)
	}
	export := exportAllForTest(t, source)
	exportJSON, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncryptExport(exportJSON, "test-password")
	if err != nil {
		t.Fatal(err)
	}

	target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	manager, err := NewConfigManager(target)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: target, cfgMgr: manager, logger: logger}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/import", bytes.NewReader(data))
	request.Header.Set("X-ClipVault-Import-Format", "cvexport")
	request.Header.Set("X-ClipVault-Import-Password", "test-password")
	result := httptest.NewRecorder()
	server.handleImportData(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("cvexport import status = %d, body = %s", result.Code, result.Body.String())
	}
	if !sameStrings(manager.Get().Sensitive.CustomPatterns, sourceConfig.Sensitive.CustomPatterns) {
		t.Fatalf("reloaded cvexport sensitive settings = %#v", manager.Get().Sensitive)
	}
}

func TestCVExportCarriesSensitiveSettings(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Sensitive.CustomPatterns = []string{"(?i)private", "^\\d{6}$"}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	export := exportAllForTest(t, store)
	exportJSON, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := EncryptExport(exportJSON, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := DecryptExport(encrypted, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	var restored ExportData
	if err := json.Unmarshal(decrypted, &restored); err != nil {
		t.Fatal(err)
	}
	if !sameStrings(restored.Settings.Sensitive.CustomPatterns, cfg.Sensitive.CustomPatterns) {
		t.Fatalf("cvexport sensitive settings = %#v", restored.Settings.Sensitive)
	}
}

func TestEmptyExportUsesArrays(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	export := exportAllForTest(t, store)
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"items":[]`, `"groups":[]`, `"itemGroups":[]`, `"customPatterns":[]`} {
		if !bytes.Contains(data, []byte(field)) {
			t.Fatalf("empty export is missing %s: %s", field, data)
		}
	}
}

func TestImportDataRejectsInvalidCVExport(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: store, logger: logger}
	data, err := EncryptExport([]byte(`{}`), "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/import", bytes.NewReader(data))
	request.Header.Set("X-ClipVault-Import-Format", "cvexport")
	request.Header.Set("X-ClipVault-Import-Password", "wrong-password")
	result := httptest.NewRecorder()
	server.handleImportData(result, request)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid cvexport status = %d, body = %s", result.Code, result.Body.String())
	}
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid cvexport response is not JSON: %v", err)
	}
	if response.OK {
		t.Fatalf("invalid cvexport should be rejected, got ok=true: %#v", response)
	}
}

func TestImportDataRejectsMismatchedCVExportFormat(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	data, err := EncryptExport([]byte(`{}`), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	server := &SearchServer{store: store, logger: logger}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/import", bytes.NewReader(data))
	request.Header.Set("X-ClipVault-Import-Format", "json")
	request.Header.Set("X-ClipVault-Import-Password", "test-password")
	result := httptest.NewRecorder()
	server.handleImportData(result, request)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("mismatched cvexport response: status=%d, body=%s", result.Code, result.Body.String())
	}
}

func TestImportDataRequiresFormat(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: store, logger: logger}
	result := httptest.NewRecorder()
	server.handleImportData(result, httptest.NewRequest(http.MethodPost, "/api/v1/transfer/import", bytes.NewReader([]byte(`{}`))))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("missing import format response: status=%d, body=%s", result.Code, result.Body.String())
	}
}

func TestExportDataDoesNotLeakKey(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	export := exportAllForTest(t, store)
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	keyHex := strings.TrimSpace(string(store.dbKey))
	if keyHex != "" && strings.Contains(string(data), keyHex) {
		t.Fatalf("export JSON contains raw db_key hex!")
	}
	t.Logf("[OK] export does not contain db_key")
}
