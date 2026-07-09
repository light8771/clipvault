package clipvault

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RecoverWithPassword attempts to recover the database key using the recovery password.
// It reads the recovery file, decrypts the db_key, verifies it against the DuckDB file,
// rewrites the DPAPI key file, and updates the state file.
func RecoverWithPassword(dataDir, password string) error {
	recPath := filepath.Join(dataDir, "clipvault.recovery")
	keyPath := filepath.Join(dataDir, "clipvault.key")
	dbPath := filepath.Join(dataDir, "clipvault.duckdb")

	// 1. Read recovery file and decrypt db_key
	dbKey, fingerprint, err := ReadRecoveryFile(recPath, []byte(password))
	if err != nil {
		// Add brief delay to slow brute-force attempts
		time.Sleep(500 * time.Millisecond)
		return fmt.Errorf("recovery decrypt failed: %w", err)
	}

	// 2. Verify the key works by opening DuckDB
	keyHex := hex.EncodeToString(dbKey)
	if err := verifyDuckDBKey(dbPath, keyHex); err != nil {
		return fmt.Errorf("key verification failed: %w", err)
	}

	// 3. Rewrite DPAPI key file
	if err := WriteDPAPIKeyFile(keyPath, dbKey); err != nil {
		return fmt.Errorf("rewrite DPAPI key file: %w", err)
	}

	// 4. Update state file
	state := AppState{
		Engine:         "duckdb",
		KeyFingerprint: hex.EncodeToString(fingerprint),
		HasRecovery:    true,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := WriteStateFile(dataDir, state); err != nil {
		return fmt.Errorf("update state file: %w", err)
	}

	return nil
}

// CreateNewDatabase isolates old files and creates a fresh encrypted database.
// Old files are renamed with a .locked-YYYYMMDD-HHMMSS suffix.
func CreateNewDatabase(dataDir string) error {
	ts := time.Now().Format("20060102-150405")

	// Isolate old files
	oldFiles := []string{"clipvault.duckdb", "clipvault.duckdb.wal", "clipvault.key", "clipvault.recovery", "clipvault.state"}
	for _, name := range oldFiles {
		oldPath := filepath.Join(dataDir, name)
		if _, err := os.Stat(oldPath); err == nil {
			lockedPath := filepath.Join(dataDir, name+".locked-"+ts)
			if err := os.Rename(oldPath, lockedPath); err != nil {
				return fmt.Errorf("isolate %s: %w", name, err)
			}
		}
	}

	// Create new database via OpenStore (first-run path)
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		return fmt.Errorf("create new store: %w", err)
	}
	defer store.Close()

	// Write state file
	fp := KeyFingerprint(store.dbKey)
	state := AppState{
		Engine:         "duckdb",
		KeyFingerprint: hex.EncodeToString(fp),
		HasRecovery:    false,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := WriteStateFile(dataDir, state); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}

	return nil
}

// verifyDuckDBKey tries to open the DuckDB file with the given key to verify correctness.
func verifyDuckDBKey(dbPath, keyHex string) error {
	// If db file doesn't exist yet, skip verification
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}

	// Use the same ATTACH approach as OpenStore
	// We import database/sql here to avoid circular dependency issues
	return verifyDuckDBKeyInternal(dbPath, keyHex)
}
