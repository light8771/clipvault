package clipvault

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestStageJSONImportStreamsPayloadsToDisk(t *testing.T) {
	now := time.Now().UTC()
	text := "large-ish staged payload"
	export := ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    now.Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "test",
		Items: []StoredItem{{
			ID: 1, CreatedAt: now, Kind: "text", Format: "CF_UNICODETEXT", Text: text,
			ByteLen: len([]byte(text)), CharLen: textRuneLen(text), SHA256: hashString(text), MetaJSON: `{}`,
		}},
		Groups:     []StoredGroup{{ID: 10, Name: "group", CreatedAt: now}},
		ItemGroups: []ItemGroupLink{{ItemID: 1, GroupID: 10}},
		Settings:   ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{`secret\d+`}}},
	}
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := stageJSONImport(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	itemsPath, linksPath := staged.items.Name(), staged.links.Name()
	if staged.itemCount != 1 || staged.linkCount != 1 || len(staged.groups) != 1 {
		t.Fatalf("unexpected staged counts: items=%d links=%d groups=%d", staged.itemCount, staged.linkCount, len(staged.groups))
	}
	if info, err := staged.items.Stat(); err != nil || info.Size() == 0 {
		t.Fatalf("items were not staged: info=%v err=%v", info, err)
	}
	staged.Close()
	if _, err := os.Stat(itemsPath); !os.IsNotExist(err) {
		t.Fatalf("item staging file was not removed: %v", err)
	}
	if _, err := os.Stat(linksPath); !os.IsNotExist(err) {
		t.Fatalf("link staging file was not removed: %v", err)
	}
}

func TestStageJSONImportRejectsMissingLinkTarget(t *testing.T) {
	data := []byte(`{
		"exportVersion":4,"app":"ClipVault","exportedAt":"2026-08-11T00:00:00Z",
		"sourceEngine":"duckdb","warning":"test","items":[],"groups":[],
		"itemGroups":[{"itemId":99,"groupId":1}],
		"settings":{"sensitive":{"customPatterns":[]}}
	}`)
	if staged, err := stageJSONImport(bytes.NewReader(data)); err == nil {
		staged.Close()
		t.Fatal("missing link target was accepted")
	}
}
