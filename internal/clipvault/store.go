package clipvault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
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
	Offset           int    // pagination offset
	StarredOnly      bool
	Kind             string // filter by kind (empty = all)
	DateFrom         string // YYYY-MM-DD, inclusive lower bound on created_at (UTC date)
	DateTo           string // YYYY-MM-DD, inclusive upper bound on created_at (UTC date)
	SensitiveOnly    bool   // filter to sensitive items only
	IncludeSensitive bool   // include sensitive items in results (default true)
	GroupID          int64  // filter by group id (0 = no group filter)
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS clipboard_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TEXT NOT NULL,
			kind TEXT NOT NULL,
			format TEXT NOT NULL,
			text TEXT NOT NULL,
			byte_len INTEGER NOT NULL,
			char_len INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			meta_json TEXT NOT NULL,
			starred INTEGER NOT NULL DEFAULT 0,
			sensitive INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_created_at ON clipboard_items(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_kind ON clipboard_items(kind)`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_sha256 ON clipboard_items(sha256)`,
		// 迁移：为旧表（无 sensitive 列）添加该列，已存在则忽略错误
		`ALTER TABLE clipboard_items ADD COLUMN sensitive INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_sensitive ON clipboard_items(sensitive)`,
		// settings 表：存储应用配置（key-value，value 为 JSON）
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		// groups 表：用户自定义分组，支持嵌套
		`CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			parent_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_groups_parent ON groups(parent_id)`,
		// item_groups 表：条目与分组的多对多关联
		`CREATE TABLE IF NOT EXISTS item_groups (
			item_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL,
			PRIMARY KEY (item_id, group_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_item_groups_group ON item_groups(group_id)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			// ALTER TABLE ADD COLUMN 在列已存在时会报错，忽略即可
			if strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *Store) Save(ctx context.Context, item ClipboardItem) error {
	if item.MetaJSON == "" {
		item.MetaJSON = "{}"
	} else if !json.Valid([]byte(item.MetaJSON)) {
		item.MetaJSON = "{}"
	}
	sensVal := 0
	if item.Sensitive {
		sensVal = 1
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
		sensVal,
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
		conds = append(conds, "starred = 1")
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
		conds = append(conds, "sensitive = 1")
	} else if !opts.IncludeSensitive {
		conds = append(conds, "sensitive = 0")
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
		var starredInt, sensInt int
		if err := rows.Scan(&item.ID, &created, &item.Kind, &item.Format, &item.Text,
			&item.ByteLen, &item.CharLen, &item.SHA256, &item.MetaJSON, &starredInt, &sensInt); err != nil {
			return nil, 0, err
		}
		item.Starred = starredInt != 0
		item.Sensitive = sensInt != 0
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
	var starredInt, sensInt int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive
		FROM clipboard_items WHERE id = ?`, id).Scan(
		&item.ID, &created, &item.Kind, &item.Format, &item.Text,
		&item.ByteLen, &item.CharLen, &item.SHA256, &item.MetaJSON, &starredInt, &sensInt)
	if err != nil {
		return item, err
	}
	item.Starred = starredInt != 0
	item.Sensitive = sensInt != 0
	if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
		item.CreatedAt = parsed
	}
	return item, nil
}

func (s *Store) Star(ctx context.Context, id int64, starred bool) error {
	v := 0
	if starred {
		v = 1
	}
	result, err := s.db.ExecContext(ctx, `UPDATE clipboard_items SET starred = ? WHERE id = ?`, v, id)
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
		`INSERT OR REPLACE INTO settings (key, value) VALUES ('config', ?)`,
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
		var starredInt, sensInt int
		if err := rows.Scan(&item.ID, &created, &item.Kind, &item.Format, &item.Text, &item.ByteLen, &item.CharLen, &item.SHA256, &item.MetaJSON, &starredInt, &sensInt); err != nil {
			return nil, err
		}
		item.Starred = starredInt != 0
		item.Sensitive = sensInt != 0
		if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
			item.CreatedAt = parsed
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO groups (name, parent_id, created_at) VALUES (?, ?, ?)`,
		name, parentID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return StoredGroup{}, err
	}
	id, err := res.LastInsertId()
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
		`INSERT OR IGNORE INTO item_groups (item_id, group_id) VALUES (?, ?)`, itemID, groupID)
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
