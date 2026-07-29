package clipvault

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	storageLocationFile = "storage-location.json"
	storageMoveFile     = "storage-move.json"
	storageKeyFileName  = "clipvault.key"
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
	if err := json.Unmarshal(data, &location); err != nil {
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

// applyPendingDataDirectoryMove is a two-stage relocation. Once the location
// pointer is written, the target directory is authoritative; old-directory
// cleanup may be retried later without blocking startup.
func applyPendingDataDirectoryMove(controlDir string) (string, error) {
	path := filepath.Join(controlDir, storageMoveFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read data directory relocation task failed: %w", err)
	}
	var move storageMove
	if err := json.Unmarshal(data, &move); err != nil {
		return "", fmt.Errorf("parse data directory relocation task failed: %w", err)
	}
	sourcePath, err := resolveAbsoluteDataDirectory(move.SourceDir, "storage move source directory")
	if err != nil {
		return "", err
	}
	targetPath, err := resolveAbsoluteDataDirectory(move.TargetDir, "storage move target directory")
	if err != nil {
		return "", err
	}
	targetDir, err := resolveExistingDirectory(targetPath)
	if err != nil {
		if sourceDir, sourceErr := resolveExistingDirectory(sourcePath); sourceErr == nil {
			if verifyErr := verifyStorageKeyDigest(sourceDir, move.SourceKeyDigest); verifyErr != nil {
				return "", verifyErr
			}
		}
		return cancelDataDirectoryMove(path, fmt.Sprintf("target directory is unavailable; source data was not changed: %v", err))
	}
	location, hasLocation, err := configuredStorageLocation(controlDir)
	if err != nil {
		return "", err
	}
	if hasLocation && samePath(location.DataDir, targetDir) {
		if err := syncStorageControlFile(filepath.Join(controlDir, storageLocationFile)); err != nil {
			return "", fmt.Errorf("sync data directory location failed; source data is retained and relocation remains pending: %w", err)
		}
		cleanupSourcePath, err := resolveAbsoluteDataDirectory(location.CleanupSourceDir, "storage move cleanup source directory")
		if err != nil {
			return "", fmt.Errorf("committed relocation state is invalid; source data is retained and relocation remains pending: %w", err)
		}
		if !samePath(cleanupSourcePath, sourcePath) {
			return "", fmt.Errorf("committed relocation state does not match pending relocation; source data is retained and relocation remains pending")
		}
		sourceDir, err := resolveExistingDirectory(cleanupSourcePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return completeDataDirectoryMove(targetDir, controlDir, path, "new directory is active; source directory no longer exists")
			}
			return fmt.Sprintf("new directory is active, but source cleanup remains pending: %v", err), nil
		}
		if err := verifyStorageKeyDigest(sourceDir, move.SourceKeyDigest); err != nil {
			return "", err
		}
		return finalizeDataDirectoryMove(sourceDir, targetDir, controlDir, path)
	}
	sourceDir, err := resolveExistingDirectory(sourcePath)
	if err != nil {
		return "", fmt.Errorf("relocation source directory is unavailable: %w", err)
	}
	if err := verifyStorageKeyDigest(sourceDir, move.SourceKeyDigest); err != nil {
		return "", err
	}
	if samePath(sourceDir, targetDir) || pathContains(sourceDir, targetDir) || pathContains(targetDir, sourceDir) {
		return "", fmt.Errorf("relocation directory relationship is invalid")
	}
	if err := validateDataDirectoryMoveTarget(targetDir, controlDir); err != nil {
		return cancelDataDirectoryMove(path, "target directory contains content; source data was not changed")
	}
	if err := copyDataDirectory(sourceDir, targetDir, controlDir); err != nil {
		return cancelDataDirectoryMove(path, fmt.Sprintf("copy data failed; target directory is retained to avoid accidental deletion: %v", err))
	}
	locationData, err := json.Marshal(storageLocation{DataDir: targetDir, CleanupSourceDir: sourceDir})
	if err != nil {
		return "", fmt.Errorf("encode data directory location failed: %w", err)
	}
	written, err := atomicWriteStorageControlFile(filepath.Join(controlDir, storageLocationFile), locationData)
	if err != nil {
		if written {
			return "", fmt.Errorf("save data directory location sync failed after commit; source data is retained and relocation remains pending: %w", err)
		}
		return cancelDataDirectoryMove(path, fmt.Sprintf("save data directory location failed; source data was not changed: %v", err))
	}
	return finalizeDataDirectoryMove(sourceDir, targetDir, controlDir, path)
}

