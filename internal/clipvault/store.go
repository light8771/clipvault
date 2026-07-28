package clipvault

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

type Store struct {
	db    *sql.DB
	dbKey []byte
}

var errItemOrGroupNotFound = errors.New("item or group not found")

const emailBackupHistoryTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

type storeQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type ClipboardItem struct {
	Kind      string
	Format    string
	Text      string
	ByteLen   int
	CharLen   int
	SHA256    string
	MetaJSON  string
	Sensitive bool
}

// Group 表示一个用户自定义分组，支持嵌套（parent_id 为 0 表示顶级分组）。
type Group struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parentId"` // 0 = 顶级分组
}

// StoredGroup 是从数据库读取的分组记录。
type StoredGroup struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	ParentID  int64     `json:"parentId"`
	CreatedAt time.Time `json:"createdAt"`
}

// EmailBackupHistory is an immutable record of a successfully sent backup email.
type EmailBackupHistory struct {
	SentAt time.Time `json:"sentAt"`
	Mode   string    `json:"mode"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Format string    `json:"format"`
}

type StoredItem struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Kind      string    `json:"kind"`
	Format    string    `json:"format"`
	Text      string    `json:"text"`
	ByteLen   int       `json:"byteLen"`
	CharLen   int       `json:"charLen"`
	SHA256    string    `json:"sha256"`
	MetaJSON  string    `json:"metaJson"`
	Starred   bool      `json:"starred"`
	Sensitive bool      `json:"sensitive"`
}

// SearchOptions holds parameters for Store.Search.
type SearchOptions struct {
	Query            string
	Limit            int
	Offset           int // pagination offset
	StarredOnly      bool
	Kind             string // filter by kind (empty = all)
	DateFrom         string // YYYY-MM-DD, inclusive lower bound on created_at (UTC date)
	DateTo           string // YYYY-MM-DD, inclusive upper bound on created_at (UTC date)
	SensitiveOnly    bool   // filter to sensitive items only
	IncludeSensitive bool   // include sensitive items in results (default false)
	GroupID          int64  // filter by group id (0 = no group filter)
	SkipTotal        bool   // skip total count when the caller only needs the current page
}

const maxSearchTerms = 8

// StoreOptions holds parameters for opening an encrypted DuckDB store.
type StoreOptions struct {
	DataDir  string
	Portable bool
}

// OpenStore opens or creates an encrypted DuckDB store.
// On first run (no key file + no db file), it generates a new db_key,
// protects it with DPAPI, writes the key file, and creates the database.
// On subsequent runs, it reads the key file, DPAPI-decrypts the key, and opens the db.
func OpenStore(opts StoreOptions) (*Store, error) {
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}

	keyPath := filepath.Join(opts.DataDir, "clipvault.key")
	dbPath := filepath.Join(opts.DataDir, "clipvault.duckdb")

	var dbKey []byte

	_, keyErr := os.Stat(keyPath)
	if keyErr == nil {
		// Key file exists — DPAPI decrypt
		key, _, err := ReadDPAPIKeyFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read DPAPI key file: %w", err)
		}
		dbKey = key
	} else if os.IsNotExist(keyErr) {
		// First run — generate new key
		dbKey = make([]byte, 32)
		if _, err := rand.Read(dbKey); err != nil {
			return nil, fmt.Errorf("generate db key: %w", err)
		}
		if err := WriteDPAPIKeyFile(keyPath, dbKey); err != nil {
			return nil, fmt.Errorf("write DPAPI key file: %w", err)
		}
	} else {
		return nil, fmt.Errorf("stat key file: %w", keyErr)
	}

	keyHex := hex.EncodeToString(dbKey)

	// Open DuckDB :memory: + ATTACH encrypted file
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("sql.Open(duckdb): %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	escapedPath := escapeSQLPath(dbPath)
	attachSQL := fmt.Sprintf("ATTACH '%s' AS vault (ENCRYPTION_KEY '%s')", escapedPath, keyHex)
	if _, err := db.Exec(attachSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ATTACH with ENCRYPTION_KEY: %w", err)
	}
	if _, err := db.Exec("USE vault"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("USE vault: %w", err)
	}

	store := &Store{db: db, dbKey: dbKey}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

// DBKey returns the raw database encryption key (for recovery file creation).
func (s *Store) DBKey() []byte { return s.dbKey }

// escapeSQLPath escapes single quotes in a file path for safe embedding in SQL.
func escapeSQLPath(path string) string {
	return strings.ReplaceAll(path, "'", "''")
}

// verifyDuckDBKeyInternal opens a DuckDB file with the given key to verify correctness.
func verifyDuckDBKeyInternal(dbPath, keyHex string) error {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return fmt.Errorf("sql.Open(duckdb): %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	escapedPath := escapeSQLPath(dbPath)
	attachSQL := fmt.Sprintf("ATTACH '%s' AS vault (ENCRYPTION_KEY '%s')", escapedPath, keyHex)
	if _, err := db.Exec(attachSQL); err != nil {
		return fmt.Errorf("ATTACH failed (wrong key?): %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	// schema_migrations table for version tracking
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Check current version
	var currentVersion int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}

	if currentVersion >= 1 {
		return nil // already migrated
	}

	stmts := []string{
		`CREATE SEQUENCE IF NOT EXISTS seq_clipboard_items START 1`,
		`CREATE TABLE IF NOT EXISTS clipboard_items (
			id INTEGER PRIMARY KEY DEFAULT nextval('seq_clipboard_items'),
			created_at TEXT NOT NULL,
			kind TEXT NOT NULL,
			format TEXT NOT NULL,
			text TEXT NOT NULL,
			byte_len INTEGER NOT NULL,
			char_len INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			meta_json TEXT NOT NULL,
			starred BOOLEAN NOT NULL DEFAULT false,
			sensitive BOOLEAN NOT NULL DEFAULT false
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_created_at ON clipboard_items(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_kind ON clipboard_items(kind)`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_sha256 ON clipboard_items(sha256)`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_sensitive ON clipboard_items(sensitive)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE SEQUENCE IF NOT EXISTS seq_groups START 1`,
		`CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY DEFAULT nextval('seq_groups'),
			name TEXT NOT NULL,
			parent_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_groups_parent ON groups(parent_id)`,
		`CREATE TABLE IF NOT EXISTS item_groups (
			item_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL,
			PRIMARY KEY (item_id, group_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_item_groups_group ON item_groups(group_id)`,
		`CREATE SEQUENCE IF NOT EXISTS seq_email_backup_history START 1`,
		`CREATE TABLE IF NOT EXISTS email_backup_history (
			id INTEGER PRIMARY KEY DEFAULT nextval('seq_email_backup_history'),
			sent_at TEXT NOT NULL,
			mode TEXT NOT NULL,
			from_address TEXT NOT NULL,
			to_address TEXT NOT NULL,
			format TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_email_backup_history_sent_at ON email_backup_history(sent_at DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate stmt: %w\nSQL: %s", err, stmt)
		}
	}

	// Record migration version
	if _, err := s.db.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (1, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return nil
}

func (s *Store) Save(ctx context.Context, item ClipboardItem) error {
	if item.MetaJSON == "" {
		item.MetaJSON = "{}"
	} else if !json.Valid([]byte(item.MetaJSON)) {
		item.MetaJSON = "{}"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO clipboard_items
		(created_at, kind, format, text, byte_len, char_len, sha256, meta_json, sensitive)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano),
		item.Kind,
		item.Format,
		item.Text,
		item.ByteLen,
		item.CharLen,
		item.SHA256,
		item.MetaJSON,
		item.Sensitive,
	)
	return err
}

// AddEmailBackupHistory records a successful backup email. History is append-only.
func (s *Store) AddEmailBackupHistory(ctx context.Context, record EmailBackupHistory) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO email_backup_history
		(sent_at, mode, from_address, to_address, format) VALUES (?, ?, ?, ?, ?)`,
		record.SentAt.UTC().Format(emailBackupHistoryTimeLayout), record.Mode, record.From, record.To, record.Format)
	return err
}

// ListEmailBackupHistory returns the most recent successful backup emails.
func (s *Store) ListEmailBackupHistory(ctx context.Context, limit int) ([]EmailBackupHistory, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sent_at, mode, from_address, to_address, format
		FROM email_backup_history ORDER BY sent_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]EmailBackupHistory, 0)
	for rows.Next() {
		var record EmailBackupHistory
		var sentAt string
		if err := rows.Scan(&sentAt, &record.Mode, &record.From, &record.To, &record.Format); err != nil {
			return nil, err
		}
		var err error
		record.SentAt, err = time.Parse(emailBackupHistoryTimeLayout, sentAt)
		if err != nil {
			return nil, fmt.Errorf("parse email backup history timestamp: %w", err)
		}
		history = append(history, record)
	}
	return history, rows.Err()
}

func (s *Store) Search(ctx context.Context, opts SearchOptions) ([]StoredItem, int, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Limit > 500 {
		opts.Limit = 500
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}

	// Build dynamic WHERE clause
	var conds []string
	var args []any

	for _, term := range splitSearchTerms(opts.Query) {
		like := "%" + escapeLike(term) + "%"
		// Each term may match the visible text, type, native format, metadata,
		// or an assigned group. Terms are ANDed together so a broad query remains
		// useful instead of returning every record that contains any one word.
		conds = append(conds, `(text ILIKE ? ESCAPE '\' OR kind ILIKE ? ESCAPE '\' OR format ILIKE ? ESCAPE '\' OR sha256 ILIKE ? ESCAPE '\' OR meta_json ILIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM item_groups ig JOIN groups g ON g.id = ig.group_id WHERE ig.item_id = clipboard_items.id AND g.name ILIKE ? ESCAPE '\'))`)
		args = append(args, like, like, like, like, like, like)
	}
	if opts.StarredOnly {
		conds = append(conds, "starred = true")
	}
	if opts.Kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, opts.Kind)
	}
	if opts.DateFrom != "" {
		conds = append(conds, "substr(created_at,1,10) >= ?")
		args = append(args, opts.DateFrom)
	}
	if opts.DateTo != "" {
		conds = append(conds, "substr(created_at,1,10) <= ?")
		args = append(args, opts.DateTo)
	}
	if opts.SensitiveOnly {
		conds = append(conds, "sensitive = true")
	} else if !opts.IncludeSensitive {
		conds = append(conds, "sensitive = false")
	}
	if opts.GroupID > 0 {
		conds = append(conds, "id IN (SELECT item_id FROM item_groups WHERE group_id = ?)")
		args = append(args, opts.GroupID)
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if !opts.SkipTotal {
		// Web pagination needs this value, but a type-ahead consumer does not.
		// Skipping it avoids a second full scan for wildcard searches.
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clipboard_items"+where, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}

	// Fetch page
	fetchArgs := append(append([]any{}, args...), opts.Limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive "+
			"FROM clipboard_items"+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		fetchArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []StoredItem
	for rows.Next() {
		var item StoredItem
		var created string
		if err := rows.Scan(&item.ID, &created, &item.Kind, &item.Format, &item.Text,
			&item.ByteLen, &item.CharLen, &item.SHA256, &item.MetaJSON, &item.Starred, &item.Sensitive); err != nil {
			return nil, 0, err
		}
		if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
			item.CreatedAt = parsed
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func splitSearchTerms(query string) []string {
	terms := strings.Fields(query)
	if len(terms) > maxSearchTerms {
		return terms[:maxSearchTerms]
	}
	return terms
}

// GetByID 返回指定 ID 的单条记录。
func (s *Store) GetByID(ctx context.Context, id int64) (StoredItem, error) {
	var item StoredItem
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive
		FROM clipboard_items WHERE id = ?`, id).Scan(
		&item.ID, &created, &item.Kind, &item.Format, &item.Text,
		&item.ByteLen, &item.CharLen, &item.SHA256, &item.MetaJSON, &item.Starred, &item.Sensitive)
	if err != nil {
		return item, err
	}
	if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
		item.CreatedAt = parsed
	}
	return item, nil
}

// GetPositionByID 返回指定 ID 在全量列表（按 id DESC）中的 0-based 偏移量。
// 用于搜索结果定位到时间线中的页码位置。
func (s *Store) GetPositionByID(ctx context.Context, id int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM clipboard_items WHERE id > ?`, id).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) Star(ctx context.Context, id int64, starred bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE clipboard_items SET starred = ? WHERE id = ?`, starred, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("item %d not found", id)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM item_groups WHERE item_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM clipboard_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("item %d not found", id)
	}
	return tx.Commit()
}

func (s *Store) ClearAll(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_groups`); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM clipboard_items`)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// GetConfig 从 settings 表读取配置，不存在时返回默认配置。
func (s *Store) GetConfig() (Config, error) {
	return getConfig(context.Background(), s.db)
}

func getConfig(ctx context.Context, queryer storeQueryer) (Config, error) {
	cfg := DefaultConfig()
	var jsonStr string
	err := queryer.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'config'`).Scan(&jsonStr)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return DefaultConfig(), nil
	}
	normalizeConfig(&cfg)
	return cfg, nil
}

// SaveConfig 将配置持久化到 settings 表。
func (s *Store) SaveConfig(cfg Config) error {
	normalizeConfig(&cfg)
	if err := validateSensitiveRules(cfg.Sensitive); err != nil {
		return fmt.Errorf("invalid sensitive rule: %w", err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO settings (key, value) VALUES ('config', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		string(data),
	)
	return err
}

// ExportAll 返回所有剪切板记录（不限数量），按 id 正序排列，用于数据导出。
func (s *Store) ExportAll(ctx context.Context) ([]StoredItem, error) {
	return exportAll(ctx, s.db)
}

func exportAll(ctx context.Context, queryer storeQueryer) ([]StoredItem, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id, created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive
		FROM clipboard_items ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []StoredItem
	for rows.Next() {
		var item StoredItem
		var created string
		if err := rows.Scan(&item.ID, &created, &item.Kind, &item.Format, &item.Text, &item.ByteLen, &item.CharLen, &item.SHA256, &item.MetaJSON, &item.Starred, &item.Sensitive); err != nil {
			return nil, err
		}
		if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
			item.CreatedAt = parsed
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ExportData is the complete portable export structure.
type ExportData struct {
	ExportVersion int             `json:"exportVersion"`
	App           string          `json:"app"`
	ExportedAt    string          `json:"exportedAt"`
	SourceEngine  string          `json:"sourceEngine"`
	Warning       string          `json:"warning"`
	Items         []StoredItem    `json:"items"`
	Groups        []StoredGroup   `json:"groups"`
	ItemGroups    []ItemGroupLink `json:"itemGroups"`
	Settings      ExportSettings  `json:"settings"`
}

// ExportSettings contains the portable settings required to restore the
// exported data's behavior. Credentials and machine-local settings stay out
// of data exports.
type ExportSettings struct {
	Sensitive SensitiveConfig `json:"sensitive"`
}

// UnmarshalJSON requires every portable settings field in the v3 contract.
// A missing sensitive rule set must not silently clear the destination rules.
func (settings *ExportSettings) UnmarshalJSON(data []byte) error {
	type exportSettings ExportSettings
	var decoded exportSettings
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	rawSensitive, ok := fields["sensitive"]
	if !ok || string(rawSensitive) == "null" {
		return errors.New("settings.sensitive is required")
	}
	var sensitiveFields map[string]json.RawMessage
	if err := json.Unmarshal(rawSensitive, &sensitiveFields); err != nil {
		return fmt.Errorf("settings.sensitive: %w", err)
	}
	rawPatterns, ok := sensitiveFields["customPatterns"]
	if !ok || string(rawPatterns) == "null" {
		return errors.New("settings.sensitive.customPatterns is required")
	}
	*settings = ExportSettings(decoded)
	return nil
}

// ItemGroupLink is a single item-group association for export/import.
type ItemGroupLink struct {
	ItemID  int64 `json:"itemId"`
	GroupID int64 `json:"groupId"`
}

// ExportFullForJSON exports clipboard data, group hierarchy, and portable
// sensitive-data rules.
func (s *Store) ExportFullForJSON(ctx context.Context) (*ExportData, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin export transaction: %w", err)
	}
	defer tx.Rollback()

	items, err := exportAll(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("export items: %w", err)
	}

	groups, err := listGroups(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("export groups: %w", err)
	}

	// Export item_groups
	rows, err := tx.QueryContext(ctx, `SELECT item_id, group_id FROM item_groups ORDER BY item_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("export item_groups: %w", err)
	}
	var links []ItemGroupLink
	for rows.Next() {
		var l ItemGroupLink
		if err := rows.Scan(&l.ItemID, &l.GroupID); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close item_groups export: %w", err)
	}
	cfg, err := getConfig(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("export settings: %w", err)
	}
	if items == nil {
		items = []StoredItem{}
	}
	if groups == nil {
		groups = []StoredGroup{}
	}
	if links == nil {
		links = []ItemGroupLink{}
	}

	export := &ExportData{
		ExportVersion: 3,
		App:           "ClipVault",
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "This export contains plaintext clipboard content.",
		Items:         items,
		Groups:        groups,
		ItemGroups:    links,
		Settings: ExportSettings{Sensitive: SensitiveConfig{
			CustomPatterns: append([]string{}, cfg.Sensitive.CustomPatterns...),
		}},
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit export transaction: %w", err)
	}
	return export, nil
}

// ImportFullJSON imports clipboard data, group hierarchy, and portable
// sensitive-data rules in one transaction.
func (s *Store) ImportFullJSON(ctx context.Context, data []byte) error {
	var envelope struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("parse export JSON: %w", err)
	}
	if len(envelope.Settings) == 0 || string(envelope.Settings) == "null" {
		return errors.New("settings is required")
	}
	var export ExportData
	if err := json.Unmarshal(data, &export); err != nil {
		return fmt.Errorf("parse export JSON: %w", err)
	}
	if export.App != "ClipVault" {
		return fmt.Errorf("not a ClipVault export (app=%q)", export.App)
	}
	if export.ExportVersion != 3 {
		return fmt.Errorf("unsupported export version %d", export.ExportVersion)
	}
	return s.importExportData(ctx, export)
}

// importExportData imports a validated complete export in one transaction.
// JSON and XLSX both use this path so their restore behavior cannot diverge.
func (s *Store) importExportData(ctx context.Context, export ExportData) error {
	if err := validateExportData(export); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	cfg, err := getConfig(ctx, tx)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	cfg.Sensitive = export.Settings.Sensitive
	normalizeConfig(&cfg)
	settings, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}

	// Import groups with ID remapping. Parent groups are inserted first even
	// when a workbook happens to list children before parents.
	groupIDMap := make(map[int64]int64) // oldID -> newID
	pendingGroups := append([]StoredGroup(nil), export.Groups...)
	for len(pendingGroups) > 0 {
		next := pendingGroups[:0]
		progressed := false
		for _, g := range pendingGroups {
			parentID, ready := groupIDMap[g.ParentID]
			if g.ParentID == 0 {
				ready = true
			}
			if !ready {
				next = append(next, g)
				continue
			}
			var newID int64
			err := tx.QueryRowContext(ctx,
				`INSERT INTO groups (name, parent_id, created_at) VALUES (?, ?, ?) RETURNING id`,
				g.Name, parentID, g.CreatedAt.UTC().Format(time.RFC3339Nano),
			).Scan(&newID)
			if err != nil {
				return fmt.Errorf("import group %d: %w", g.ID, err)
			}
			groupIDMap[g.ID] = newID
			progressed = true
		}
		if !progressed {
			return errors.New("import groups: cyclic parent hierarchy")
		}
		pendingGroups = next
	}

	// Import items with ID remapping
	itemIDMap := make(map[int64]int64) // oldID -> newID
	for _, item := range export.Items {
		var newID int64
		err := tx.QueryRowContext(ctx,
			`INSERT INTO clipboard_items
			(created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			item.CreatedAt.UTC().Format(time.RFC3339Nano),
			item.Kind, item.Format, item.Text,
			item.ByteLen, item.CharLen, item.SHA256, item.MetaJSON,
			item.Starred, item.Sensitive,
		).Scan(&newID)
		if err != nil {
			return fmt.Errorf("import item %d: %w", item.ID, err)
		}
		itemIDMap[item.ID] = newID
	}

	// Import item_groups with remapped IDs
	for _, link := range export.ItemGroups {
		newItemID := itemIDMap[link.ItemID]
		newGroupID := groupIDMap[link.GroupID]
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_groups (item_id, group_id) VALUES (?, ?)`,
			newItemID, newGroupID,
		); err != nil {
			return fmt.Errorf("import item_group: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES ('config', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(settings)); err != nil {
		return fmt.Errorf("import settings: %w", err)
	}

	return tx.Commit()
}

func validateExportData(export ExportData) error {
	if err := validateSensitiveRules(export.Settings.Sensitive); err != nil {
		return fmt.Errorf("invalid sensitive rule: %w", err)
	}
	itemIDs := make(map[int64]struct{}, len(export.Items))
	for _, item := range export.Items {
		if item.ID <= 0 || item.CreatedAt.IsZero() || item.ByteLen < 0 || item.CharLen < 0 || !json.Valid([]byte(item.MetaJSON)) {
			return fmt.Errorf("invalid item %d", item.ID)
		}
		if _, exists := itemIDs[item.ID]; exists {
			return fmt.Errorf("duplicate item ID %d", item.ID)
		}
		itemIDs[item.ID] = struct{}{}
	}
	groupIDs := make(map[int64]struct{}, len(export.Groups))
	for _, group := range export.Groups {
		if group.ID <= 0 || group.ParentID < 0 || group.CreatedAt.IsZero() {
			return fmt.Errorf("invalid group %d", group.ID)
		}
		if _, exists := groupIDs[group.ID]; exists {
			return fmt.Errorf("duplicate group ID %d", group.ID)
		}
		groupIDs[group.ID] = struct{}{}
	}
	for _, group := range export.Groups {
		if group.ParentID != 0 {
			if _, exists := groupIDs[group.ParentID]; !exists {
				return fmt.Errorf("group %d references missing parent %d", group.ID, group.ParentID)
			}
		}
	}
	links := make(map[ItemGroupLink]struct{}, len(export.ItemGroups))
	for _, link := range export.ItemGroups {
		if _, exists := itemIDs[link.ItemID]; !exists {
			return fmt.Errorf("item-group link references missing item %d", link.ItemID)
		}
		if _, exists := groupIDs[link.GroupID]; !exists {
			return fmt.Errorf("item-group link references missing group %d", link.GroupID)
		}
		if _, exists := links[link]; exists {
			return fmt.Errorf("duplicate item-group link %d:%d", link.ItemID, link.GroupID)
		}
		links[link] = struct{}{}
	}
	return nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// ── Groups CRUD ───────────────────────────────────────────────

// ListGroups 返回所有分组，按 id 排序。
func (s *Store) ListGroups(ctx context.Context) ([]StoredGroup, error) {
	return listGroups(ctx, s.db)
}

func listGroups(ctx context.Context, queryer storeQueryer) ([]StoredGroup, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id, name, parent_id, created_at FROM groups ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []StoredGroup
	for rows.Next() {
		var g StoredGroup
		var createdAt string
		if err := rows.Scan(&g.ID, &g.Name, &g.ParentID, &createdAt); err != nil {
			return nil, err
		}
		g.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse group %d created_at: %w", g.ID, err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// CreateGroup 创建一个新分组。
func (s *Store) CreateGroup(ctx context.Context, name string, parentID int64) (StoredGroup, error) {
	now := time.Now().UTC()
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO groups (name, parent_id, created_at) VALUES (?, ?, ?) RETURNING id`,
		name, parentID, now.Format(time.RFC3339Nano)).Scan(&id)
	if err != nil {
		return StoredGroup{}, err
	}
	return StoredGroup{ID: id, Name: name, ParentID: parentID, CreatedAt: now}, nil
}

// UpdateGroup 更新分组名称和父分组。
func (s *Store) UpdateGroup(ctx context.Context, id int64, name string, parentID int64) error {
	// 不允许将分组设为自己的后代，防止循环
	if parentID > 0 {
		if parentID == id {
			return fmt.Errorf("cannot set parent to self")
		}
		if err := s.checkDescendant(ctx, id, parentID); err != nil {
			return err
		}
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE groups SET name = ?, parent_id = ? WHERE id = ?`, name, parentID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("group %d not found", id)
	}
	return nil
}

// checkDescendant 检查 targetID 是否是 groupID 的后代（防止循环引用）。
func (s *Store) checkDescendant(ctx context.Context, groupID, targetID int64) error {
	cursor := targetID
	for cursor > 0 {
		if cursor == groupID {
			return fmt.Errorf("cannot move group into its own descendant")
		}
		var parent int64
		err := s.db.QueryRowContext(ctx, `SELECT parent_id FROM groups WHERE id = ?`, cursor).Scan(&parent)
		if err != nil {
			return err
		}
		cursor = parent
	}
	return nil
}

// DeleteGroup 删除分组及其所有后代分组，同时清理 item_groups 关联。
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	// 收集所有后代分组 id
	ids := []int64{id}
	queue := []int64{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM groups WHERE parent_id = ?`, cur)
		if err != nil {
			return err
		}
		for rows.Next() {
			var child int64
			if err := rows.Scan(&child); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, child)
			queue = append(queue, child)
		}
		rows.Close()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, gid := range ids {
		if _, err := tx.Exec(`DELETE FROM item_groups WHERE group_id = ?`, gid); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`DELETE FROM groups WHERE id = ?`, gid); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ── Item-Group association ────────────────────────────────────

// AddItemToGroup 将条目加入分组。
func (s *Store) AddItemToGroup(ctx context.Context, itemID, groupID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `INSERT INTO item_groups (item_id, group_id)
		SELECT items.id, groups.id
		FROM clipboard_items AS items, groups
		WHERE items.id = ? AND groups.id = ?
		ON CONFLICT DO NOTHING`, itemID, groupID)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM item_groups WHERE item_id = ? AND group_id = ?
		)`, itemID, groupID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: item %d or group %d", errItemOrGroupNotFound, itemID, groupID)
		}
	}
	return tx.Commit()
}

// RemoveItemFromGroup 将条目从分组移除。
func (s *Store) RemoveItemFromGroup(ctx context.Context, itemID, groupID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM item_groups WHERE item_id = ? AND group_id = ?`, itemID, groupID)
	return err
}

// GetItemGroups 返回条目所属的所有分组 id。
func (s *Store) GetItemGroups(ctx context.Context, itemID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id FROM item_groups WHERE item_id = ? ORDER BY group_id ASC`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var gid int64
		if err := rows.Scan(&gid); err != nil {
			return nil, err
		}
		ids = append(ids, gid)
	}
	return ids, rows.Err()
}
