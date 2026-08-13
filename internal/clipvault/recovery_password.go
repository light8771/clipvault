package clipvault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	minRecoveryPasswordRunes = 12
	maxRecoveryPasswordBytes = 1024
)

// RecoveryPasswordError describes a recovery password operation failure.
type RecoveryPasswordError struct {
	Code string // "wrong_password", "not_set"
	Err  error
}

func (e *RecoveryPasswordError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Code
}

func (e *RecoveryPasswordError) Unwrap() error { return e.Err }

// HasRecoveryPassword checks if a recovery file exists in the data directory.
func HasRecoveryPassword(dataDir string) (bool, error) {
	recPath := activeDatabaseFile(dataDir, databaseRecoveryFileName)
	_, err := os.Stat(recPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat recovery file: %w", err)
}

func hasRecoveryEnvelopeForKey(dataDir string, dbKey []byte) (bool, error) {
	recPath := activeDatabaseFile(dataDir, databaseRecoveryFileName)
	fingerprint, err := readRecoveryFileFingerprint(recPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("validate recovery file: %w", err)
	}
	if !bytes.Equal(fingerprint, KeyFingerprint(dbKey)) {
		return false, errors.New("recovery file belongs to a different database key")
	}
	return true, nil
}

// SetRecoveryPassword creates the initial recovery file. Existing recovery
// material must be changed through ChangeRecoveryPassword or ResetRecoveryPassword.
// Requires the current dbKey (from Store.DBKey()).
func SetRecoveryPassword(dataDir string, dbKey []byte, password string) error {
	if err := validateRecoveryPassword(password); err != nil {
		return err
	}
	recPath := activeDatabaseFile(dataDir, databaseRecoveryFileName)
	if _, err := os.Stat(recPath); err == nil {
		return errors.New("recovery password is already set")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat recovery file: %w", err)
	}
	if err := WriteRecoveryFile(recPath, dbKey, []byte(password)); err != nil {
		return fmt.Errorf("write recovery file: %w", err)
	}

	// Self-test: verify the password can decrypt the key
	if err := VerifyRecoveryPassword(dataDir, password); err != nil {
		// Remove the bad recovery file
		_ = os.Remove(recPath)
		return fmt.Errorf("self-test failed: %w", err)
	}

	return nil
}

// ChangeRecoveryPassword verifies the old password, then sets a new one.
// 原子替换：新文件写入临时路径并自检通过后才 rename 覆盖旧文件，
// 任何失败路径都保留原 recovery 文件，避免"自检失败即删除"把用户
// 唯一的恢复能力从"有"清零到"无"（原实现直接覆写 + os.Remove）。
func ChangeRecoveryPassword(dataDir string, dbKey []byte, oldPassword, newPassword string) error {
	recPath := activeDatabaseFile(dataDir, databaseRecoveryFileName)
	if _, err := os.Stat(recPath); err != nil {
		if os.IsNotExist(err) {
			return &RecoveryPasswordError{Code: "not_set"}
		}
		return fmt.Errorf("stat recovery file: %w", err)
	}

	// Verify old password
	if err := VerifyRecoveryPassword(dataDir, oldPassword); err != nil {
		return &RecoveryPasswordError{Code: "wrong_password", Err: err}
	}
	return ResetRecoveryPassword(dataDir, dbKey, newPassword)
}

// ResetRecoveryPassword 使用当前已打开数据库的 dbKey 原子替换恢复文件。
// 调用方必须先完成一次“刚刚验证”的二次认证；该能力用于用户忘记旧恢复
// 密码、但 Windows DPAPI 仍能正常打开数据库时重新建立恢复能力。
func ResetRecoveryPassword(dataDir string, dbKey []byte, newPassword string) error {
	if err := validateRecoveryPassword(newPassword); err != nil {
		return err
	}
	recPath := activeDatabaseFile(dataDir, databaseRecoveryFileName)

	// 同目录临时文件保证 rename 不跨卷。
	tmpPath := recPath + ".new"
	if err := WriteRecoveryFile(tmpPath, dbKey, []byte(newPassword)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write recovery file: %w", err)
	}
	defer os.Remove(tmpPath)

	// 自检临时文件本身：VerifyRecoveryPassword 固定读 recPath（旧文件），
	// 不能用于验证新文件，必须直接读 tmpPath。
	if _, _, err := ReadRecoveryFile(tmpPath, []byte(newPassword)); err != nil {
		return fmt.Errorf("self-test failed: %w", err)
	}

	// 原子替换：此刻旧文件仍完整，rename 失败也不丢恢复能力
	if err := os.Rename(tmpPath, recPath); err != nil {
		return fmt.Errorf("rename recovery file: %w", err)
	}

	return nil
}

func validateRecoveryPassword(password string) error {
	if strings.TrimSpace(password) != password || utf8.RuneCountInString(password) < minRecoveryPasswordRunes {
		return &RecoveryPasswordError{Code: "weak_password", Err: fmt.Errorf("恢复密码至少需要 %d 个字符，且首尾不能有空格", minRecoveryPasswordRunes)}
	}
	if len(password) > maxRecoveryPasswordBytes {
		return &RecoveryPasswordError{Code: "weak_password", Err: fmt.Errorf("恢复密码过长")}
	}
	return nil
}

// VerifyRecoveryPassword tests that the password can decrypt the db_key from the recovery file.
func VerifyRecoveryPassword(dataDir, password string) error {
	recPath := activeDatabaseFile(dataDir, databaseRecoveryFileName)
	_, _, err := ReadRecoveryFile(recPath, []byte(password))
	if err != nil {
		return fmt.Errorf("verify recovery: %w", err)
	}
	return nil
}
