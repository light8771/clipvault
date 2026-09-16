// Package duckdb owns the DuckDB persistence boundary, including driver
// registration. Importing this package is sufficient for storage consumers to
// open the "duckdb" database/sql driver.
package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"

	duckdbdriver "github.com/duckdb/duckdb-go/v2"
)

// OpenEncrypted opens and verifies an encrypted database, restoring the default
// catalog on every physical connection. The caller must close the returned DB.
// ATTACH is database-wide, but USE is connection-local. Even a pool limited to
// one connection can replace it (for example after a cancelled transaction).
func OpenEncrypted(path, keyHex string) (*sql.DB, error) {
	attachSQL := fmt.Sprintf("ATTACH IF NOT EXISTS '%s' AS vault (ENCRYPTION_KEY '%s')",
		strings.ReplaceAll(path, "'", "''"), keyHex)
	connector, err := duckdbdriver.NewConnector("", func(conn driver.ExecerContext) error {
		if _, err := conn.ExecContext(context.Background(), attachSQL, nil); err != nil {
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
