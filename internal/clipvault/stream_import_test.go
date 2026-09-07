package clipvault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStageJSONImportStreamsPayloadsToDisk(t *testing.T) {
	now := time.Now().UTC()
	text := "large-ish staged payload"
	contentSHA256 := hashString(text)
	export := ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    now.Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "test",
		Items: []StoredItem{{
			ID: 1, CreatedAt: now, RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT", Text: text,
			ByteLen: len([]byte(text)), CharLen: textRuneLen(text), ContentSHA256: &contentSHA256, MetaJSON: `{}`, SourceAttribution: "unknown",
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

func TestStageJSONImportRejectsMissingConsecutiveDuplicateField(t *testing.T) {
	now := time.Now().UTC()
	hash := hashString("body")
	export := ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    now.Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "test",
		Items: []StoredItem{{
			ID: 1, CreatedAt: now, RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT",
			Text: "body", ByteLen: 4, CharLen: 4, ContentSHA256: &hash, MetaJSON: "{}", SourceAttribution: "unknown",
		}},
		Groups:     []StoredGroup{},
		ItemGroups: []ItemGroupLink{},
		Settings:   ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{}}},
	}
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	delete(document["items"].([]any)[0].(map[string]any), "consecutiveDuplicate")
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := stageJSONImport(bytes.NewReader(data))
	if staged != nil {
		staged.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "requires consecutiveDuplicate") {
		t.Fatalf("missing consecutiveDuplicate error = %v", err)
	}
}

func TestStageJSONImportRejectsMissingLinkTarget(t *testing.T) {
	data := []byte(fmt.Sprintf(`{
		"exportVersion":%d,"app":"ClipVault","exportedAt":"2026-08-11T00:00:00Z",
		"sourceEngine":"duckdb","warning":"test","items":[],"groups":[],
		"assets":[],
		"itemGroups":[{"itemId":99,"groupId":1}],
		"settings":{"sensitive":{"customPatterns":[]}}
	}`, currentExportVersion))
	if staged, err := stageJSONImport(bytes.NewReader(data)); err == nil {
		staged.Close()
		t.Fatal("missing link target was accepted")
	}
}

func TestStageJSONImportRequiresCompleteNonNullCollections(t *testing.T) {
	base := fmt.Sprintf(`"exportVersion":%d,"app":"ClipVault","exportedAt":"2026-08-11T00:00:00Z",`, currentExportVersion) +
		`"sourceEngine":"duckdb","warning":"test","items":[],"assets":[],"groups":[],"itemGroups":[],` +
		`"settings":{"sensitive":{"customPatterns":[]}}`
	tests := map[string]string{
		"null items":      `{` + strings.Replace(base, `"items":[]`, `"items":null`, 1) + `}`,
		"null assets":     `{` + strings.Replace(base, `"assets":[]`, `"assets":null`, 1) + `}`,
		"null groups":     `{` + strings.Replace(base, `"groups":[]`, `"groups":null`, 1) + `}`,
		"null itemGroups": `{` + strings.Replace(base, `"itemGroups":[]`, `"itemGroups":null`, 1) + `}`,
		"missing groups":  `{` + strings.Replace(base, `"groups":[],`, ``, 1) + `}`,
		"missing assets":  `{` + strings.Replace(base, `"assets":[],`, ``, 1) + `}`,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			staged, err := stageJSONImport(strings.NewReader(payload))
			if staged != nil {
				staged.Close()
			}
			if err == nil {
				t.Fatalf("incomplete export was accepted: %s", payload)
			}
		})
	}
}

func TestStageJSONImportValidatesEnvelopeMetadata(t *testing.T) {
	base := fmt.Sprintf(`{"exportVersion":%d,"app":"ClipVault","exportedAt":"2026-08-11T00:00:00Z",`, currentExportVersion) +
		`"sourceEngine":"duckdb","warning":"test","items":[],"assets":[],"groups":[],"itemGroups":[],` +
		`"settings":{"sensitive":{"customPatterns":[]}}}`
	tests := map[string]string{
		"null timestamp":      strings.Replace(base, `"exportedAt":"2026-08-11T00:00:00Z"`, `"exportedAt":null`, 1),
		"malformed timestamp": strings.Replace(base, `"exportedAt":"2026-08-11T00:00:00Z"`, `"exportedAt":"yesterday"`, 1),
		"wrong source engine": strings.Replace(base, `"sourceEngine":"duckdb"`, `"sourceEngine":"sqlite"`, 1),
		"null source engine":  strings.Replace(base, `"sourceEngine":"duckdb"`, `"sourceEngine":null`, 1),
		"empty warning":       strings.Replace(base, `"warning":"test"`, `"warning":""`, 1),
		"null warning":        strings.Replace(base, `"warning":"test"`, `"warning":null`, 1),
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			staged, err := stageJSONImport(strings.NewReader(payload))
			if staged != nil {
				staged.Close()
			}
			if err == nil {
				t.Fatalf("invalid export envelope was accepted: %s", payload)
			}
		})
	}
}
