package duckdb

const SchemaVersion = "2"

// SchemaIdentity pins the exact supported schema shape. A database must match
// both the version and this identity; every other shape is rejected.
const SchemaIdentity = "clipvault.duckdb.v2.timestamptz-json"

var SchemaStatements = []string{
	`CREATE SEQUENCE seq_clipboard_items START 1`,
	`CREATE TABLE clipboard_items (
		id BIGINT PRIMARY KEY DEFAULT nextval('seq_clipboard_items'),
		created_at TIMESTAMPTZ NOT NULL,
		kind VARCHAR NOT NULL,
		format VARCHAR NOT NULL,
		text VARCHAR NOT NULL,
		byte_len BIGINT NOT NULL,
		char_len BIGINT NOT NULL,
		sha256 VARCHAR NOT NULL,
		meta_json JSON NOT NULL,
		starred BOOLEAN NOT NULL DEFAULT false,
		sensitive BOOLEAN NOT NULL DEFAULT false,
		source_exe_name VARCHAR NOT NULL DEFAULT '',
		source_exe_path VARCHAR NOT NULL DEFAULT '',
		source_window_title VARCHAR NOT NULL DEFAULT '',
		source_method VARCHAR NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX idx_clipboard_items_created_at ON clipboard_items(created_at DESC)`,
	`CREATE INDEX idx_clipboard_items_kind ON clipboard_items(kind)`,
	`CREATE INDEX idx_clipboard_items_sha256 ON clipboard_items(sha256)`,
	`CREATE INDEX idx_clipboard_items_sensitive ON clipboard_items(sensitive)`,
	`CREATE INDEX idx_clipboard_items_source_exe_name ON clipboard_items(source_exe_name)`,
	`CREATE TABLE settings (key VARCHAR PRIMARY KEY, value JSON NOT NULL)`,
	`CREATE SEQUENCE seq_groups START 1`,
	`CREATE TABLE groups (id BIGINT PRIMARY KEY DEFAULT nextval('seq_groups'), name VARCHAR NOT NULL, parent_id BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL)`,
	`CREATE INDEX idx_groups_parent ON groups(parent_id)`,
	`CREATE TABLE item_groups (item_id BIGINT NOT NULL, group_id BIGINT NOT NULL, PRIMARY KEY (item_id, group_id))`,
	`CREATE INDEX idx_item_groups_group ON item_groups(group_id)`,
	`CREATE SEQUENCE seq_email_backup_history START 1`,
	`CREATE TABLE email_backup_history (id BIGINT PRIMARY KEY DEFAULT nextval('seq_email_backup_history'), sent_at TIMESTAMPTZ NOT NULL, mode VARCHAR NOT NULL, from_address VARCHAR NOT NULL, to_address VARCHAR NOT NULL, format VARCHAR NOT NULL)`,
	`CREATE INDEX idx_email_backup_history_sent_at ON email_backup_history(sent_at DESC)`,
}
