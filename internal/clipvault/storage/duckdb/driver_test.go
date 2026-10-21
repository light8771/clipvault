package duckdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenEncryptedDoesNotClassifyCorruptionAsWrongKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.duckdb")
	if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := OpenEncrypted(path, strings.Repeat("12", 32))
	if db != nil {
		db.Close()
		t.Fatal("corrupt database unexpectedly opened")
	}
	if err == nil || errors.Is(err, ErrEncryptionKeyMismatch) {
		t.Fatalf("corruption misclassified: %v", err)
	}
}

func TestOpenEncryptedVerifiesKeyAndReleasesFailedOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault's data.duckdb")
	key := strings.Repeat("12", 32)
	db, err := OpenEncrypted(path, key)
	if err != nil {
		t.Fatal(err)
	}
	var providerLoaded, unsafeCrypto bool
	if err := db.QueryRow("SELECT loaded FROM duckdb_extensions() WHERE extension_name = 'httpfs'").Scan(&providerLoaded); err != nil || !providerLoaded {
		_ = db.Close()
		t.Fatalf("secure crypto provider loaded=%v, err=%v", providerLoaded, err)
	}
	if err := db.QueryRow("SELECT current_setting('force_mbedtls_unsafe')").Scan(&unsafeCrypto); err != nil || unsafeCrypto {
		_ = db.Close()
		t.Fatalf("unsafe crypto enabled=%v, err=%v", unsafeCrypto, err)
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
	if !errors.Is(err, ErrEncryptionKeyMismatch) {
		t.Fatalf("wrong key must return a recoverable key error: %v", err)
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
