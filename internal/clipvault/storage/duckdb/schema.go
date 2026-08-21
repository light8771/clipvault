package duckdb

const SchemaVersion = "8"

// SchemaIdentity pins the exact supported schema shape. A database must match
// both the version and this identity; every other shape is rejected.
//
// v7（外部只读 + MCP）：api_tokens 带 channel（http|mcp 通道隔离）与
// allow_sensitive（敏感数据按 Token 显式授权，默认 false）；api_audit_log
// 带 channel、resource_uri、sensitive_accessed 与请求摘要/请求体审计列。
//
// v8（多语言 i18n）：新增 i18n_keys（官方语言 Schema）、i18n_languages
// （官方与用户自定义语言）、i18n_translations（各语言对每个 Key 的译文）。
const SchemaIdentity = "clipvault.duckdb.v8.i18n"

var SchemaStatements = []string{
	`CREATE SEQUENCE seq_clipboard_items START 1`,
	`CREATE TABLE clipboard_items (
		id BIGINT PRIMARY KEY DEFAULT nextval('seq_clipboard_items'),
		created_at BIGINT NOT NULL,
		kind VARCHAR NOT NULL,
		format VARCHAR NOT NULL,
		text VARCHAR NOT NULL,
		byte_len BIGINT NOT NULL,
		char_len BIGINT NOT NULL,
		sha256 VARCHAR NOT NULL,
		meta_json JSON NOT NULL,
		starred BOOLEAN NOT NULL DEFAULT false,
		sensitive BOOLEAN NOT NULL DEFAULT false,
		content_available BOOLEAN NOT NULL DEFAULT true,
		is_duplicate BOOLEAN NOT NULL DEFAULT false,
		unavailable_reason VARCHAR NOT NULL DEFAULT '',
		source_exe_name VARCHAR NOT NULL DEFAULT '',
		source_exe_path VARCHAR NOT NULL DEFAULT '',
		source_window_title VARCHAR NOT NULL DEFAULT '',
		source_method VARCHAR NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX idx_clipboard_items_created_at ON clipboard_items(created_at DESC)`,
	`CREATE INDEX idx_clipboard_items_kind ON clipboard_items(kind)`,
	`CREATE INDEX idx_clipboard_items_sha256 ON clipboard_items(sha256)`,
	`CREATE INDEX idx_clipboard_items_sensitive ON clipboard_items(sensitive)`,
	`CREATE INDEX idx_clipboard_items_is_duplicate ON clipboard_items(is_duplicate)`,
	`CREATE INDEX idx_clipboard_items_content_available ON clipboard_items(content_available)`,
	`CREATE INDEX idx_clipboard_items_source_exe_name ON clipboard_items(source_exe_name)`,
	`CREATE TABLE settings (key VARCHAR PRIMARY KEY, value JSON NOT NULL)`,
	`CREATE SEQUENCE seq_groups START 1`,
	`CREATE TABLE groups (id BIGINT PRIMARY KEY DEFAULT nextval('seq_groups'), name VARCHAR NOT NULL, parent_id BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL)`,
	`CREATE INDEX idx_groups_parent ON groups(parent_id)`,
	`CREATE TABLE item_groups (item_id BIGINT NOT NULL, group_id BIGINT NOT NULL, PRIMARY KEY (item_id, group_id))`,
	`CREATE INDEX idx_item_groups_group ON item_groups(group_id)`,
	`CREATE SEQUENCE seq_backup_history START 1`,
	`CREATE TABLE backup_history (id BIGINT PRIMARY KEY DEFAULT nextval('seq_backup_history'), created_at BIGINT NOT NULL, kind VARCHAR NOT NULL, trigger VARCHAR NOT NULL, mode VARCHAR NOT NULL, ok BOOLEAN NOT NULL, error VARCHAR NOT NULL DEFAULT '', target VARCHAR NOT NULL DEFAULT '', format VARCHAR NOT NULL DEFAULT '', parts BIGINT NOT NULL DEFAULT 0, items BIGINT NOT NULL DEFAULT 0)`,
	`CREATE INDEX idx_backup_history_created_at ON backup_history(created_at DESC)`,
	`CREATE INDEX idx_backup_history_kind ON backup_history(kind, created_at DESC)`,
	`CREATE SEQUENCE seq_config_change_log START 1`,
	`CREATE TABLE config_change_log (id BIGINT PRIMARY KEY DEFAULT nextval('seq_config_change_log'), changed_at BIGINT NOT NULL, changes JSON NOT NULL)`,
	`CREATE INDEX idx_config_change_log_changed_at ON config_change_log(changed_at DESC)`,
	`CREATE SEQUENCE seq_search_history START 1`,
	`CREATE TABLE search_history (id BIGINT PRIMARY KEY DEFAULT nextval('seq_search_history'), searched_at BIGINT NOT NULL, query VARCHAR NOT NULL, filters JSON NOT NULL, result_count BIGINT NOT NULL)`,
	`CREATE INDEX idx_search_history_searched_at ON search_history(searched_at DESC)`,
	`CREATE SEQUENCE seq_api_tokens START 1`,
	// expires_at/last_used_at/revoked_at 为 Unix 毫秒，0 表示"无"（永不过期/未使用/未撤销）。
	// channel 限定 http|mcp：HTTP 数据接口与 MCP 服务各自只认本通道签发的 Token；
	// allow_sensitive 默认 false：敏感数据访问必须按 Token 显式授权。
	`CREATE TABLE api_tokens (id BIGINT PRIMARY KEY DEFAULT nextval('seq_api_tokens'), name VARCHAR NOT NULL, token_hash VARCHAR NOT NULL, created_at BIGINT NOT NULL, expires_at BIGINT NOT NULL DEFAULT 0, last_used_at BIGINT NOT NULL DEFAULT 0, revoked_at BIGINT NOT NULL DEFAULT 0, channel VARCHAR NOT NULL DEFAULT 'http', allow_sensitive BOOLEAN NOT NULL DEFAULT false)`,
	`CREATE UNIQUE INDEX idx_api_tokens_hash ON api_tokens(token_hash)`,
	`CREATE SEQUENCE seq_api_audit_log START 1`,
	// token_id/item_id 为 0 表示无关联（如认证失败）；审计不记录剪贴板正文与 Token 原文。
	// request_summary 记录查询条件摘要；request_body 记录调用方提交的原始请求体
	// （有上限截断），供本机用户审计"谁查了什么"。
	// channel 区分 http/mcp；resource_uri 记录 MCP 资源 URI；
	// sensitive_accessed 仅在实际返回敏感行/内容时为 true。
	`CREATE TABLE api_audit_log (id BIGINT PRIMARY KEY DEFAULT nextval('seq_api_audit_log'), ts BIGINT NOT NULL, token_id BIGINT NOT NULL DEFAULT 0, token_name VARCHAR NOT NULL DEFAULT '', remote_ip VARCHAR NOT NULL DEFAULT '', method VARCHAR NOT NULL DEFAULT '', endpoint VARCHAR NOT NULL DEFAULT '', operation VARCHAR NOT NULL DEFAULT '', item_id BIGINT NOT NULL DEFAULT 0, item_count BIGINT NOT NULL DEFAULT 0, duration_ms BIGINT NOT NULL DEFAULT 0, status_code BIGINT NOT NULL DEFAULT 0, error_code VARCHAR NOT NULL DEFAULT '', request_summary VARCHAR NOT NULL DEFAULT '', request_body VARCHAR NOT NULL DEFAULT '', channel VARCHAR NOT NULL DEFAULT 'http', resource_uri VARCHAR NOT NULL DEFAULT '', sensitive_accessed BOOLEAN NOT NULL DEFAULT false)`,
	`CREATE INDEX idx_api_audit_log_ts ON api_audit_log(ts DESC)`,
	// ── i18n 多语言 ────────────────────────────────────────────────────────────
	// i18n_keys 是官方语言 Schema：模块分类、完成度统计、source_version 版本化
	// （"最近新增"筛选）、is_active 退役（不物理删除，保留历史译文与回滚余地）。
	`CREATE SEQUENCE seq_i18n_keys START 1`,
	`CREATE TABLE i18n_keys (
		id BIGINT PRIMARY KEY DEFAULT nextval('seq_i18n_keys'),
		translation_key VARCHAR NOT NULL,
		module VARCHAR NOT NULL,
		description VARCHAR NOT NULL DEFAULT '',
		source_version INTEGER NOT NULL DEFAULT 1,
		is_active BOOLEAN NOT NULL DEFAULT true,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE UNIQUE INDEX idx_i18n_keys_key ON i18n_keys(translation_key)`,
	`CREATE INDEX idx_i18n_keys_module ON i18n_keys(module)`,
	// i18n_languages 定义系统有哪些语言：官方（is_builtin，只读保护在 Service
	// 层与 Store 层双重强制）与用户自定义；base_language_code 限定官方两种
	// （Service 层校验），fallback 环在构造上不可能出现。
	`CREATE SEQUENCE seq_i18n_languages START 1`,
	`CREATE TABLE i18n_languages (
		id BIGINT PRIMARY KEY DEFAULT nextval('seq_i18n_languages'),
		code VARCHAR NOT NULL,
		name VARCHAR NOT NULL,
		native_name VARCHAR NOT NULL DEFAULT '',
		base_language_code VARCHAR NOT NULL DEFAULT 'en-US',
		is_builtin BOOLEAN NOT NULL DEFAULT false,
		is_enabled BOOLEAN NOT NULL DEFAULT true,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE UNIQUE INDEX idx_i18n_languages_code ON i18n_languages(code)`,
	// i18n_translations 存某语言对某 Key 的译文。空翻译语义 = 删除该行
	// （运行时自然进入 fallback），因此 translation_value 恒非空。
	`CREATE SEQUENCE seq_i18n_translations START 1`,
	`CREATE TABLE i18n_translations (
		id BIGINT PRIMARY KEY DEFAULT nextval('seq_i18n_translations'),
		language_code VARCHAR NOT NULL,
		translation_key VARCHAR NOT NULL,
		translation_value VARCHAR NOT NULL,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	`CREATE UNIQUE INDEX idx_i18n_translations_lang_key ON i18n_translations(language_code, translation_key)`,
}
