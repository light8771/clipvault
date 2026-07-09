package clipvault

// POC: DuckDB 加密能力验证
// 本文件仅在 POC 阶段使用，验证通过后会被删除或合并到正式代码中。
//
// 验证项：
//  1. duckdb-go 能在 Windows + CGO 下编译运行
//  2. ATTACH ... ENCRYPTION_KEY 可用
//  3. 正确 key 可读写、错误 key / 无 key 不可读
//  4. 正文不可被文本搜索
//  5. RowsAffected() 行为
//  6. INSERT ... RETURNING id
//  7. LIKE ESCAPE 行为
//  8. ON CONFLICT 行为
//  9. SetMaxOpenConns(1) 下 CRUD 全量跑通

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

// generateTestKey 生成 32 字节随机 key 并返回 hex 编码字符串。
func generateTestKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b) // 64 个 [0-9a-f] 字符
}

// escapeSQLPath 对 SQL 路径中的单引号做转义（' -> ''）。
func escapeSQLPath(path string) string {
	return strings.ReplaceAll(path, "'", "''")
}

// openDuckDBEncrypted 打开一个加密的 DuckDB 数据库。
// dbPath: .duckdb 文件路径
// keyHex: hex 编码的加密 key
func openDuckDBEncrypted(t *testing.T, dbPath, keyHex string) *sql.DB {
	t.Helper()
	// DuckDB :memory: 模式 + ATTACH 加密文件
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("sql.Open(duckdb): %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	escapedPath := escapeSQLPath(dbPath)
	attachSQL := fmt.Sprintf("ATTACH '%s' AS vault (ENCRYPTION_KEY '%s')", escapedPath, keyHex)
	if _, err := db.Exec(attachSQL); err != nil {
		t.Fatalf("ATTACH with ENCRYPTION_KEY: %v", err)
	}
	if _, err := db.Exec("USE vault"); err != nil {
		t.Fatalf("USE vault: %v", err)
	}
	return db
}

// TestPOC1_EncryptionBasic 验证 DuckDB 加密基本能力。
func TestPOC1_EncryptionBasic(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_encrypted.duckdb")
	keyHex := generateTestKey(t)

	// 1. 创建加密库并写入数据
	db := openDuckDBEncrypted(t, dbPath, keyHex)
	defer db.Close()

	schema := `
		CREATE SEQUENCE IF NOT EXISTS seq_items START 1;
		CREATE TABLE IF NOT EXISTS items (
			id BIGINT PRIMARY KEY DEFAULT nextval('seq_items'),
			text TEXT NOT NULL,
			starred BOOLEAN NOT NULL DEFAULT false
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	testText := "这是中文测试 hello world 🎉 emoji"
	if _, err := db.Exec("INSERT INTO items (text, starred) VALUES (?, ?)", testText, true); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// 读回验证
	var id int64
	var text string
	var starred bool
	err := db.QueryRow("SELECT id, text, starred FROM items WHERE id = 1").Scan(&id, &text, &starred)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if text != testText {
		t.Fatalf("text mismatch: got %q, want %q", text, testText)
	}
	if !starred {
		t.Fatalf("starred should be true")
	}
	t.Logf("[OK] 正确 key 读写成功: id=%d text=%q starred=%v", id, text, starred)

	db.Close()

	// 2. 错误 key 不能读
	wrongKey := generateTestKey(t)
	db2, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("sql.Open for wrong key: %v", err)
	}
	defer db2.Close()
	db2.SetMaxOpenConns(1)
	escapedPath := escapeSQLPath(dbPath)
	wrongAttach := fmt.Sprintf("ATTACH '%s' AS vault (ENCRYPTION_KEY '%s')", escapedPath, wrongKey)
	_, err = db2.Exec(wrongAttach)
	if err == nil {
		// ATTACH 可能成功但读取时会失败
		_, err = db2.Exec("USE vault")
		if err == nil {
			_, err = db2.Exec("SELECT COUNT(*) FROM items")
		}
	}
	if err == nil {
		t.Fatalf("错误 key 竟然能读取加密数据库！")
	}
	t.Logf("[OK] 错误 key 被拒绝: %v", err)
	db2.Close()

	// 3. 无 key 不能读
	db3, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("sql.Open for no key: %v", err)
	}
	defer db3.Close()
	db3.SetMaxOpenConns(1)
	noKeyAttach := fmt.Sprintf("ATTACH '%s' AS vault", escapedPath)
	_, err = db3.Exec(noKeyAttach)
	if err == nil {
		_, err = db3.Exec("USE vault")
		if err == nil {
			_, err = db3.Exec("SELECT COUNT(*) FROM items")
		}
	}
	if err == nil {
		t.Fatalf("无 key 竟然能读取加密数据库！")
	}
	t.Logf("[OK] 无 key 被拒绝: %v", err)
	db3.Close()

	// 4. 正文不可被文本搜索
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db file: %v", err)
	}
	if strings.Contains(string(raw), testText) {
		t.Fatalf("加密数据库文件中找到了明文正文！")
	}
	t.Logf("[OK] 数据库文件中未找到明文正文（文件大小 %d bytes）", len(raw))

	// 5. 正确 key 重开可读
	db4 := openDuckDBEncrypted(t, dbPath, keyHex)
	defer db4.Close()
	var count int
	if err := db4.QueryRow("SELECT COUNT(*) FROM items").Scan(&count); err != nil {
		t.Fatalf("reopen with correct key: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
	t.Logf("[OK] 正确 key 重开成功，共 %d 条记录", count)
}

// TestPOC2_RowsAffected 验证 RowsAffected() 行为。
func TestPOC2_RowsAffected(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_ra.duckdb")
	keyHex := generateTestKey(t)
	db := openDuckDBEncrypted(t, dbPath, keyHex)
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS test_ra (id INTEGER PRIMARY KEY, val TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// INSERT
	res, err := db.Exec("INSERT INTO test_ra VALUES (1, 'a')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	n, err := res.RowsAffected()
	t.Logf("[RowsAffected] INSERT: n=%d err=%v", n, err)

	// UPDATE 命中 1 行
	res, err = db.Exec("UPDATE test_ra SET val='b' WHERE id=1")
	if err != nil {
		t.Fatalf("update hit: %v", err)
	}
	n, err = res.RowsAffected()
	t.Logf("[RowsAffected] UPDATE hit 1: n=%d err=%v", n, err)
	if n != 1 {
		t.Logf("[WARN] UPDATE 命中1行但 RowsAffected=%d（可能不稳定）", n)
	}

	// UPDATE 命中 0 行
	res, err = db.Exec("UPDATE test_ra SET val='c' WHERE id=999")
	if err != nil {
		t.Fatalf("update miss: %v", err)
	}
	n, err = res.RowsAffected()
	t.Logf("[RowsAffected] UPDATE hit 0: n=%d err=%v", n, err)
	if n != 0 {
		t.Logf("[WARN] UPDATE 命中0行但 RowsAffected=%d（可能不稳定）", n)
	}

	// DELETE 命中 1 行
	res, err = db.Exec("DELETE FROM test_ra WHERE id=1")
	if err != nil {
		t.Fatalf("delete hit: %v", err)
	}
	n, err = res.RowsAffected()
	t.Logf("[RowsAffected] DELETE hit 1: n=%d err=%v", n, err)

	// DELETE 命中 0 行
	res, err = db.Exec("DELETE FROM test_ra WHERE id=999")
	if err != nil {
		t.Fatalf("delete miss: %v", err)
	}
	n, err = res.RowsAffected()
	t.Logf("[RowsAffected] DELETE hit 0: n=%d err=%v", n, err)
}

// TestPOC3_ReturningID 验证 INSERT ... RETURNING id 行为。
func TestPOC3_ReturningID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_ret.duckdb")
	keyHex := generateTestKey(t)
	db := openDuckDBEncrypted(t, dbPath, keyHex)
	defer db.Close()

	if _, err := db.Exec(`CREATE SEQUENCE IF NOT EXISTS seq_ret START 1;`); err != nil {
		t.Fatalf("create sequence: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS test_ret (id BIGINT PRIMARY KEY DEFAULT nextval('seq_ret'), name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	var id int64
	err := db.QueryRow("INSERT INTO test_ret (name) VALUES (?) RETURNING id", "first").Scan(&id)
	if err != nil {
		t.Fatalf("INSERT RETURNING: %v", err)
	}
	t.Logf("[OK] RETURNING id = %d", id)
	if id != 1 {
		t.Logf("[WARN] expected id=1, got %d", id)
	}

	var id2 int64
	err = db.QueryRow("INSERT INTO test_ret (name) VALUES (?) RETURNING id", "second").Scan(&id2)
	if err != nil {
		t.Fatalf("INSERT RETURNING 2: %v", err)
	}
	t.Logf("[OK] RETURNING id = %d", id2)
}

// TestPOC4_LikeEscape 验证 LIKE ESCAPE 行为。
func TestPOC4_LikeEscape(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_like.duckdb")
	keyHex := generateTestKey(t)
	db := openDuckDBEncrypted(t, dbPath, keyHex)
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS test_like (id INTEGER PRIMARY KEY, text TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	rows := []string{
		"100% discount",
		"item_001",
		"back\\slash",
		"中文测试项目",
		"project alpha",
		"PROJECT BETA",
	}
	for i, r := range rows {
		if _, err := db.Exec("INSERT INTO test_like VALUES (?, ?)", i+1, r); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	tests := []struct {
		pattern string
		expect  int
		desc    string
	}{
		{"%discount", 1, "百分号 literal"},
		{"item\\_001", 0, "下划线 literal (escaped)"},
		{"item_001", 1, "下划号 wildcard"},
		{"%中文%", 1, "中文 LIKE"},
		{"%project%", 1, "英文小写 LIKE (大小写敏感)"},
		{"%PROJECT%", 1, "英文大写 LIKE"},
	}

	for _, tc := range tests {
		var count int
		like := "%" + tc.pattern + "%"
		// 测试 ESCAPE '\'
		err := db.QueryRow("SELECT COUNT(*) FROM test_like WHERE text LIKE ? ESCAPE '\\'", like).Scan(&count)
		if err != nil {
			t.Logf("[FAIL] LIKE ESCAPE pattern=%q: err=%v", tc.pattern, err)
			continue
		}
		status := "OK"
		if count != tc.expect {
			status = "WARN"
		}
		t.Logf("[%s] LIKE ESCAPE pattern=%q -> count=%d (expect %d) [%s]", status, tc.pattern, count, tc.expect, tc.desc)
	}

	// 测试 ILIKE
	var ilikeCount int
	err := db.QueryRow("SELECT COUNT(*) FROM test_like WHERE text ILIKE '%project%'").Scan(&ilikeCount)
	if err != nil {
		t.Logf("[FAIL] ILIKE: err=%v", err)
	} else {
		t.Logf("[OK] ILIKE '%%project%%' -> count=%d (大小写不敏感应=2)", ilikeCount)
	}
}

// TestPOC5_OnConflict 验证 ON CONFLICT 行为。
func TestPOC5_OnConflict(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_conflict.duckdb")
	keyHex := generateTestKey(t)
	db := openDuckDBEncrypted(t, dbPath, keyHex)
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS test_conflict (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// ON CONFLICT DO UPDATE (upsert)
	if _, err := db.Exec("INSERT INTO test_conflict (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		"config", "v1"); err != nil {
		t.Fatalf("insert upsert: %v", err)
	}
	var v string
	if err := db.QueryRow("SELECT value FROM test_conflict WHERE key = 'config'").Scan(&v); err != nil {
		t.Fatalf("select 1: %v", err)
	}
	t.Logf("[OK] ON CONFLICT DO UPDATE: value=%s", v)

	// 再次 upsert，应该更新
	if _, err := db.Exec("INSERT INTO test_conflict (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		"config", "v2"); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if err := db.QueryRow("SELECT value FROM test_conflict WHERE key = 'config'").Scan(&v); err != nil {
		t.Fatalf("select 2: %v", err)
	}
	if v != "v2" {
		t.Fatalf("upsert failed: got %s, want v2", v)
	}
	t.Logf("[OK] ON CONFLICT DO UPDATE 2: value=%s (updated)", v)

	// ON CONFLICT DO NOTHING
	if _, err := db.Exec("INSERT INTO test_conflict (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING",
		"config", "v3"); err != nil {
		t.Fatalf("conflict do nothing: %v", err)
	}
	if err := db.QueryRow("SELECT value FROM test_conflict WHERE key = 'config'").Scan(&v); err != nil {
		t.Fatalf("select 3: %v", err)
	}
	if v != "v2" {
		t.Fatalf("DO NOTHING failed: got %s, want v2", v)
	}
	t.Logf("[OK] ON CONFLICT DO NOTHING: value=%s (not changed)", v)
}

// TestPOC6_FullCRUDWithSingleConn 验证 SetMaxOpenConns(1) 下 CRUD 全量跑通。
func TestPOC6_FullCRUDWithSingleConn(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_crud.duckdb")
	keyHex := generateTestKey(t)
	db := openDuckDBEncrypted(t, dbPath, keyHex)
	defer db.Close()

	// 建表（高仿 clipboard_items）
	schema := `
		CREATE SEQUENCE IF NOT EXISTS seq_ci START 1;
		CREATE TABLE IF NOT EXISTS clipboard_items (
			id BIGINT PRIMARY KEY DEFAULT nextval('seq_ci'),
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
		);
		CREATE INDEX IF NOT EXISTS idx_ci_created ON clipboard_items(created_at);
		CREATE INDEX IF NOT EXISTS idx_ci_kind ON clipboard_items(kind);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	// 批量插入（中文/英文/emoji/长文本）
	testItems := []struct {
		text string
		kind string
	}{
		{"Hello World", "text"},
		{"你好世界", "text"},
		{"🎉🎊🎈 emoji test", "text"},
		{strings.Repeat("长文本测试", 1000), "text"},
		{"C:\\Users\\test\\file.txt", "file"},
		{"password=123456&token=abc", "text"},
	}

	ctx := context.Background()
	for i, item := range testItems {
		_, err := db.ExecContext(ctx, `INSERT INTO clipboard_items
			(created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"2026-07-08T12:00:00.000000000Z",
			item.kind,
			"CF_UNICODETEXT",
			item.text,
			len(item.text),
			len([]rune(item.text)),
			fmt.Sprintf("sha256_%d", i),
			"{}",
			i%2 == 0,
			i == 4, // 第5条敏感
		)
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// SELECT COUNT
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM clipboard_items").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != len(testItems) {
		t.Fatalf("expected %d rows, got %d", len(testItems), count)
	}
	t.Logf("[OK] 插入 %d 条，count=%d", len(testItems), count)

	// LIKE 搜索中文
	var cnCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM clipboard_items WHERE text LIKE '%你好%'").Scan(&cnCount); err != nil {
		t.Fatalf("search cn: %v", err)
	}
	t.Logf("[OK] LIKE '%%你好%%' -> %d", cnCount)

	// LIKE 搜索 emoji
	var emojiCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM clipboard_items WHERE text LIKE '%🎉%'").Scan(&emojiCount); err != nil {
		t.Fatalf("search emoji: %v", err)
	}
	t.Logf("[OK] LIKE '%%🎉%%' -> %d", emojiCount)

	// BOOLEAN 过滤
	var starCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM clipboard_items WHERE starred = true").Scan(&starCount); err != nil {
		t.Fatalf("starred count: %v", err)
	}
	t.Logf("[OK] starred=true -> %d", starCount)

	var sensCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM clipboard_items WHERE sensitive = true").Scan(&sensCount); err != nil {
		t.Fatalf("sensitive count: %v", err)
	}
	t.Logf("[OK] sensitive=true -> %d", sensCount)

	// ORDER BY + LIMIT
	rows, err := db.Query("SELECT id, text, starred, sensitive FROM clipboard_items ORDER BY id DESC LIMIT 3")
	if err != nil {
		t.Fatalf("query desc: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var text string
		var starred, sensitive bool
		if err := rows.Scan(&id, &text, &starred, &sensitive); err != nil {
			t.Fatalf("scan: %v", err)
		}
		preview := text
		if len(preview) > 50 {
			preview = preview[:50] + "..."
		}
		t.Logf("[OK] DESC id=%d starred=%v sensitive=%v text=%q", id, starred, sensitive, preview)
	}

	// UPDATE
	if _, err := db.Exec("UPDATE clipboard_items SET starred = true WHERE id = 1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	var starred1 bool
	if err := db.QueryRow("SELECT starred FROM clipboard_items WHERE id = 1").Scan(&starred1); err != nil {
		t.Fatalf("select after update: %v", err)
	}
	if !starred1 {
		t.Fatalf("update starred failed")
	}
	t.Logf("[OK] UPDATE starred -> %v", starred1)

	// DELETE
	if _, err := db.Exec("DELETE FROM clipboard_items WHERE id = ?", len(testItems)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM clipboard_items").Scan(&count); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if count != len(testItems)-1 {
		t.Fatalf("expected %d after delete, got %d", len(testItems)-1, count)
	}
	t.Logf("[OK] DELETE -> count=%d", count)

	// substr 日期过滤
	var dateCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM clipboard_items WHERE substr(created_at,1,10) >= '2026-07-08'").Scan(&dateCount); err != nil {
		t.Fatalf("substr date: %v", err)
	}
	t.Logf("[OK] substr(created_at,1,10) >= '2026-07-08' -> %d", dateCount)
}
