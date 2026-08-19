package duckdb

import (
	"strings"
	"testing"
)

func TestSchemaV6UsesUnixMillisecondsAndDataAPITables(t *testing.T) {
	if SchemaVersion != "6" {
		t.Fatalf("SchemaVersion = %q, want 6", SchemaVersion)
	}
	schema := strings.Join(SchemaStatements, "\n")
	if SchemaIdentity == "" {
		t.Fatal("SchemaIdentity must identify the exact supported schema shape")
	}
	for _, fragment := range []string{
		"id BIGINT", "meta_json JSON", "value JSON",
		"created_at BIGINT", "searched_at BIGINT",
		"content_available BOOLEAN", "is_duplicate BOOLEAN", "unavailable_reason VARCHAR",
		"config_change_log", "changed_at BIGINT", "changes JSON",
		"search_history", "filters JSON", "result_count BIGINT",
		"backup_history", "kind VARCHAR", "trigger VARCHAR", "mode VARCHAR",
		"ok BOOLEAN", "error VARCHAR", "target VARCHAR", "parts BIGINT", "items BIGINT",
		"api_tokens", "token_hash VARCHAR", "expires_at BIGINT", "revoked_at BIGINT",
		"api_audit_log", "ts BIGINT", "operation VARCHAR", "status_code BIGINT", "duration_ms BIGINT",
	} {
		if !strings.Contains(schema, fragment) {
			t.Fatalf("schema does not contain %q", fragment)
		}
	}
	if strings.Contains(schema, "TIMESTAMPTZ") {
		t.Fatal("schema v5 must not use TIMESTAMPTZ columns")
	}
	if strings.Contains(schema, "email_backup_history") {
		t.Fatal("schema v5 replaces email_backup_history with backup_history")
	}
}
