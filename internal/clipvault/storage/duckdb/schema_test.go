package duckdb

import (
	"strings"
	"testing"
)

func TestSchemaV3UsesUnixMillisecondsAndEventStateColumns(t *testing.T) {
	if SchemaVersion != "3" {
		t.Fatalf("SchemaVersion = %q, want 3", SchemaVersion)
	}
	schema := strings.Join(SchemaStatements, "\n")
	if SchemaIdentity == "" {
		t.Fatal("SchemaIdentity must identify the exact supported schema shape")
	}
	for _, fragment := range []string{
		"id BIGINT", "meta_json JSON", "value JSON",
		"created_at BIGINT", "sent_at BIGINT",
		"content_available BOOLEAN", "is_duplicate BOOLEAN", "unavailable_reason VARCHAR",
	} {
		if !strings.Contains(schema, fragment) {
			t.Fatalf("schema does not contain %q", fragment)
		}
	}
	if strings.Contains(schema, "TIMESTAMPTZ") {
		t.Fatal("schema v3 must not use TIMESTAMPTZ columns")
	}
}
