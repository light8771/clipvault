package duckdb

import (
	"strings"
	"testing"
)

func TestSchemaV2UsesWideIdentifiersJSONAndTypedTimestamps(t *testing.T) {
	if SchemaVersion != "2" {
		t.Fatalf("SchemaVersion = %q, want 2", SchemaVersion)
	}
	schema := strings.Join(SchemaStatements, "\n")
	if SchemaIdentity == "" {
		t.Fatal("SchemaIdentity must identify the exact supported schema shape")
	}
	for _, fragment := range []string{"id BIGINT", "meta_json JSON", "value JSON", "created_at TIMESTAMPTZ", "sent_at TIMESTAMPTZ"} {
		if !strings.Contains(schema, fragment) {
			t.Fatalf("schema does not contain %q", fragment)
		}
	}
}
