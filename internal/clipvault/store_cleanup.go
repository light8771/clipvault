package clipvault

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
)

// 历史清理（M1）：preview 与 delete 严格共用同一套条件构造，
// 保证「预览看到的数量 = 实际删除的数量」。

// validateCleanupOptions 校验清理条件。range 模式下界闭、上界开（Unix ms）。
func validateCleanupOptions(opts CleanupOptions) error {
	switch opts.Mode {
	case "all":
		if opts.FromMS != 0 || opts.ToExclusiveMS != 0 {
			return errors.New("cleanup mode all does not accept time bounds")
		}
	case "range":
		if opts.FromMS <= 0 || opts.ToExclusiveMS <= 0 {
			return errors.New("cleanup mode range requires from/to")
		}
		if opts.FromMS >= opts.ToExclusiveMS {
			return errors.New("cleanup range start must be before end")
		}
	default:
		return fmt.Errorf("unsupported cleanup mode %q", opts.Mode)
	}
	return nil
}

// cleanupWhere 构造 preview/delete 共用的 WHERE 子句（不含 " WHERE" 前缀）。
func cleanupWhere(opts CleanupOptions) (string, []any) {
	var conds []string
	var args []any
	if opts.Mode == "range" {
		conds = append(conds, "created_at >= ?", "created_at < ?")
		args = append(args, opts.FromMS, opts.ToExclusiveMS)
	}
	if opts.PreserveStarred {
		conds = append(conds, "starred = false")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// PreviewCleanup 返回按给定条件将被删除的记录数（不执行删除）。
// 与 Cleanup 共用 cleanupWhere，两者命中集合完全一致。
func (s *Store) PreviewCleanup(ctx context.Context, opts CleanupOptions) (int64, error) {
	if err := validateCleanupOptions(opts); err != nil {
		return 0, err
	}
	where, args := cleanupWhere(opts)
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clipboard_items`+where, args...).Scan(&n)
	return n, err
}

// Cleanup 按给定条件删除记录（同事务清理 item_groups 关联）。
// PreserveStarred=true 时收藏记录保留（其分组关联也保留）。
func (s *Store) Cleanup(ctx context.Context, opts CleanupOptions) (int64, error) {
	if err := validateCleanupOptions(opts); err != nil {
		return 0, err
	}
	where, args := cleanupWhere(opts)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	imageWhere := where
	if imageWhere == "" {
		imageWhere = " WHERE "
	} else {
		imageWhere += " AND "
	}
	imageArgs := append(append([]any{}, args...), domain.HistoryRecordContent, domain.FormatImageFile)
	imageRows, err := tx.QueryContext(ctx, `SELECT DISTINCT text FROM clipboard_items`+imageWhere+`record_type = ? AND format = ?`, imageArgs...)
	if err != nil {
		return 0, err
	}
	var imagePaths []string
	for imageRows.Next() {
		var storedPath string
		if err := imageRows.Scan(&storedPath); err != nil {
			imageRows.Close()
			return 0, err
		}
		imagePaths = append(imagePaths, storedPath)
	}
	if err := imageRows.Close(); err != nil {
		return 0, err
	}
	if err := imageRows.Err(); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM item_groups WHERE item_id IN (SELECT id FROM clipboard_items`+where+`)`, args...); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM clipboard_items`+where, args...)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		if err := recomputeAllConsecutiveDuplicatesTx(ctx, tx); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if err := s.removeManagedImagesIfUnreferenced(ctx, imagePaths); err != nil {
		return n, err
	}
	return n, nil
}
