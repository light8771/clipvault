package clipvault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateFileRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()

	// 不存在时应返回 false
	if StateExists(tmpDir) {
		t.Fatalf("state should not exist yet")
	}

	// 写入
	state := AppState{
		Engine:         "duckdb",
		KeyFingerprint: "abcd1234ef5678ab",
		CreatedAt:      "2026-07-09T10:00:00Z",
	}
	if err := WriteStateFile(tmpDir, state); err != nil {
		t.Fatalf("WriteStateFile: %v", err)
	}

	// 存在
	if !StateExists(tmpDir) {
		t.Fatalf("state should exist now")
	}

	// 读取
	read, err := ReadStateFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if read.Engine != "duckdb" {
		t.Fatalf("engine mismatch: %q", read.Engine)
	}
	if read.KeyFingerprint != "abcd1234ef5678ab" {
		t.Fatalf("fingerprint mismatch: %q", read.KeyFingerprint)
	}
	if read.LastOpenedAt == "" {
		t.Fatalf("lastOpenedAt should be set")
	}

	// 确认文件名
	if _, err := os.Stat(filepath.Join(tmpDir, "clipvault.state")); err != nil {
		t.Fatalf("state file not at expected path: %v", err)
	}
}
