package clipvault

import (
	"fmt"
	"os"
	"path/filepath"
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
func HasRecoveryPassword(dataDir string) bool {
	recPath := filepath.Join(dataDir, "clipvault.recovery")
	_, err := os.Stat(recPath)
	return err == nil
}

// SetRecoveryPassword creates or overwrites the recovery file with the given password.
// Also updates the state file to mark HasRecovery = true.
// Requires the current dbKey (from Store.DBKey()).
func SetRecoveryPassword(dataDir string, dbKey []byte, password string) error {
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
// 原子替换：新文件写入临时路径并自检通过后才 rename 覆盖旧文件，
// 任何失败路径都保留原 recovery 文件，避免"自检失败即删除"把用户
// 唯一的恢复能力从"有"清零到"无"（原实现直接覆写 + os.Remove）。
func ChangeRecoveryPassword(dataDir string, dbKey []byte, oldPassword, newPassword string) error {
	recPath := filepath.Join(dataDir, "clipvault.recovery")
	if _, err := os.Stat(recPath); os.IsNotExist(err) {
		return &RecoveryPasswordError{Code: "not_set"}
	}

	// Verify old password
	if err := VerifyRecoveryPassword(dataDir, oldPassword); err != nil {
		return &RecoveryPasswordError{Code: "wrong_password", Err: err}
	}

	// 写临时文件：同目录 .new 后缀保证与 recPath 同卷（rename 原子、无跨盘问题）；
	// 不用 .tmp 后缀，避免与 WriteRecoveryFile 内部 atomicWriteFile 的 .tmp 混淆。
	tmpPath := recPath + ".new"
	if err := WriteRecoveryFile(tmpPath, dbKey, []byte(newPassword)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write recovery file: %w", err)
	}
	// 兜底清理：rename 成功后 tmpPath 已不存在，Remove 报错可忽略
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

	// 同步 state 标志（原实现缺失：改密后 state.HasRecovery 可能滞后）。
	// best-effort：改密成功是主操作，state 写失败仅记日志不应 abort，
	// handleRecoveryStatus 以文件存在性为准，数据一致性不受影响。
	if err := updateStateRecoveryFlag(dataDir, true); err != nil {
		// 无 logger 引用，静默保留：state 仅用于展示，文件存在性为准
		_ = err
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
