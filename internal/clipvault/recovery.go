package clipvault

import (
	"encoding/hex"
	"errors"
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
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := WriteStateFile(dataDir, state); err != nil {
		return fmt.Errorf("update state file: %w", err)
	}

	return nil
}

// CreateNewDatabase isolates old files and creates a fresh encrypted database.
// Any failure restores the original file set, so the recovery UI can safely retry.
func CreateNewDatabase(dataDir string) (err error) {
	isolation, err := isolateDatabaseFiles(dataDir)
	if err != nil {
		return err
	}
	keyPath := filepath.Join(dataDir, "clipvault.key")
	dbPath := filepath.Join(dataDir, "clipvault.duckdb")
	defer func() {
		if err != nil {
			err = errors.Join(err, removeNewStoreFiles(keyPath, dbPath), restoreDatabaseFiles(isolation))
		}
	}()

	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		return fmt.Errorf("create new store: %w", err)
	}
	fp := KeyFingerprint(store.dbKey)
	if closeErr := store.Close(); closeErr != nil {
		return fmt.Errorf("close new store: %w", closeErr)
	}

	// Write state file
	state := AppState{
		Engine:         "duckdb",
		KeyFingerprint: hex.EncodeToString(fp),
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := WriteStateFile(dataDir, state); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}

	return nil
}

type isolatedDatabaseFile struct {
	original string
	locked   string
}

func isolateDatabaseFiles(dataDir string) ([]isolatedDatabaseFile, error) {
	suffix := ".locked-" + time.Now().UTC().Format("20060102-150405.000000000")
	names := []string{"clipvault.duckdb", "clipvault.duckdb.wal", "clipvault.key", "clipvault.recovery", "clipvault.state"}
	files := make([]isolatedDatabaseFile, 0, len(names))
	for _, name := range names {
		original := filepath.Join(dataDir, name)
		exists, err := pathExists(original)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", name, err)
		}
		if !exists {
			continue
		}
		locked := original + suffix
		lockedExists, err := pathExists(locked)
		if err != nil {
			return nil, fmt.Errorf("inspect isolation target for %s: %w", name, err)
		}
		if lockedExists {
			return nil, fmt.Errorf("isolation target already exists for %s", name)
		}
		files = append(files, isolatedDatabaseFile{original: original, locked: locked})
	}

	for i, file := range files {
		if err := os.Rename(file.original, file.locked); err != nil {
			return nil, errors.Join(fmt.Errorf("isolate %s: %w", filepath.Base(file.original), err), restoreDatabaseFiles(files[:i]))
		}
	}
	return files, nil
}

func restoreDatabaseFiles(files []isolatedDatabaseFile) error {
	var restoreErrors []error
	for i := len(files) - 1; i >= 0; i-- {
		file := files[i]
		if err := os.Rename(file.locked, file.original); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("restore %s: %w", filepath.Base(file.original), err))
		}
	}
	return errors.Join(restoreErrors...)
}

// verifyDuckDBKey tries to open the DuckDB file with the given key to verify correctness.
func verifyDuckDBKey(dbPath, keyHex string) error {
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("database file unavailable: %w", err)
	}
	return verifyDuckDBKeyInternal(dbPath, keyHex)
}
