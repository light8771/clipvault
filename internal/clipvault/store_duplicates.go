package clipvault

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Read-time masking uses the actual content predecessor, before filtering or
// pagination. Never skip a sensitive predecessor to find an earlier public row.
// Keep this in SQL so the flag and its visibility come from the same snapshot.
const publicConsecutiveDuplicateSQL = `(clipboard_items.consecutive_duplicate
	AND NOT clipboard_items.sensitive
	AND clipboard_items.id NOT IN (
		SELECT next_id FROM (
			SELECT sensitive, LEAD(id) OVER (ORDER BY id) AS next_id
			FROM clipboard_items WHERE record_type = 'content'
		) AS adjacency WHERE sensitive AND next_id IS NOT NULL
	))`

func clipboardItemReadColumns(redactSensitiveAdjacency bool) string {
	if !redactSensitiveAdjacency {
		return clipboardItemColumns
	}
	return clipboardItemColumnsBeforeDuplicate + publicConsecutiveDuplicateSQL + " AS consecutive_duplicate" + clipboardItemColumnsAfterDuplicate
}

// Lists project metadata in DuckDB so binary packages never cross into the
// application just to render a page of cards. Full reads are explicit actions.
func clipboardItemListColumns(redactSensitiveAdjacency bool) string {
	return strings.Replace(clipboardItemReadColumns(redactSensitiveAdjacency), "CAST(content_json AS VARCHAR)", `CASE WHEN content_json IS NULL THEN NULL ELSE CAST(json_object(
		'kinds', json_extract(content_json, '$.kinds'),
		'issues', json_extract(content_json, '$.issues'),
		'representations', list_transform(json_extract_string(content_json, '$.representations[*].format'), x -> json_object('format', x)),
		'assets', []
	) AS VARCHAR) END`, 1)
}

const contentKindsSQL = `CASE WHEN content_json IS NULL THEN [kind] ELSE CAST(json_extract(content_json, '$.kinds') AS VARCHAR[]) END`

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
