// Package duckdb owns the DuckDB persistence boundary, including driver
// registration. Importing this package is sufficient for storage consumers to
// open the "duckdb" database/sql driver.
package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"

	duckdbdriver "github.com/duckdb/duckdb-go/v2"
)

var ErrEncryptionKeyMismatch = errors.New("database encryption key mismatch")

// OpenEncrypted opens and verifies an encrypted database, restoring the default
// catalog on every physical connection. The caller must close the returned DB.
// ATTACH is database-wide, but USE is connection-local. Even a pool limited to
// one connection can replace it (for example after a cancelled transaction).
func OpenEncrypted(path, keyHex string) (*sql.DB, error) {
	attachSQL := fmt.Sprintf("ATTACH IF NOT EXISTS '%s' AS vault (ENCRYPTION_KEY '%s')",
		strings.ReplaceAll(path, "'", "''"), keyHex)
	connector, err := duckdbdriver.NewConnector("", func(conn driver.ExecerContext) error {
		// 加密写入要求 httpfs 的 OpenSSL provider；首次启动先完成官方签名
		// 扩展安装和加载，避免依赖 ATTACH 的隐式自动加载。缓存命中不下载。
		for _, query := range []string{"INSTALL httpfs FROM core", "LOAD httpfs"} {
			if _, err := conn.ExecContext(context.Background(), query, nil); err != nil {
				return fmt.Errorf("初始化 DuckDB 安全加密提供程序失败: %w", err)
			}
		}
		if _, err := conn.ExecContext(context.Background(), attachSQL, nil); err != nil {
			// DuckDB exposes key mismatch as INVALID_INPUT rather than a
			// dedicated error type. Match its specific diagnostic, never all
			// attachment failures (locks, permissions, and corruption differ).
			var databaseErr *duckdbdriver.Error
			if errors.As(err, &databaseErr) && databaseErr.Type == duckdbdriver.ErrorTypeInvalidInput &&
				databaseErr.Msg == "Invalid Input Error: Wrong encryption key used to open the database file" {
				return fmt.Errorf("%w: %w", ErrEncryptionKeyMismatch, err)
			}
			return fmt.Errorf("ATTACH with ENCRYPTION_KEY: %w", err)
		}
		if _, err := conn.ExecContext(context.Background(), "USE vault", nil); err != nil {
			return fmt.Errorf("USE vault: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("open DuckDB connector: %w", err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
