package clipvault

import (
	"clipboard_ext/internal/clipvault/domain"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ExeStat 是某个来源应用（原始 exe 名）的记录计数。
type ExeStat struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

const eventGroupingWindowMS int64 = 5 * 60 * 1000

func (s *Store) SourceExeStatsFiltered(ctx context.Context, recordType domain.HistoryRecordType, includeSensitive bool, restrictToLatestN int) ([]ExeStat, error) {
	if recordType != "" && recordType != domain.HistoryRecordContent && recordType != domain.HistoryRecordEvent {
		return nil, fmt.Errorf("invalid record type %q", recordType)
	}
	where := `WHERE source_exe_name <> ''`
	var args []any
	if recordType != "" {
		where += ` AND record_type = ?`
		args = append(args, recordType)
	}
	if restrictToLatestN > 0 {
		if recordType == "" {
			where += ` AND id IN (SELECT id FROM clipboard_items ORDER BY id DESC LIMIT ?)`
			args = append(args, restrictToLatestN)
		} else {
			where += ` AND id IN (SELECT id FROM clipboard_items WHERE record_type = ? ORDER BY id DESC LIMIT ?)`
			args = append(args, recordType, restrictToLatestN)
		}
	}
	if !includeSensitive {
		where += ` AND sensitive = false`
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT source_exe_name, COUNT(*) AS c FROM clipboard_items `+
			where+` GROUP BY source_exe_name ORDER BY c DESC, source_exe_name ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExeStat{}
	for rows.Next() {
		var st ExeStat
		if err := rows.Scan(&st.Name, &st.Count); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) Search(ctx context.Context, opts SearchOptions) ([]StoredItem, int, error) {
	redactAdjacency := !opts.IncludeSensitive
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Limit > 500 {
		opts.Limit = 500
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}

	// Build dynamic WHERE clause
	var conds []string
	var args []any
	effectiveRecordType := opts.RecordType
	if effectiveRecordType == "" && !opts.IncludeAllRecords {
		effectiveRecordType = domain.HistoryRecordContent
	}
	if effectiveRecordType != "" && effectiveRecordType != domain.HistoryRecordContent && effectiveRecordType != domain.HistoryRecordEvent {
		return nil, 0, fmt.Errorf("invalid record type %q", effectiveRecordType)
	}
	if effectiveRecordType != "" {
		conds = append(conds, "record_type = ?")
		args = append(args, effectiveRecordType)
	}

	if opts.RestrictToLatestN > 0 {
		if effectiveRecordType != "" {
			conds = append(conds, "id IN (SELECT id FROM clipboard_items WHERE record_type = ? ORDER BY id DESC LIMIT ?)")
			args = append(args, effectiveRecordType, opts.RestrictToLatestN)
		} else {
			conds = append(conds, "id IN (SELECT id FROM clipboard_items ORDER BY id DESC LIMIT ?)")
			args = append(args, opts.RestrictToLatestN)
		}
	}

	terms, err := splitSearchTerms(opts.Query)
	if err != nil {
		return nil, 0, err
	}
	for _, term := range terms {
		like := "%" + escapeLike(term) + "%"
		// 每个 term 可匹配可见文本、类型、原生格式、元数据、分组名；多 term 之间 AND（语义同既有逻辑）。
		// 别名命中的原始名以精确匹配（source_exe_name = ?）并入该 term 的 OR 组。
		// 注意：来源应用名（source_exe_name）不参与主搜索的模糊匹配——只有显式使用 source_exe
		// 过滤参数（SourceExeNames IN 子句）时才按来源筛选，避免关键词误命中来源列（#4）。
		var aliasRaw []string
		if len(opts.ExeAliases) > 0 {
			lt := strings.ToLower(term)
			for raw, alias := range opts.ExeAliases {
				if strings.Contains(strings.ToLower(alias), lt) {
					aliasRaw = append(aliasRaw, raw)
				}
			}
		}
		// 托管图片记录（format = CLIPVAULT_IMAGE_FILE）的 text/format/meta_json 均为
		// 内部存储细节（相对路径、伪格式名、含数据目录的 imagePath），不参与模糊匹配，
		// 否则搜索 "clipvault"、"png" 等关键词会误命中所有图片记录；
		// 图片仍可通过 kind（类型）、content_sha256（哈希）与分组名检索。
		// 注：最外层右括号由下方统一追加（别名 OR 条件也在该分组内）。
		cond := `((text ILIKE ? ESCAPE '\' AND format <> 'CLIPVAULT_IMAGE_FILE') ` +
			`OR kind ILIKE ? ESCAPE '\' OR COALESCE(content_sha256, '') ILIKE ? ESCAPE '\' ` +
			`OR (format ILIKE ? ESCAPE '\' AND format NOT LIKE 'CLIPVAULT\_%' ESCAPE '\') ` +
			`OR event_code ILIKE ? ESCAPE '\' OR (meta_json ILIKE ? ESCAPE '\' AND format <> 'CLIPVAULT_IMAGE_FILE') ` +
			`OR EXISTS (SELECT 1 FROM item_groups ig JOIN groups g ON g.id = ig.group_id ` +
			`WHERE ig.item_id = clipboard_items.id AND g.name ILIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like, like, like, like)
		for _, raw := range aliasRaw {
			cond += ` OR source_exe_name = ?`
			args = append(args, raw)
		}
		cond += `)`
		conds = append(conds, cond)
	}
	if opts.StarredOnly {
		conds = append(conds, "starred = true")
	}
	if opts.HideConsecutiveDuplicates {
		if redactAdjacency {
			conds = append(conds, "NOT "+publicConsecutiveDuplicateSQL)
		} else {
			conds = append(conds, "consecutive_duplicate = false")
		}
	}
	if len(opts.SourceExeNames) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(opts.SourceExeNames)), ",")
		conds = append(conds, "source_exe_name IN ("+ph+")")
		for _, n := range opts.SourceExeNames {
			args = append(args, n)
		}
	}
	if opts.Kind != "" && opts.Subtype == "" && validHistorySubtypeKind(opts.Kind) {
		conds = append(conds, "json_contains(content_types, json_object('kind', CAST(? AS VARCHAR)))")
		args = append(args, opts.Kind)
	} else if opts.Kind != "" && opts.Subtype == "" {
		conds = append(conds, "(kind = ? OR json_contains(json_extract(content_json, '$.kinds'), to_json(CAST(? AS VARCHAR))))")
		args = append(args, opts.Kind)
		args = append(args, opts.Kind)
	}
	if opts.Subtype != "" {
		if !validHistorySubtype(opts.Kind, opts.Subtype) {
			return nil, 0, fmt.Errorf("invalid content subtype")
		}
		conds = append(conds, "json_contains(content_types, json_object('kind', CAST(? AS VARCHAR), 'subtype', CAST(? AS VARCHAR)))")
		args = append(args, opts.Kind, opts.Subtype)
	}
	if opts.CreatedFromMS > 0 {
		conds = append(conds, "created_at >= ?")
		args = append(args, opts.CreatedFromMS)
	}
	if opts.CreatedBeforeMS > 0 {
		conds = append(conds, "created_at < ?")
		args = append(args, opts.CreatedBeforeMS)
	}
	if opts.SensitiveOnly {
		conds = append(conds, "sensitive = true")
	}
	if !opts.IncludeSensitive && !opts.IncludeSensitivePlaceholders {
		conds = append(conds, "sensitive = false")
	}
	if opts.GroupID > 0 {
		conds = append(conds, "id IN (SELECT item_id FROM item_groups WHERE group_id = ?)")
		args = append(args, opts.GroupID)
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	if effectiveRecordType == domain.HistoryRecordEvent {
		return s.searchGroupedEvents(ctx, opts, where, args)
	}

	var total int
	if !opts.SkipTotal {

		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clipboard_items"+where, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}

	fetchArgs := append(append([]any{}, args...), opts.Limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+clipboardItemListColumns(redactAdjacency)+" FROM clipboard_items"+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		fetchArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []StoredItem
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// searchGroupedEvents keeps every raw event row immutable and collapses only
// the query result. A new occurrence group starts after a five-minute gap for
// the same canonical fingerprint. Source executable identity is already a
// normalized fingerprint component, so partitioning by its raw spelling would
// incorrectly split case variants reported by Windows.
func (s *Store) searchGroupedEvents(ctx context.Context, opts SearchOptions, where string, args []any) ([]StoredItem, int, error) {
	cte := eventGroupingCTE(where)

	var total int
	if !opts.SkipTotal {
		if err := s.db.QueryRowContext(ctx, cte+` SELECT COUNT(*) FROM grouped`, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}

	fetchArgs := append(append([]any{}, args...), opts.Limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx, cte+`
		SELECT `+clipboardItemColumnsAliased+`, g.occurrence_count, g.first_occurred_at, g.last_occurred_at
		FROM grouped g
		JOIN clipboard_items i ON i.id = g.representative_id
		ORDER BY g.last_occurred_at DESC, g.representative_id DESC
		LIMIT ? OFFSET ?`, fetchArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]StoredItem, 0)
	for rows.Next() {
		var item StoredItem
		if err := scanGroupedEvent(rows, &item); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func eventGroupingCTE(where string) string {
	return `WITH filtered AS (
		SELECT * FROM clipboard_items` + where + `
	), ordered AS (
		SELECT *, LAG(created_at) OVER (
			PARTITION BY event_fingerprint ORDER BY created_at, id
		) AS previous_created_at
		FROM filtered
	), marked AS (
		SELECT *, CASE WHEN previous_created_at IS NULL OR created_at - previous_created_at > ` + fmt.Sprint(eventGroupingWindowMS) + `
			THEN 1 ELSE 0 END AS group_start
		FROM ordered
	), bucketed AS (
		SELECT *, SUM(group_start) OVER (
			PARTITION BY event_fingerprint ORDER BY created_at, id
			ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
		) AS event_group
		FROM marked
	), grouped AS (
		SELECT event_fingerprint, event_group,
			FIRST(id ORDER BY created_at DESC, id DESC) AS representative_id, COUNT(*) AS occurrence_count,
			MIN(created_at) AS first_occurred_at, MAX(created_at) AS last_occurred_at
		FROM bucketed
		GROUP BY event_fingerprint, event_group
	)`
}

func splitSearchTerms(query string) ([]string, error) {
	terms := strings.Fields(query)
	if len(terms) > maxSearchTerms {
		return nil, fmt.Errorf("search query exceeds %d terms", maxSearchTerms)
	}
	return terms, nil
}

// GetByID 返回指定 ID 的单条记录。
func (s *Store) GetByID(ctx context.Context, id int64) (StoredItem, error) {
	var item StoredItem
	row := s.db.QueryRowContext(ctx,
		`SELECT `+clipboardItemColumns+`
		FROM clipboard_items WHERE id = ?`, id)
	if err := scanStoredItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return item, fmt.Errorf("%w: %d", ErrItemNotFound, id)
		}
		return item, err
	}
	return item, nil
}

// Native search rows carry summaries. Reload the full package and recheck
// visibility in one statement immediately before restoring the selection.
func (s *Store) getPublicClipboardItem(ctx context.Context, id int64, latestN int) (StoredItem, error) {
	where := " WHERE id = ? AND record_type = 'content' AND NOT sensitive"
	args := []any{id}
	if latestN > 0 {
		where += " AND id IN (SELECT id FROM clipboard_items WHERE record_type = 'content' ORDER BY id DESC LIMIT ?)"
		args = append(args, latestN)
	}
	var item StoredItem
	err := scanStoredItem(s.db.QueryRowContext(ctx, "SELECT "+clipboardItemReadColumns(true)+" FROM clipboard_items"+where, args...), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredItem{}, ErrItemNotFound
	}
	return item, err
}

// GetPositionByID 返回指定 ID 在全量列表（按 id DESC）中的 0-based 偏移量。
// 用于搜索结果定位到时间线中的页码位置。
func (s *Store) GetPositionByID(ctx context.Context, id int64) (int, error) {
	var count int
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM clipboard_items WHERE id > ? AND record_type = (SELECT record_type FROM clipboard_items WHERE id = ?)),
		EXISTS(SELECT 1 FROM clipboard_items WHERE id = ?)`, id, id, id).Scan(&count, &exists)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("%w: %d", ErrItemNotFound, id)
	}
	return count, nil
}

// CountAll returns the full content-record count. System status intentionally
// reports durable clipboard content separately from diagnostic capture events.
func (s *Store) CountAll(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clipboard_items WHERE record_type = ?", domain.HistoryRecordContent).Scan(&n)
	return n, err
}

// CountHistoryProjection returns the unfiltered total represented by one
// history view. Event totals use the same five-minute grouping as Search;
// the explicit all-record projection is raw by design.
func (s *Store) CountHistoryProjection(ctx context.Context, recordType domain.HistoryRecordType, includeAllRecords bool) (int, error) {
	if includeAllRecords {
		if recordType != "" {
			return 0, errors.New("record type and all-record projection are mutually exclusive")
		}
		var n int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clipboard_items`).Scan(&n)
		return n, err
	}
	switch recordType {
	case "", domain.HistoryRecordContent:
		return s.CountAll(ctx)
	case domain.HistoryRecordEvent:
		var n int
		err := s.db.QueryRowContext(ctx,
			eventGroupingCTE(` WHERE record_type = ?`)+` SELECT COUNT(*) FROM grouped`,
			domain.HistoryRecordEvent).Scan(&n)
		return n, err
	default:
		return 0, fmt.Errorf("invalid record type %q", recordType)
	}
}

func (s *Store) Star(ctx context.Context, id int64, starred bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE clipboard_items SET starred = ? WHERE id = ? AND record_type = ?`,
		starred, id, domain.HistoryRecordContent)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %d", ErrItemNotFound, id)
	}
	return nil
}

