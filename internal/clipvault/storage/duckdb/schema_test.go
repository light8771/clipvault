package duckdb

import (
	"strings"
	"testing"
)

func TestSchemaV7ExternalReadMCP(t *testing.T) {
	if SchemaVersion != "7" {
		t.Fatalf("SchemaVersion = %q, want 7", SchemaVersion)
	}
	if SchemaIdentity != "clipvault.duckdb.v7.external-read-mcp" {
		t.Fatalf("SchemaIdentity = %q, want clipvault.duckdb.v7.external-read-mcp", SchemaIdentity)
	}
	schema := strings.Join(SchemaStatements, "\n")
	for _, fragment := range []string{
		"id BIGINT", "meta_json JSON", "value JSON",
		"created_at BIGINT", "searched_at BIGINT",
		"content_available BOOLEAN", "is_duplicate BOOLEAN", "unavailable_reason VARCHAR",
		"config_change_log", "changed_at BIGINT", "changes JSON",
		"search_history", "filters JSON", "result_count BIGINT",
		"backup_history", "kind VARCHAR", "trigger VARCHAR", "mode VARCHAR",
		"ok BOOLEAN", "error VARCHAR", "target VARCHAR", "parts BIGINT", "items BIGINT",
		"api_tokens", "token_hash VARCHAR", "expires_at BIGINT", "revoked_at BIGINT",
		"channel VARCHAR NOT NULL DEFAULT 'http'", "allow_sensitive BOOLEAN NOT NULL DEFAULT false",
		"api_audit_log", "ts BIGINT", "operation VARCHAR", "status_code BIGINT", "duration_ms BIGINT",
		"resource_uri VARCHAR NOT NULL DEFAULT ''", "sensitive_accessed BOOLEAN NOT NULL DEFAULT false",
	} {
		if !strings.Contains(schema, fragment) {
			t.Fatalf("schema does not contain %q", fragment)
		}
	}
	if strings.Contains(schema, "TIMESTAMPTZ") {
		t.Fatal("schema must not use TIMESTAMPTZ columns")
	}
	if strings.Contains(schema, "email_backup_history") {
		t.Fatal("schema replaces email_backup_history with backup_history")
	}
	// api_tokens 完整列集：身份/哈希 + 生命周期 + 通道与敏感授权。
	for _, column := range []string{"id", "name", "token_hash", "created_at", "expires_at", "last_used_at", "revoked_at", "channel", "allow_sensitive"} {
		if !strings.Contains(schema, column) {
			t.Fatalf("api_tokens missing column %q", column)
		}
	}
}
