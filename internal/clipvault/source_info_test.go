package clipvault

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSourceInfoRoundtrip 验证来源字段经 Save → Search/GetByID 全链路不丢失。
func TestSourceInfoRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	item := ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "source test", ByteLen: 11, CharLen: 11,
		SHA256: "src001", MetaJSON: "{}",
		SourceExeName:     "notepad.exe",
		SourceExePath:     `C:\Windows\System32\notepad.exe`,
		SourceWindowTitle: "无标题 - 记事本",
		SourceMethod:      "foreground",
	}
	if err := store.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}

	items, total, err := store.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 item, got total=%d len=%d", total, len(items))
	}
	got := items[0]
	if got.SourceExeName != item.SourceExeName || got.SourceExePath != item.SourceExePath ||
		got.SourceWindowTitle != item.SourceWindowTitle || got.SourceMethod != item.SourceMethod {
		t.Fatalf("Search source fields mismatch: %#v", got)
	}

	byID, err := store.GetByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.SourceExeName != item.SourceExeName || byID.SourceMethod != item.SourceMethod {
		t.Fatalf("GetByID source fields mismatch: %#v", byID)
	}
}

// TestExportCarriesSourceFields 验证 JSON 导出携带来源字段（cvexport 是同一份 JSON 加密）。
func TestExportCarriesSourceFields(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Save(ctx, ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "export source", ByteLen: 13, CharLen: 13,
		SHA256: "exp001", MetaJSON: "{}",
		SourceExeName: "msedge.exe", SourceMethod: "foreground",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	export, err := store.ExportFullForJSON(ctx)
	if err != nil {
		t.Fatalf("ExportFullForJSON: %v", err)
	}
	if len(export.Items) != 1 {
		t.Fatalf("expected 1 exported item, got %d", len(export.Items))
	}
	if export.Items[0].SourceExeName != "msedge.exe" || export.Items[0].SourceMethod != "foreground" {
		t.Fatalf("exported source fields mismatch: %#v", export.Items[0])
	}

	data, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	if !strings.Contains(string(data), `"sourceExeName":"msedge.exe"`) {
		t.Fatalf("export JSON missing sourceExeName: %s", data)
	}
}

// TestDefaultConfigRecordsSourceInfo 验证默认开启来源记录。
func TestDefaultConfigRecordsSourceInfo(t *testing.T) {
	if !DefaultConfig().RecordSourceInfo {
		t.Fatal("DefaultConfig().RecordSourceInfo should be true")
	}
}
