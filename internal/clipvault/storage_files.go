package clipvault

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func copyStorageEntry(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("data directory must not contain links: %s", source)
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
	path := activeDatabaseFile(dir, databaseKeyFileName)
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
