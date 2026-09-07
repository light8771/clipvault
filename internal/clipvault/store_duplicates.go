package clipvault

import (
	"context"
	"database/sql"
	"errors"
)

// recomputeConsecutiveDuplicateForIDTx refreshes the one content row whose
// predecessor may have changed after deletion. Capture-event rows never take
// part in content adjacency.
func recomputeConsecutiveDuplicateForIDTx(ctx context.Context, tx *sql.Tx, id int64) error {
	var kind, format, hash string
	if err := tx.QueryRowContext(ctx, `SELECT kind, format, content_sha256
		FROM clipboard_items WHERE id = ? AND record_type = 'content'`, id).Scan(&kind, &format, &hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	duplicate := false
	var previousKind, previousFormat, previousHash string
	err := tx.QueryRowContext(ctx, `SELECT kind, format, content_sha256
		FROM clipboard_items WHERE record_type = 'content' AND id < ?
		ORDER BY id DESC LIMIT 1`, id).Scan(&previousKind, &previousFormat, &previousHash)
	switch {
	case err == nil:
		duplicate = kind == previousKind && format == previousFormat && hash == previousHash
	case errors.Is(err, sql.ErrNoRows):
	default:
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE clipboard_items SET consecutive_duplicate = ? WHERE id = ?`, duplicate, id)
	return err
}

// recomputeAllConsecutiveDuplicatesTx derives every flag from the current
// content timeline. Imports and bulk cleanup can create multiple new
// boundaries, so a single windowed update is both clearer and faster than
// trying to predict each affected row in application code.
func recomputeAllConsecutiveDuplicatesTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `WITH ordered AS (
		SELECT id, kind, format, content_sha256,
			LAG(kind) OVER (ORDER BY id) AS previous_kind,
			LAG(format) OVER (ORDER BY id) AS previous_format,
			LAG(content_sha256) OVER (ORDER BY id) AS previous_hash
		FROM clipboard_items
		WHERE record_type = 'content'
	)
	UPDATE clipboard_items AS item
	SET consecutive_duplicate = (
		ordered.previous_kind IS NOT NULL
		AND item.kind = ordered.previous_kind
		AND item.format = ordered.previous_format
		AND item.content_sha256 = ordered.previous_hash
	)
	FROM ordered
	WHERE item.id = ordered.id`)
	return err
}
