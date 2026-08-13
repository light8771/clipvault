package clipvault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	storageLocationFile = "storage-location.json"
	storageMoveFile     = "storage-move.json"
)

type storageLocation struct {
	DataDir          string `json:"dataDir"`
	CleanupSourceDir string `json:"cleanupSourceDir,omitempty"`
}

type storageMove struct {
	SourceDir       string `json:"sourceDir"`
	TargetDir       string `json:"targetDir"`
	SourceKeyDigest string `json:"sourceKeyDigest"`
}

var storageMoveMu sync.Mutex

// StorageStatus describes the active data directory and whether it can be
// relocated through the settings page.
type StorageStatus struct {
	DataDir string `json:"dataDir"`
	LogDir  string `json:"logDir"`
	Movable bool   `json:"movable"`
	Pending bool   `json:"pending"`
	Reason  string `json:"reason,omitempty"`
}

func (env Environment) StorageStatus() StorageStatus {
	status := StorageStatus{DataDir: env.DataDir, LogDir: env.LogDirectory(), Movable: true}
	switch env.Source {
	case "portable":
		status.Movable = false
		status.Reason = "便携模式的数据目录由 portable.flag 固定"
	case "environment", "command":
		status.Movable = false
		status.Reason = "数据目录由启动环境指定"
	}
	if status.Movable {
		_, err := os.Stat(filepath.Join(environmentControlDir(), storageMoveFile))
		status.Pending = err == nil
	}
	return status
}

// prepareEnvironmentForRun applies a pending relocation only after the main
// process holds the single-instance lock. CLI helper commands must not move
// data while the tray application may still have DuckDB files open.
func prepareEnvironmentForRun(env Environment) (Environment, string, error) {
	if !env.StorageStatus().Movable {
		return env, "", nil
	}
	controlDir := environmentControlDir()
	warning, err := applyPendingDataDirectoryMove(controlDir)
	if err != nil {
		return Environment{}, "", err
	}
	configuredDir, err := configuredDataDirectory(controlDir)
	if err != nil {
		return Environment{}, "", err
	}
	if configuredDir != "" {
		env.DataDir = configuredDir
		env.Source = "configured"
	}
	return env, warning, nil
}

// ScheduleDataDirectoryMove validates a user-provided existing directory and
// writes a pending move record. The move itself happens before the next full
// application startup, after DuckDB has released its files.
func ScheduleDataDirectoryMove(env Environment, target string) error {
	if !env.StorageStatus().Movable {
		return fmt.Errorf("current run mode does not support data directory relocation")
	}
	storageMoveMu.Lock()
	defer storageMoveMu.Unlock()

	targetDir, err := resolveExistingDirectory(target)
	if err != nil {
		return err
	}
	sourceDir, err := resolveExistingDirectory(env.DataDir)
	if err != nil {
		return fmt.Errorf("current data directory is unavailable: %w", err)
	}
	if samePath(sourceDir, targetDir) {
		return fmt.Errorf("new directory is the same as current data directory")
	}
	if pathContains(sourceDir, targetDir) || pathContains(targetDir, sourceDir) {
		return fmt.Errorf("new directory must not be a parent or child of current data directory")
	}
	controlDir := environmentControlDir()
	if err := validateDataDirectoryMoveTarget(targetDir, controlDir); err != nil {
		return err
	}
	if err := verifyDirectoryWritable(targetDir); err != nil {
		return err
	}

	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		return fmt.Errorf("prepare data directory configuration failed: %w", err)
	}
	pendingPath := filepath.Join(controlDir, storageMoveFile)
	if _, err := os.Stat(pendingPath); err == nil {
		return fmt.Errorf("a data directory relocation is already pending")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read relocation task failed: %w", err)
	}
	sourceDigest, err := storageKeyDigest(sourceDir)
	if err != nil {
		return fmt.Errorf("read current data directory identity failed: %w", err)
	}
	data, err := json.Marshal(storageMove{SourceDir: sourceDir, TargetDir: targetDir, SourceKeyDigest: sourceDigest})
	if err != nil {
		return fmt.Errorf("encode relocation task failed: %w", err)
	}
	if _, err := atomicWriteStorageControlFile(pendingPath, data); err != nil {
		return fmt.Errorf("save relocation task failed: %w", err)
	}
	return nil
}

func resolveExistingDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("请选择数据目录")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("数据目录必须使用绝对路径")
	}
	abs := filepath.Clean(path)
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("目录不存在；请先在系统中创建: %w", os.ErrNotExist)
		}
		return "", fmt.Errorf("访问目录失败: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("指定路径不是目录")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("解析目录失败: %w", err)
	}
	if !strings.EqualFold(filepath.Clean(resolved), abs) {
		return "", fmt.Errorf("数据目录不能是符号链接或联接点")
	}
	return abs, nil
}

func verifyDirectoryWritable(dir string) error {
	probe := filepath.Join(dir, fmt.Sprintf(".clipvault-write-check-%d", time.Now().UnixNano()))
	if err := verifyFileRoundTrip(probe); err != nil {
		return err
	}

	probeDir := probe + "-dir"
	if err := os.Mkdir(probeDir, 0o700); err != nil {
		return fmt.Errorf("目录创建子目录检查失败: %w", err)
	}
	defer os.RemoveAll(probeDir)
	if err := verifyFileRoundTrip(filepath.Join(probeDir, "probe")); err != nil {
		return err
	}
	if err := os.Remove(probeDir); err != nil {
		return fmt.Errorf("目录删除子目录检查失败: %w", err)
	}
	return nil
}

func verifyFileRoundTrip(probe string) error {
	const probeData = "clipvault"
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("目录不可写，请检查权限: %w", err)
	}
	defer os.Remove(probe)
	if _, err := f.WriteString(probeData); err != nil {
		_ = f.Close()
		return fmt.Errorf("目录写入检查失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("目录写入检查失败: %w", err)
	}
	data, err := os.ReadFile(probe)
	if err != nil {
		return fmt.Errorf("目录读取检查失败: %w", err)
	}
	if string(data) != probeData {
		return fmt.Errorf("目录读写检查数据不一致")
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("目录删除检查失败: %w", err)
	}
	return nil
}

func validateDataDirectoryMoveTarget(targetDir, controlDir string) error {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return fmt.Errorf("读取目标目录失败: %w", err)
	}
	for _, entry := range entries {
		if samePath(targetDir, controlDir) && isManagedControlEntry(entry) {
			continue
		}
		return fmt.Errorf("目标目录必须为空")
	}
	return nil
}

func isManagedControlEntry(entry os.DirEntry) bool {
	if entry.Type()&os.ModeSymlink != 0 {
		return false
	}
	switch entry.Name() {
	case storageLocationFile, storageMoveFile, "startup-error.log":
		return entry.Type().IsRegular()
	case "logs":
		return entry.IsDir()
	default:
		return false
	}
}

func samePath(a, b string) bool {
	return strings.EqualFold(canonicalPath(a), canonicalPath(b))
}

func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func environmentControlDir() string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, appDirName)
	}
	if roaming := os.Getenv("APPDATA"); roaming != "" {
		return filepath.Join(roaming, appDirName)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, "AppData", "Roaming", appDirName)
	}
	return appDirName
}

func configuredDataDirectory(controlDir string) (string, error) {
	location, exists, err := configuredStorageLocation(controlDir)
	if err != nil || !exists {
		return "", err
	}
	return location.DataDir, nil
}

func configuredStorageLocation(controlDir string) (storageLocation, bool, error) {
	data, err := os.ReadFile(filepath.Join(controlDir, storageLocationFile))
	if os.IsNotExist(err) {
		return storageLocation{}, false, nil
	}
	if err != nil {
		return storageLocation{}, false, fmt.Errorf("读取数据目录配置失败: %w", err)
	}
	var location storageLocation
	if err := decodeStrictJSON(data, &location); err != nil {
		return storageLocation{}, false, fmt.Errorf("解析数据目录配置失败: %w", err)
	}
	dataDir, err := resolveAbsoluteDataDirectory(location.DataDir, "configured data directory")
	if err != nil {
		return storageLocation{}, false, err
	}
	location.DataDir, err = resolveExistingDirectory(dataDir)
	if err != nil {
		return storageLocation{}, false, err
	}
	return location, true, nil
}
