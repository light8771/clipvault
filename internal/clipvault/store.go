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

	"clipboard_ext/internal/clipvault/domain"
	duckdbstorage "clipboard_ext/internal/clipvault/storage/duckdb"
)

type Store struct {
	db    *sql.DB
	dbKey []byte
}

var (
	ErrItemNotFound        = domain.ErrItemNotFound
	errItemOrGroupNotFound = errors.New("item or group not found")
)

const storeSchemaVersion = duckdbstorage.SchemaVersion

const storeSchemaIdentity = duckdbstorage.SchemaIdentity

const maxGroupNameRunes = 100

// FreeTierVisibleLimit 是未激活（免费版）可浏览/检索的最近记录条数。
// 数据照常录制入库；未激活时所有读取入口（列表、搜索、弹窗）只作用于这个窗口。
const FreeTierVisibleLimit = domain.FreeTierVisibleLimit

// 业务模型的唯一定义在 internal/clipvault/domain；根包只保留别名，
// 历史调用点无需改动，也避免再次出现双份定义漂移。
type (
	ClipboardItem  = domain.ClipboardItem
	StoredItem     = domain.StoredItem
	SearchOptions  = domain.SearchOptions
	Group          = domain.Group
	StoredGroup    = domain.StoredGroup
	CleanupOptions = domain.CleanupOptions
)

type storeQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// BackupHistory 是一次备份执行（目录或邮件）的不可变记录，成功/失败/跳过都留痕。
type BackupHistory struct {
	CreatedAt time.Time `json:"createdAt"`       // 执行时间
	Kind      string    `json:"kind"`            // "dir" / "email"
	Trigger   string    `json:"trigger"`         // "scheduled" / "startup" / "manual"
	Mode      string    `json:"mode"`            // "full" / "incremental" / "skipped"
	OK        bool      `json:"ok"`              // 是否成功（跳过视为成功）
	Error     string    `json:"error,omitempty"` // 失败原因（已截断）
	Target    string    `json:"target"`          // 备份目录路径 / 收件邮箱
	Format    string    `json:"format"`          // 内容格式
	Parts     int       `json:"parts,omitempty"` // 邮件分片数（目录备份为 1）
	Items     int       `json:"items"`           // 导出的记录条数
}

// clipboardItemColumns 是 clipboard_items 行查询的统一列清单，
// Search/GetByID/exportAll/exportItemsQuery 共用，防止多处列清单漂移。
// 扫描顺序必须与 scanStoredItem 一致。
const clipboardItemColumns = `id, created_at, kind, format, text, byte_len, char_len, sha256, CAST(meta_json AS VARCHAR), starred, sensitive, ` +
	`content_available, is_duplicate, unavailable_reason, ` +
	`source_exe_name, source_exe_path, source_window_title, source_method`

// rowScanner 同时抽象 *sql.Row 与 *sql.Rows 的 Scan。
type rowScanner interface{ Scan(dest ...any) error }

func scanStoredItem(row rowScanner, item *StoredItem) error {
	var createdAtMS int64
	if err := row.Scan(&item.ID, &createdAtMS, &item.Kind, &item.Format, &item.Text,
		&item.ByteLen, &item.CharLen, &item.SHA256, &item.MetaJSON, &item.Starred, &item.Sensitive,
		&item.ContentAvailable, &item.IsDuplicate, &item.UnavailableReason,
		&item.SourceExeName, &item.SourceExePath, &item.SourceWindowTitle, &item.SourceMethod); err != nil {
		return err
	}
	item.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	return nil
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
	if err := store.ensureAuditColumns(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// ensureAuditColumns 为 v6 早期数据库幂等补充 api_audit_log 的
// request_summary / request_body 列。DuckDB 的 ADD COLUMN 不支持列约束，
// 因此建可空列后回填空串；写入路径始终显式提供该值，故后续不会出现 NULL。
func (s *Store) ensureAuditColumns() error {
	for _, column := range []string{"request_summary", "request_body"} {
		if _, err := s.db.Exec(`ALTER TABLE api_audit_log ADD COLUMN IF NOT EXISTS ` + column + ` VARCHAR`); err != nil {
			return fmt.Errorf("migrate api_audit_log.%s: %w", column, err)
		}
		if _, err := s.db.Exec(`UPDATE api_audit_log SET ` + column + ` = '' WHERE ` + column + ` IS NULL`); err != nil {
			return fmt.Errorf("backfill api_audit_log.%s: %w", column, err)
		}
	}
	return nil
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
