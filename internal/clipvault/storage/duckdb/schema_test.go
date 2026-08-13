package duckdb

import (
	"strings"
	"testing"
)

func TestSchemaV2UsesWideIdentifiersAndJSON(t *testing.T) {
	if SchemaVersion != "2" {
		t.Fatalf("SchemaVersion = %q, want 2", SchemaVersion)
	}
	schema := strings.Join(SchemaStatements, "\n")
	for _, fragment := range []string{"id BIGINT", "meta_json JSON", "value JSON"} {
		if !strings.Contains(schema, fragment) {
			t.Fatalf("schema does not contain %q", fragment)
		}
	}
}