// SetSensitive 手动标记/取消标记敏感。手动标记与录制时按敏感规则自动
// 判定写在同一列：规则只在采集时生效，之后 sensitive 完全由用户控制。
func (s *Store) SetSensitive(ctx context.Context, id int64, sensitive bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE clipboard_items SET sensitive = ? WHERE id = ? AND record_type = ?`,
		sensitive, id, domain.HistoryRecordContent)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %d", ErrItemNotFound, id)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var recordType domain.HistoryRecordType
	var format, text string
	if err := tx.QueryRowContext(ctx, `SELECT record_type, format, text FROM clipboard_items WHERE id = ?`, id).Scan(&recordType, &format, &text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %d", ErrItemNotFound, id)
		}
		return err
	}
	var nextContentID sql.NullInt64
	if recordType == domain.HistoryRecordContent {
		if err := tx.QueryRowContext(ctx, `SELECT MIN(id) FROM clipboard_items
			WHERE record_type = 'content' AND id > ?`, id).Scan(&nextContentID); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM item_groups WHERE item_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM clipboard_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %d", ErrItemNotFound, id)
	}
	if nextContentID.Valid {
		if err := recomputeConsecutiveDuplicateForIDTx(ctx, tx, nextContentID.Int64); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if recordType == domain.HistoryRecordContent && format == domain.FormatImageFile {
		return s.removeManagedImagesIfUnreferenced(ctx, []string{text})
	}
	if format == domain.FormatComposite {
		return s.removeUnreferencedCompositeCache(ctx)
	}
	return nil
}
