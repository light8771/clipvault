package clipvault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AppState 对应 clipvault.state 文件，记录数据库引擎和密钥指纹等元数据。
// 不含任何敏感数据（无 key、无密码、无正文）。
type AppState struct {
	Engine       string `json:"engine"`        // "duckdb" 或 "sqlite"
	KeyFingerprint string `json:"keyFingerprint"` // db_key 的 SHA-256 前 8 字节 hex
	HasRecovery  bool   `json:"hasRecovery"`   // 是否已设置恢复密码
	CreatedAt    string `json:"createdAt"`     // ISO 8601
	LastOpenedAt string `json:"lastOpenedAt"`  // ISO 8601
}

const stateFileName = "clipvault.state"

// StateFilePath 返回 dataDir 下的 state 文件路径。
func StateFilePath(dataDir string) string {
	return filepath.Join(dataDir, stateFileName)
}

// WriteStateFile 原子写入 state 文件。
func WriteStateFile(dataDir string, state AppState) error {
	state.LastOpenedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return atomicWriteFile(StateFilePath(dataDir), data)
}

// ReadStateFile 读取 state 文件。
func ReadStateFile(dataDir string) (AppState, error) {
	data, err := os.ReadFile(StateFilePath(dataDir))
	if err != nil {
		return AppState{}, fmt.Errorf("read state file: %w", err)
	}
	var state AppState
	if err := json.Unmarshal(data, &state); err != nil {
		return AppState{}, fmt.Errorf("unmarshal state: %w", err)
	}
	return state, nil
}

// StateExists 检查 state 文件是否存在。
func StateExists(dataDir string) bool {
	_, err := os.Stat(StateFilePath(dataDir))
	return err == nil
}