func cancelDataDirectoryMove(pendingPath, reason string) (string, error) {
	if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("取消数据目录迁移失败: %w", err)
	}
	return "数据目录迁移已取消：" + reason, nil
}

func finalizeDataDirectoryMove(sourceDir, targetDir, controlDir, pendingPath string) (string, error) {
	if err := removeMovedData(sourceDir, controlDir); err != nil {
		return fmt.Sprintf("数据目录已迁移，但清理原目录失败: %v", err), nil
	}
	return completeDataDirectoryMove(targetDir, controlDir, pendingPath, "")
}

func completeDataDirectoryMove(targetDir, controlDir, pendingPath, warning string) (string, error) {
	if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
		return fmt.Sprintf("数据目录已迁移，但清理迁移任务失败: %v", err), nil
	}
	locationData, err := json.Marshal(storageLocation{DataDir: targetDir})
	if err != nil {
		return "", fmt.Errorf("编码数据目录配置失败: %w", err)
	}
	if _, err := atomicWriteStorageControlFile(filepath.Join(controlDir, storageLocationFile), locationData); err != nil {
		return fmt.Sprintf("数据目录已迁移，但清理迁移状态失败: %v", err), nil
	}
	return warning, nil
}

func copyDataDirectory(sourceDir, targetDir, controlDir string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if samePath(sourceDir, controlDir) && isControlFile(entry.Name()) {
			continue
		}
		if err := copyStorageEntry(filepath.Join(sourceDir, entry.Name()), filepath.Join(targetDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func removeMovedData(sourceDir, controlDir string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return err
	}
	removeKey := false
	for _, entry := range entries {
		if samePath(sourceDir, controlDir) && isControlFile(entry.Name()) {
			continue
		}
		if entry.Name() == storageKeyFileName {
			removeKey = true
			continue
		}
		if err := os.RemoveAll(filepath.Join(sourceDir, entry.Name())); err != nil {
			return err
		}
	}
	if removeKey {
		if err := os.Remove(filepath.Join(sourceDir, storageKeyFileName)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func isControlFile(name string) bool {
	return name == storageLocationFile || name == storageMoveFile || name == "logs" || name == "startup-error.log"
}

func copyStorageEntry(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("数据目录不支持链接项: %s", source)
	}
	if info.IsDir() {
		if err := os.Mkdir(target, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyStorageEntry(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	from, err := os.Open(source)
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(to, from)
	if copyErr == nil {
		copyErr = to.Sync()
	}
	closeErr := to.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func storageKeyDigest(dir string) (string, error) {
	path := filepath.Join(dir, storageKeyFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read source identity file failed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("source identity file must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read source identity file failed: %w", err)
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", fmt.Errorf("read source identity file failed: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func verifyStorageKeyDigest(dir, expected string) error {
	if strings.TrimSpace(expected) == "" {
		return fmt.Errorf("relocation task is missing source identity digest")
	}
	actual, err := storageKeyDigest(dir)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("source directory identity changed")
	}
	return nil
}

// atomicWriteStorageControlFile flushes a control file before atomically
// replacing it. written reports whether the replacement completed, even if
// the final synchronization fails.
func atomicWriteStorageControlFile(path string, data []byte) (bool, error) {
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return false, fmt.Errorf("write tmp: %w", err)
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}()
	if _, err := file.Write(data); err != nil {
		return false, fmt.Errorf("write tmp: %w", err)
	}
	if err := file.Sync(); err != nil {
		return false, fmt.Errorf("sync tmp: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, fmt.Errorf("rename: %w", err)
	}
	if err := syncStorageControlFile(path); err != nil {
		return true, fmt.Errorf("sync saved file: %w", err)
	}
	return true, nil
}

func syncStorageControlFile(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
