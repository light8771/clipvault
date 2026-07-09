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
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parentId"`
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
	IncludeSensitive bool   // include sensitive items in results (default true)
	GroupID          int64  // filter by group id (0 = no group filter)
}

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

	if q := strings.TrimSpace(opts.Query); q != "" {
		like := "%" + escapeLike(q) + "%"
		conds = append(conds, `(text LIKE ? ESCAPE '\' OR format LIKE ? ESCAPE '\' OR sha256 LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like)
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

	// Count total matching rows
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clipboard_items"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
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
	result, err := s.db.ExecContext(ctx, `DELETE FROM clipboard_items WHERE id = ?`, id)
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

func (s *Store) ClearAll(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM clipboard_items`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetConfig 从 settings 表读取配置，不存在时返回默认配置。
func (s *Store) GetConfig() (Config, error) {
	cfg := DefaultConfig()
	var jsonStr string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'config'`).Scan(&jsonStr)
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive
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

// ExportData is the complete JSON export structure.
type ExportData struct {
	ExportVersion int             `json:"exportVersion"`
	App           string          `json:"app"`
	ExportedAt    string          `json:"exportedAt"`
	SourceEngine  string          `json:"sourceEngine"`
	Warning       string          `json:"warning"`
	Config        *Config         `json:"config,omitempty"`
	Items         []StoredItem    `json:"items"`
	Groups        []StoredGroup   `json:"groups"`
	ItemGroups    []ItemGroupLink `json:"itemGroups"`
}

// ItemGroupLink is a single item-group association for export/import.
type ItemGroupLink struct {
	ItemID  int64 `json:"itemId"`
	GroupID int64 `json:"groupId"`
}

// ExportFullForJSON exports all data (config + items + groups + itemGroups) for JSON import/export.
func (s *Store) ExportFullForJSON(ctx context.Context) (*ExportData, error) {
	items, err := s.ExportAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("export items: %w", err)
	}

	groups, err := s.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("export groups: %w", err)
	}

	// Export item_groups
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, group_id FROM item_groups ORDER BY item_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("export item_groups: %w", err)
	}
	defer rows.Close()
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

	cfg, err := s.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("export config: %w", err)
	}
	// 导出时清除密码哈希和盐，避免泄露
	cfg.Security.LockPasswordHash = ""
	cfg.Security.SecondaryPasswordHash = ""
	cfg.Security.PasswordSalt = ""

	return &ExportData{
		ExportVersion: 2,
		App:           "ClipVault",
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "This export contains plaintext clipboard content.",
		Config:        &cfg,
		Items:         items,
		Groups:        groups,
		ItemGroups:    links,
	}, nil
}

// ImportFullJSON imports a complete JSON export into the database.
// It remaps IDs to avoid conflicts and uses a transaction for atomicity.
func (s *Store) ImportFullJSON(ctx context.Context, data []byte) error {
	var export ExportData
	if err := json.Unmarshal(data, &export); err != nil {
		return fmt.Errorf("parse export JSON: %w", err)
	}
	if export.App != "ClipVault" {
		return fmt.Errorf("not a ClipVault export (app=%q)", export.App)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Import groups with ID remapping
	groupIDMap := make(map[int64]int64) // oldID -> newID
	for _, g := range export.Groups {
		var newID int64
		parentID := groupIDMap[g.ParentID] // 0 if not found (top-level)
		err := tx.QueryRowContext(ctx,
			`INSERT INTO groups (name, parent_id, created_at) VALUES (?, ?, ?) RETURNING id`,
			g.Name, parentID, time.Now().UTC().Format(time.RFC3339Nano),
		).Scan(&newID)
		if err != nil {
			return fmt.Errorf("import group %d: %w", g.ID, err)
		}
		groupIDMap[g.ID] = newID
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
		newItemID, ok1 := itemIDMap[link.ItemID]
		newGroupID, ok2 := groupIDMap[link.GroupID]
		if !ok1 || !ok2 {
			continue // skip orphaned links
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_groups (item_id, group_id) VALUES (?, ?) ON CONFLICT DO NOTHING`,
			newItemID, newGroupID,
		); err != nil {
			return fmt.Errorf("import item_group: %w", err)
		}
	}

	// Import config (optional, only if present)
	// 保留当前密码安全配置，不被导入的空值覆盖
	if export.Config != nil {
		curCfg, err := s.GetConfig()
		if err == nil {
			export.Config.Security = curCfg.Security
		}
		cfgData, err := json.Marshal(export.Config)
		if err != nil {
			return fmt.Errorf("marshal config: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO settings (key, value) VALUES ('config', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			string(cfgData),
		); err != nil {
			return fmt.Errorf("import config: %w", err)
		}
	}

	return tx.Commit()
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, parent_id FROM groups ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []StoredGroup
	for rows.Next() {
		var g StoredGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.ParentID); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// CreateGroup 创建一个新分组。
func (s *Store) CreateGroup(ctx context.Context, name string, parentID int64) (StoredGroup, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO groups (name, parent_id, created_at) VALUES (?, ?, ?) RETURNING id`,
		name, parentID, time.Now().UTC().Format(time.RFC3339Nano)).Scan(&id)
	if err != nil {
		return StoredGroup{}, err
	}
	return StoredGroup{ID: id, Name: name, ParentID: parentID}, nil
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
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO item_groups (item_id, group_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, itemID, groupID)
	return err
}

// RemoveItemFromGroup 将条目从分组移除。
func (s *Store) RemoveItemFromGroup(ctx context.Context, itemID, groupID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM item_groups WHERE item_id = ? AND group_id = ?`, itemID, groupID)
	return err
}

// GetItemGroups 返回条目所属的所有分组 id。
func (s *Store) GetItemGroups(ctx context.Context, itemID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id FROM item_groups WHERE item_id = ?`, itemID)
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
