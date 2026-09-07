package duckdb

import (
	"strings"
	"testing"
)

func TestSchemaV10CaptureEventsAndContentDuplicates(t *testing.T) {
	if SchemaVersion != "11" {
		t.Fatalf("SchemaVersion = %q, want 11", SchemaVersion)
	}
	if SchemaIdentity != "clipvault.duckdb.v11.stable-event-fingerprints" {
		t.Fatalf("SchemaIdentity = %q, want clipvault.duckdb.v11.stable-event-fingerprints", SchemaIdentity)
	}
	schema := strings.Join(SchemaStatements, "\n")
	for _, fragment := range []string{
		"id BIGINT", "meta_json JSON", "value JSON",
		"created_at BIGINT", "searched_at BIGINT",
		"record_type VARCHAR", "content_sha256 VARCHAR", "consecutive_duplicate BOOLEAN", "event_code VARCHAR", "event_fingerprint VARCHAR",
		"capture_sequence UBIGINT", "capture_stage VARCHAR", "capture_attempts BIGINT", "event_formats JSON",
		"json_type(event_formats) = 'ARRAY'", "CAST(event_formats AS VARCHAR) = '[]'",
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
	// i18n 三表：keys（语言 Schema）/ languages（语言定义）/ translations（译文）。
	for _, fragment := range []string{
		"i18n_keys", "translation_key VARCHAR", "module VARCHAR", "source_version INTEGER", "is_active BOOLEAN",
		"i18n_languages", "base_language_code VARCHAR", "is_builtin BOOLEAN", "is_enabled BOOLEAN",
		"i18n_translations", "language_code VARCHAR", "translation_value VARCHAR",
		"idx_i18n_keys_key", "idx_i18n_languages_code", "idx_i18n_translations_lang_key",
	} {
		if !strings.Contains(schema, fragment) {
			t.Fatalf("schema does not contain %q", fragment)
		}
	}
}
