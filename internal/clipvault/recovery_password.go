package clipvault

import (
	"fmt"
	"os"
	"path/filepath"
	"unicode"
)

// RecoveryPasswordError describes a recovery password operation failure.
type RecoveryPasswordError struct {
	Code string // "too_short", "too_weak", "wrong_password", "not_set"
	Err  error
}

func (e *RecoveryPasswordError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Code
}

func (e *RecoveryPasswordError) Unwrap() error { return e.Err }

// CheckPasswordStrength validates that a password meets minimum strength requirements.
// Requirements: at least 8 characters, must contain characters from at least 2 of:
// uppercase, lowercase, digit, special.
func CheckPasswordStrength(password string) error {
	if len(password) < 8 {
		return &RecoveryPasswordError{Code: "too_short"}
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	categories := 0
	if hasUpper {
		categories++
	}
	if hasLower {
		categories++
	}
	if hasDigit {
		categories++
	}
	if hasSpecial {
		categories++
	}

	if categories < 2 {
		return &RecoveryPasswordError{Code: "too_weak"}
	}
	return nil
}

// HasRecoveryPassword checks if a recovery file exists in the data directory.
func HasRecoveryPassword(dataDir string) bool {
	recPath := filepath.Join(dataDir, "clipvault.recovery")
	_, err := os.Stat(recPath)
	return err == nil
}

// SetRecoveryPassword creates or overwrites the recovery file with the given password.
// Also updates the state file to mark HasRecovery = true.
// Requires the current dbKey (from Store.DBKey()).
func SetRecoveryPassword(dataDir string, dbKey []byte, password string) error {
	if err := CheckPasswordStrength(password); err != nil {
		return err
	}

	recPath := filepath.Join(dataDir, "clipvault.recovery")
	if err := WriteRecoveryFile(recPath, dbKey, []byte(password)); err != nil {
		return fmt.Errorf("write recovery file: %w", err)
	}

	// Self-test: verify the password can decrypt the key
	if err := VerifyRecoveryPassword(dataDir, password); err != nil {
		// Remove the bad recovery file
		_ = os.Remove(recPath)
		return fmt.Errorf("self-test failed: %w", err)
	}

	// Update state file
	return updateStateRecoveryFlag(dataDir, true)
}

// ChangeRecoveryPassword verifies the old password, then sets a new one.
func ChangeRecoveryPassword(dataDir string, dbKey []byte, oldPassword, newPassword string) error {
	if err := CheckPasswordStrength(newPassword); err != nil {
		return err
	}

	recPath := filepath.Join(dataDir, "clipvault.recovery")
	if _, err := os.Stat(recPath); os.IsNotExist(err) {
		return &RecoveryPasswordError{Code: "not_set"}
	}

	// Verify old password
	if err := VerifyRecoveryPassword(dataDir, oldPassword); err != nil {
		return &RecoveryPasswordError{Code: "wrong_password", Err: err}
	}

	// Write new recovery file
	if err := WriteRecoveryFile(recPath, dbKey, []byte(newPassword)); err != nil {
		return fmt.Errorf("write recovery file: %w", err)
	}

	// Self-test
	if err := VerifyRecoveryPassword(dataDir, newPassword); err != nil {
		_ = os.Remove(recPath)
		return fmt.Errorf("self-test failed: %w", err)
	}

	return nil
}

// RemoveRecoveryPassword deletes the recovery file and updates the state file.
func RemoveRecoveryPassword(dataDir string) error {
	recPath := filepath.Join(dataDir, "clipvault.recovery")
	if err := os.Remove(recPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove recovery file: %w", err)
	}
	return updateStateRecoveryFlag(dataDir, false)
}

// VerifyRecoveryPassword tests that the password can decrypt the db_key from the recovery file.
func VerifyRecoveryPassword(dataDir, password string) error {
	recPath := filepath.Join(dataDir, "clipvault.recovery")
	_, _, err := ReadRecoveryFile(recPath, []byte(password))
	if err != nil {
		return fmt.Errorf("verify recovery: %w", err)
	}
	return nil
}

// updateStateRecoveryFlag updates the HasRecovery field in the state file.
func updateStateRecoveryFlag(dataDir string, hasRecovery bool) error {
	state, err := ReadStateFile(dataDir)
	if err != nil {
		// If state file doesn't exist, create a minimal one
		state = AppState{
			Engine: "duckdb",
		}
	}
	state.HasRecovery = hasRecovery
	return WriteStateFile(dataDir, state)
}
