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
	"slices"
	"strings"
	"time"

	duckdbstorage "clipboard_ext/internal/clipvault/storage/duckdb"
)

type Store struct {
	db    *sql.DB
	dbKey []byte
}

var (
	ErrItemNotFound        = errors.New("item not found")
	errItemOrGroupNotFound = errors.New("item or group not found")
)

const storeSchemaVersion = duckdbstorage.SchemaVersion

const storeSchemaIdentity = duckdbstorage.SchemaIdentity

const maxGroupNameRunes = 100

// FreeTierVisibleLimit 是未激活（免费版）可浏览/检索的最近记录条数。
// 数据照常录制入库；未激活时所有读取入口（列表、搜索、弹窗）只作用于这个窗口。
const FreeTierVisibleLimit = 100

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
	// 来源进程信息（前台窗口法）；空串表示未捕获或配置关闭。
	SourceExeName     string
	SourceExePath     string
	SourceWindowTitle string
	SourceMethod      string
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
	// 来源进程信息（前台窗口法）；未捕获时为空串。
	SourceExeName     string `json:"sourceExeName"`
	SourceExePath     string `json:"sourceExePath"`
	SourceWindowTitle string `json:"sourceWindowTitle"`
	SourceMethod      string `json:"sourceMethod"`
}

// clipboardItemColumns 是 clipboard_items 行查询的统一列清单，
// Search/GetByID/exportAll/exportItemsQuery 共用，防止多处列清单漂移。
// 扫描顺序必须与 scanStoredItem 一致。
const clipboardItemColumns = `id, created_at, kind, format, text, byte_len, char_len, sha256, CAST(meta_json AS VARCHAR), starred, sensitive, ` +
	`source_exe_name, source_exe_path, source_window_title, source_method`

// rowScanner 同时抽象 *sql.Row 与 *sql.Rows 的 Scan。
type rowScanner interface{ Scan(dest ...any) error }

func scanStoredItem(row rowScanner, item *StoredItem) error {
	if err := row.Scan(&item.ID, &item.CreatedAt, &item.Kind, &item.Format, &item.Text,
		&item.ByteLen, &item.CharLen, &item.SHA256, &item.MetaJSON, &item.Starred, &item.Sensitive,
		&item.SourceExeName, &item.SourceExePath, &item.SourceWindowTitle, &item.SourceMethod); err != nil {
		return err
	}
	item.CreatedAt = item.CreatedAt.UTC()
	return nil
}

// SearchOptions holds parameters for Store.Search.
type SearchOptions struct {
	Query            string
	Limit            int
	Offset           int // pagination offset
	StarredOnly      bool
	Kind             string // filter by kind (empty = all)
	CreatedFrom      string // RFC3339 inclusive UTC instant
	CreatedBefore    string // RFC3339 exclusive UTC instant
	SensitiveOnly    bool   // filter to sensitive items only
	IncludeSensitive bool   // include sensitive items in results (default false)
	GroupID          int64  // filter by group id (0 = no group filter)
	SkipTotal        bool   // skip total count when the caller only needs the current page
	// RestrictToLatestN > 0 时，查询仅作用于按 id DESC 取最近 N 条的窗口。
	// 未激活（免费版）由调用方填充 FreeTierVisibleLimit；0 表示不限制。
	RestrictToLatestN int
	// SourceExeNames 用于"来源应用"下拉多选精确过滤（原始 exe 名等值比较，IN 子句）；空=不过滤。
	SourceExeNames []string
	// ExeAliases 用于主搜索框按别名子串匹配时，把命中的原始名以精确匹配并入该 term 的 OR 组；空=不展开。
	ExeAliases map[string]string
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
	return openStoreDirectory(activeDatabaseDirectory(opts.DataDir), (*Store).initializeSchema)
}

func openStore(opts StoreOptions, initializeSchema func(*Store) error) (_ *Store, err error) {
	return openStoreDirectory(activeDatabaseDirectory(opts.DataDir), initializeSchema)
}

func openStoreDirectory(databaseDir string, initializeSchema func(*Store) error) (_ *Store, err error) {
	if err := os.MkdirAll(databaseDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir database dir: %w", err)
	}

	keyPath := databaseGroupFile(databaseDir, databaseKeyFileName)
	dbPath := databaseGroupFile(databaseDir, databaseFileName)

	keyExists, err := pathExists(keyPath)
	if err != nil {
		return nil, fmt.Errorf("stat key file: %w", err)
	}
	dbExists, err := pathExists(dbPath)
	if err != nil {
		return nil, fmt.Errorf("stat database file: %w", err)
	}
	if keyExists != dbExists {
		return nil, errors.New("database and key file must either both exist or both be absent")
	}
	newStore := !dbExists
	if newStore {
		defer func() {
			if err != nil {
				err = errors.Join(err, removeNewStoreFiles(keyPath, dbPath))
			}
		}()
	}
	var dbKey []byte
	if keyExists {
		key, _, err := ReadDPAPIKeyFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read DPAPI key file: %w", err)
		}
		dbKey = key
	} else {
		dbKey = make([]byte, 32)
		if _, err := rand.Read(dbKey); err != nil {
			return nil, fmt.Errorf("generate db key: %w", err)
		}
		if err := WriteDPAPIKeyFile(keyPath, dbKey); err != nil {
			return nil, fmt.Errorf("write DPAPI key file: %w", err)
		}
	}

	keyHex := hex.EncodeToString(dbKey)

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
	if newStore {
		err = initializeSchema(store)
	} else {
		err = store.validateSchema()
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func removeNewStoreFiles(keyPath, dbPath string) error {
	var dataErrors []error
	for _, path := range []string{dbPath + ".wal", dbPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			dataErrors = append(dataErrors, fmt.Errorf("remove %s: %w", filepath.Base(path), err))
		}
	}
	if err := errors.Join(dataErrors...); err != nil {
		return fmt.Errorf("clean up new database: %w", err)
	}
	if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clean up new key file: %w", err)
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// DBKey returns an ownership-independent copy of the database encryption key
// for recovery-file operations.
func (s *Store) DBKey() []byte { return slices.Clone(s.dbKey) }

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

func (s *Store) initializeSchema() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema initialization: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range duckdbstorage.SchemaStatements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("initialize schema: %w\nSQL: %s", err, stmt)
		}
	}
	cfg := DefaultConfig()
	normalizeConfig(&cfg)
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode default config: %w", err)
	}
	identityJSON, err := json.Marshal(storeSchemaIdentity)
	if err != nil {
		return fmt.Errorf("encode schema identity: %w", err)
	}
	for key, value := range map[string]string{
		"schema.version":   storeSchemaVersion,
		"schema.identity":  string(identityJSON),
		"config":           string(configJSON),
		"config.last_good": string(configJSON),
	} {
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, key, value); err != nil {
			return fmt.Errorf("initialize setting %q: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema initialization: %w", err)
	}
	return nil
}

func (s *Store) validateSchema() error {
	var version, identity string
	if err := s.db.QueryRow(`SELECT
		(SELECT json_extract_string(value, '$') FROM settings WHERE key = 'schema.version'),
		(SELECT json_extract_string(value, '$') FROM settings WHERE key = 'schema.identity')`).Scan(&version, &identity); err != nil {
		return fmt.Errorf("unsupported database schema; rebuild the development database: %w", err)
	}
	if version != storeSchemaVersion {
		return fmt.Errorf("unsupported database schema version %q; rebuild the development database", version)
	}
	if identity != storeSchemaIdentity {
		return fmt.Errorf("unsupported database schema identity %q; rebuild the development database", identity)
	}
	return nil
}
