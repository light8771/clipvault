package clipvault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const logLocationFile = "log-location.json"

type logLocation struct {
	LogDir string `json:"logDir"`
}

var logLocationMu sync.Mutex

// configuredLogDirectory returns the startup-level log directory override.
// It deliberately lives outside the database because logging starts before the
// database and its settings table are opened.
func configuredLogDirectory(controlDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(controlDir, logLocationFile))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read log directory configuration: %w", err)
	}
	var location logLocation
	if err := decodeStrictJSON(data, &location); err != nil {
		return "", fmt.Errorf("parse log directory configuration: %w", err)
	}
	return resolveExistingLogDirectory(location.LogDir)
}

func resolveExistingLogDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New(templateT("storage.log_err_choose_dir", nil))
	}
	if !filepath.IsAbs(path) {
		return "", errors.New(templateT("storage.log_err_absolute_path", nil))
	}
	abs := filepath.Clean(path)
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s: %w", templateT("storage.err_dir_not_exist", nil), os.ErrNotExist)
		}
		return "", fmt.Errorf("%s: %w", templateT("storage.err_dir_access", nil), err)
	}
	if !info.IsDir() {
		return "", errors.New(templateT("storage.err_not_dir", nil))
	}
	linked, err := directoryPathHasLink(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", templateT("storage.err_resolve", nil), err)
	}
	if linked {
		return "", errors.New(templateT("storage.log_err_symlink", nil))
	}
	return abs, nil
}

// ConfigureLogDirectory validates and stores the directory used after the next
// restart. Existing logs are intentionally left in their current directory.
func ConfigureLogDirectory(env Environment, target string) error {
	if env.Portable {
		return errors.New(templateT("storage.log_reason_portable", nil))
	}
	logLocationMu.Lock()
	defer logLocationMu.Unlock()

	targetDir, err := resolveExistingLogDirectory(target)
	if err != nil {
		return err
	}
	if samePath(targetDir, env.LogDirectory()) {
		return nil
	}
	canonicalData, err := canonicalPathChecked(env.DataDir)
	if err != nil {
		return err
	}
	canonicalTarget, err := canonicalPathChecked(targetDir)
	if err != nil {
		return err
	}
	if pathContains(canonicalData, canonicalTarget) {
		return errors.New(templateT("storage.log_err_inside_data", nil))
	}
	if err := verifyDirectoryWritable(targetDir); err != nil {
		return err
	}
	data, err := json.Marshal(logLocation{LogDir: targetDir})
	if err != nil {
		return fmt.Errorf("encode log directory configuration: %w", err)
	}
	controlDir := environmentControlDir()
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		return fmt.Errorf("prepare log directory configuration: %w", err)
	}
	if _, err := atomicWriteStorageControlFile(filepath.Join(controlDir, logLocationFile), data); err != nil {
		return fmt.Errorf("save log directory configuration: %w", err)
	}
	return nil
}
