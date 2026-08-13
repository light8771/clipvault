package clipvault

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// AppState 对应 clipvault.state 文件，记录数据库引擎和密钥指纹等元数据。
// 不含任何敏感数据（无 key、无密码、无正文）。
type AppState struct {
	Engine         string `json:"engine"`         // "duckdb" 或 "sqlite"
	KeyFingerprint string `json:"keyFingerprint"` // db_key 的 SHA-256 前 8 字节 hex
	CreatedAt      string `json:"createdAt"`      // ISO 8601
	LastOpenedAt   string `json:"lastOpenedAt"`   // ISO 8601
}

// StateFilePath 返回活动数据库的 state 文件路径。
func StateFilePath(dataDir string) string {
	return activeDatabaseFile(dataDir, databaseStateFileName)
}

// WriteStateFile 原子写入 state 文件。
func WriteStateFile(dataDir string, state AppState) error {
	return writeDatabaseStateFile(activeDatabaseDirectory(dataDir), state)
}

func writeDatabaseStateFile(databaseDir string, state AppState) error {
	if err := os.MkdirAll(databaseDir, 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	state.LastOpenedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return atomicWriteFile(databaseGroupFile(databaseDir, databaseStateFileName), data)
}

// ReadStateFile 读取 state 文件。
func ReadStateFile(dataDir string) (AppState, error) {
	data, err := os.ReadFile(StateFilePath(dataDir))
	if err != nil {
		return AppState{}, fmt.Errorf("read state file: %w", err)
	}
	var state AppState
	if err := decodeStrictJSON(data, &state); err != nil {
		return AppState{}, fmt.Errorf("unmarshal state: %w", err)
	}
	return state, nil
}

// StateExists 检查 state 文件是否存在。
func StateExists(dataDir string) bool {
	_, err := os.Stat(StateFilePath(dataDir))
	return err == nil
}
