package clipvault

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

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
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "hello world", ByteLen: 11, CharLen: 11, SHA256: "abc123", MetaJSON: "{}"},
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "=evil formula", ByteLen: 13, CharLen: 13, SHA256: "def456", MetaJSON: "{}", Sensitive: true},
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "你好世界", ByteLen: 12, CharLen: 4, SHA256: "ghi789", MetaJSON: "{}"},
	}
	for _, item := range items {
		if err := store.Save(ctx, item); err != nil {
			t.Fatalf("Save: %v", err)
		}
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
	export, err := store.ExportFullForJSON(ctx)
	if err != nil {
		t.Fatalf("ExportFullForJSON: %v", err)
	}
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
	if export.ExportVersion != 2 {
		t.Fatalf("export version: %d", export.ExportVersion)
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
	if err := store2.SaveConfig(cfgBefore); err != nil {
		t.Fatalf("SaveConfig before import: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}
	cfgFromImport := DefaultConfig()
	cfgFromImport.Search.ListenAddr = "0.0.0.0:18765"
	cfgFromImport.Backup.EmailBackup.SMTPPass = "clobbered"
	cfgFromImport.Backup.EmailBackup.Password = "clobbered"
	payload["config"] = cfgFromImport
	data, err = json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload with config: %v", err)
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

	// Verify config embedded in data imports is ignored.
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
	t.Logf("[OK] config embedded in import payload was ignored")
}

func TestCSVSafe(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"=SUM(A1)", "'=SUM(A1)"},
		{"+123", "'+123"},
		{"-456", "'-456"},
		{"@cmd", "'@cmd"},
		{"normal text", "normal text"},
		{"", ""},
		{"hello", "hello"},
	}
	for _, tt := range tests {
		got := csvSafe(tt.input)
		if got != tt.expect {
			t.Errorf("csvSafe(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

func TestExportDataDoesNotLeakKey(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	export, err := store.ExportFullForJSON(context.Background())
	if err != nil {
		t.Fatal(err)
	}
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
