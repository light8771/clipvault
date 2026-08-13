package clipvault

import (
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// RecoverWithPassword attempts to recover the database key using the recovery password.
// It reads the recovery file, decrypts the db_key, verifies it against the DuckDB file,
// rewrites the DPAPI key file, and updates the state file.
func RecoverWithPassword(dataDir, password string) error {
	recPath := activeDatabaseFile(dataDir, databaseRecoveryFileName)
	keyPath := activeDatabaseFile(dataDir, databaseKeyFileName)
	dbPath := activeDatabaseFile(dataDir, databaseFileName)

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

// CreateNewDatabase keeps the unreadable database as one isolated directory
// and selects a newly created encrypted database directory.
func CreateNewDatabase(dataDir string) error {
	return createNewDatabaseGroup(dataDir)
}

// verifyDuckDBKey tries to open the DuckDB file with the given key to verify correctness.
func verifyDuckDBKey(dbPath, keyHex string) error {
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("database file unavailable: %w", err)
	}
	return verifyDuckDBKeyInternal(dbPath, keyHex)
}
