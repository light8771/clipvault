package clipvault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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
	if err := decodeStrictJSON(data, &move); err != nil {
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
	canonicalSource, err := canonicalPathChecked(sourceDir)
	if err != nil {
		return "", err
	}
	canonicalTarget, err := canonicalPathChecked(targetDir)
	if err != nil {
		return "", err
	}
	if samePath(sourceDir, targetDir) || pathContains(canonicalSource, canonicalTarget) || pathContains(canonicalTarget, canonicalSource) {
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
		return "", fmt.Errorf("%s: %w", templateT("storage.err_cancel_move", nil), err)
	}
	return templateT("storage.move_cancelled", map[string]any{"reason": reason}), nil
}

func finalizeDataDirectoryMove(sourceDir, targetDir, controlDir, pendingPath string) (string, error) {
	if err := removeMovedData(sourceDir, controlDir); err != nil {
		return templateT("storage.move_ok_cleanup_failed", map[string]any{"error": err.Error()}), nil
	}
	return completeDataDirectoryMove(targetDir, controlDir, pendingPath, "")
}

func completeDataDirectoryMove(targetDir, controlDir, pendingPath, warning string) (string, error) {
	if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
		return templateT("storage.move_ok_task_cleanup_failed", map[string]any{"error": err.Error()}), nil
	}
	locationData, err := json.Marshal(storageLocation{DataDir: targetDir})
	if err != nil {
		return "", fmt.Errorf("%s: %w", templateT("storage.err_encode_config", nil), err)
	}
	if _, err := atomicWriteStorageControlFile(filepath.Join(controlDir, storageLocationFile), locationData); err != nil {
		return templateT("storage.move_ok_state_cleanup_failed", map[string]any{"error": err.Error()}), nil
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
	removeDatabase := false
	for _, entry := range entries {
		if samePath(sourceDir, controlDir) && isControlFile(entry.Name()) {
			continue
		}
		if entry.Name() == activeDatabaseDirectoryName {
			removeDatabase = true
			continue
		}
		if err := os.RemoveAll(filepath.Join(sourceDir, entry.Name())); err != nil {
			return err
		}
	}
	if removeDatabase {
		if err := os.RemoveAll(activeDatabaseDirectory(sourceDir)); err != nil {
			return err
		}
	}
	return nil
}

func isControlFile(name string) bool {
	return name == storageLocationFile || name == storageMoveFile || name == logLocationFile || name == "logs" || name == "startup-error.log"
}
