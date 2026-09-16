package duckdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenEncryptedVerifiesKeyAndReleasesFailedOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault's data.duckdb")
	key := strings.Repeat("12", 32)
	db, err := OpenEncrypted(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE retained AS SELECT 42 AS value"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	badDB, err := OpenEncrypted(path, strings.Repeat("34", 32))
	if badDB != nil {
		_ = badDB.Close()
		t.Fatal("failed open returned a database handle")
	}
	if err == nil {
		t.Fatal("wrong key must fail during open")
	}
	db, err = OpenEncrypted(path, key)
	if err != nil {
		t.Fatalf("reopen after wrong key: %v", err)
	}
	defer db.Close()
	var value int
	if err := db.QueryRow("SELECT value FROM retained").Scan(&value); err != nil || value != 42 {
		t.Fatalf("persisted value = %d, err = %v", value, err)
	}
}

func TestOpenEncryptedSurvivesCancelledTransaction(t *testing.T) {
	db, err := OpenEncrypted(filepath.Join(t.TempDir(), "vault.duckdb"), strings.Repeat("56", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE retained AS SELECT 42 AS value"); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var original any
	if err := conn.Raw(func(raw any) error { original = raw; return nil }); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	transactionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	tx, err := db.BeginTx(transactionCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(transactionCtx, "INSERT INTO retained VALUES (7)"); err != nil {
		t.Fatal(err)
	}
	cancel()
	// The pool has one connection. Acquisition waits for automatic rollback,
	// which discards this driver's connection after transaction cancellation.
	conn, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		if raw == original {
			t.Error("cancellation did not replace the physical connection")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var catalog string
	var total int
	if err := conn.QueryRowContext(ctx, "SELECT current_database(), SUM(value) FROM retained").Scan(&catalog, &total); err != nil {
		t.Fatal(err)
	}
	if catalog != "vault" || total != 42 {
		t.Fatalf("after cancellation: catalog=%q total=%d", catalog, total)
	}
}
