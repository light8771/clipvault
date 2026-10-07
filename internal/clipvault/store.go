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
	"time"

	"clipboard_ext/internal/clipvault/domain"
	duckdbstorage "clipboard_ext/internal/clipvault/storage/duckdb"
)

type Store struct {
	db      *sql.DB
	dbKey   []byte
	dataDir string
}

var (
	ErrItemNotFound             = domain.ErrItemNotFound
	errItemOrGroupNotFound      = errors.New("item or group not found")
	errDatabaseRecoveryRequired = errors.New("database key requires recovery")
)

const storeSchemaVersion = duckdbstorage.SchemaVersion

const storeSchemaIdentity = duckdbstorage.SchemaIdentity

const maxGroupNameRunes = 100

// FreeTierVisibleLimit 是未激活（免费版）可浏览/检索的最近记录条数。
// 数据照常录制入库；未激活时所有读取入口（列表、搜索、弹窗）只作用于这个窗口。
const FreeTierVisibleLimit = domain.FreeTierVisibleLimit

const (
	RecordTypeContent = domain.HistoryRecordContent
	RecordTypeEvent   = domain.HistoryRecordEvent
)

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
const clipboardItemColumnsBeforeDuplicate = `id, created_at, record_type, kind, format, text, byte_len, char_len, content_sha256, `
const clipboardItemColumnsAfterDuplicate = `, CAST(meta_json AS VARCHAR), starred, sensitive, event_code, event_fingerprint, capture_sequence, ` +
	`capture_stage, capture_attempts, CAST(event_formats AS VARCHAR), error_category, ` +
	`source_exe_name, source_exe_path, source_window_title, source_attribution, CAST(content_json AS VARCHAR)`

const clipboardItemColumns = clipboardItemColumnsBeforeDuplicate + `consecutive_duplicate` + clipboardItemColumnsAfterDuplicate

const clipboardItemColumnsAliased = `i.id, i.created_at, i.record_type, i.kind, i.format, i.text, i.byte_len, i.char_len, i.content_sha256, i.consecutive_duplicate, ` +
	`CAST(i.meta_json AS VARCHAR), i.starred, i.sensitive, i.event_code, i.event_fingerprint, i.capture_sequence, ` +
	`i.capture_stage, i.capture_attempts, CAST(i.event_formats AS VARCHAR), i.error_category, ` +
	`i.source_exe_name, i.source_exe_path, i.source_window_title, i.source_attribution, CAST(i.content_json AS VARCHAR)`

// rowScanner 同时抽象 *sql.Row 与 *sql.Rows 的 Scan。
type rowScanner interface{ Scan(dest ...any) error }

func scanStoredItem(row rowScanner, item *StoredItem) error {
	state := storedItemScanState{}
	if err := row.Scan(storedItemScanDest(item, &state)...); err != nil {
		return err
	}
	return finishStoredItemScan(item, state)
}

type storedItemScanState struct {
	contentJSON      sql.NullString
	createdAtMS      int64
	contentHash      sql.NullString
	eventCode        sql.NullString
	eventFingerprint sql.NullString
	captureSequence  uint64
	eventFormatsJSON string
}

func storedItemScanDest(item *StoredItem, state *storedItemScanState) []any {
	return []any{
		&item.ID, &state.createdAtMS, &item.RecordType, &item.Kind, &item.Format, &item.Text,
		&item.ByteLen, &item.CharLen, &state.contentHash, &item.ConsecutiveDuplicate,
		&item.MetaJSON, &item.Starred, &item.Sensitive,
		&state.eventCode, &state.eventFingerprint, &state.captureSequence, &item.CaptureStage,
		&item.CaptureAttempts, &state.eventFormatsJSON, &item.ErrorCategory, &item.SourceExeName,
		&item.SourceExePath, &item.SourceWindowTitle, &item.SourceAttribution,
		&state.contentJSON,
	}
}

func finishStoredItemScan(item *StoredItem, state storedItemScanState) error {
	if state.contentJSON.Valid {
		if err := json.Unmarshal([]byte(state.contentJSON.String), &item.Content); err != nil {
			return err
		}
	}
	if state.contentHash.Valid {
		item.ContentSHA256 = &state.contentHash.String
	}
	if state.eventCode.Valid {
		item.EventCode = domain.ClipboardEventCode(state.eventCode.String)
	}
	if state.eventFingerprint.Valid {
		item.EventFingerprint = state.eventFingerprint.String
	}
	item.CaptureSequence = uint32(state.captureSequence)
	if err := json.Unmarshal([]byte(state.eventFormatsJSON), &item.EventFormats); err != nil {
		return fmt.Errorf("decode event formats: %w", err)
	}
	item.CreatedAt = time.UnixMilli(state.createdAtMS).UTC()
	return nil
}

func scanGroupedEvent(row rowScanner, item *StoredItem) error {
	state := storedItemScanState{}
	var firstOccurredMS, lastOccurredMS int64
	dest := append(storedItemScanDest(item, &state), &item.OccurrenceCount, &firstOccurredMS, &lastOccurredMS)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	if err := finishStoredItemScan(item, state); err != nil {
		return err
	}
	item.FirstOccurredAt = time.UnixMilli(firstOccurredMS).UTC()
	item.LastOccurredAt = time.UnixMilli(lastOccurredMS).UTC()
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
	return openStoreDirectory(opts.DataDir, activeDatabaseDirectory(opts.DataDir), (*Store).initializeSchema)
}

func openStore(opts StoreOptions, initializeSchema func(*Store) error) (_ *Store, err error) {
	return openStoreDirectory(opts.DataDir, activeDatabaseDirectory(opts.DataDir), initializeSchema)
}

func openStoreDirectory(dataDir, databaseDir string, initializeSchema func(*Store) error) (_ *Store, err error) {
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
		if dbExists {
			return nil, fmt.Errorf("%w: database key file is missing", errDatabaseRecoveryRequired)
		}
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
			var pathErr *os.PathError
			if errors.As(err, &pathErr) && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("read database key file: %w", err)
			}
			return nil, fmt.Errorf("%w: read DPAPI key file: %w", errDatabaseRecoveryRequired, err)
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

	db, err := duckdbstorage.OpenEncrypted(dbPath, keyHex)
	if err != nil {
		if errors.Is(err, duckdbstorage.ErrEncryptionKeyMismatch) {
			return nil, fmt.Errorf("%w: %w", errDatabaseRecoveryRequired, err)
		}
		return nil, err
	}

	store := &Store{db: db, dbKey: dbKey, dataDir: filepath.Clean(dataDir)}
	if newStore {
		err = initializeSchema(store)
	} else {
		err = store.validateSchema()
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	// 官方词条集按 i18n.seed_version 标记幂等同步：新库首次写入，
	// 已同步的库只花一次 settings 读，词条集版本前进后增量更新。
	if err := store.ensureI18nSeed(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensure i18n seed: %w", err)
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
	version, identity, err := s.readSchemaMarkers()
	if err != nil {
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

func (s *Store) readSchemaMarkers() (version, identity string, err error) {
	if err := s.db.QueryRow(`SELECT
		(SELECT json_extract_string(value, '$') FROM settings WHERE key = 'schema.version'),
		(SELECT json_extract_string(value, '$') FROM settings WHERE key = 'schema.identity')`).Scan(&version, &identity); err != nil {
		return "", "", err
	}
	return version, identity, nil
}
