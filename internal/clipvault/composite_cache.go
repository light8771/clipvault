package clipvault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// File-based paste targets need materialized images. Deleting the last owning
// record also deletes its rebuildable restoration copy, never arbitrary paths.
func (s *Store) removeUnreferencedCompositeCache(ctx context.Context) error {
	dir := filepath.Join(s.dataDir, "restore-assets")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		id := strings.TrimSuffix(entry.Name(), extension)
		if entry.IsDir() || (extension != ".png" && extension != ".jpg" && extension != ".gif") || !validContentSHA256(id) {
			continue
		}
		var referenced bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM clipboard_items WHERE list_contains(json_extract_string(content_json, '$.assets[*].id'), ?))`, id).Scan(&referenced); err != nil {
			return err
		}
		if !referenced {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
