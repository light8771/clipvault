package clipvault

import (
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

func (s *Store) SourceExeStatsFiltered(ctx context.Context, includeSensitive bool, restrictToLatestN int) ([]ExeStat, error) {
	where := `WHERE source_exe_name <> ''`
	var args []any
	if restrictToLatestN > 0 {
		where += ` AND id IN (SELECT id FROM clipboard_items ORDER BY id DESC LIMIT ?)`
		args = append(args, restrictToLatestN)
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

	if opts.RestrictToLatestN > 0 {
		conds = append(conds, "id IN (SELECT id FROM clipboard_items ORDER BY id DESC LIMIT ?)")
		args = append(args, opts.RestrictToLatestN)
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
		cond := `(text ILIKE ? ESCAPE '\' OR kind ILIKE ? ESCAPE '\' OR format ILIKE ? ESCAPE '\' ` +
			`OR sha256 ILIKE ? ESCAPE '\' OR meta_json ILIKE ? ESCAPE '\' ` +
			`OR EXISTS (SELECT 1 FROM item_groups ig JOIN groups g ON g.id = ig.group_id ` +
			`WHERE ig.item_id = clipboard_items.id AND g.name ILIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like, like, like)
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
	if len(opts.SourceExeNames) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(opts.SourceExeNames)), ",")
		conds = append(conds, "source_exe_name IN ("+ph+")")
		for _, n := range opts.SourceExeNames {
			args = append(args, n)
		}
	}
	if opts.Kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, opts.Kind)
	}
	if opts.CreatedFromMS > 0 {
		conds = append(conds, "created_at >= ?")
		args = append(args, opts.CreatedFromMS)
	}
	if opts.CreatedBeforeMS > 0 {
		conds = append(conds, "created_at < ?")
		args = append(args, opts.CreatedBeforeMS)
	}
	if opts.HideDuplicates {
		conds = append(conds, "is_duplicate = false")
	}
	if opts.HideUnavailable {
		conds = append(conds, "content_available = true")
	}
	if opts.SensitiveOnly {
		conds = append(conds, "sensitive = true")
	} else if !opts.IncludeSensitive {
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

	var total int
	if !opts.SkipTotal {

		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clipboard_items"+where, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}

	fetchArgs := append(append([]any{}, args...), opts.Limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+clipboardItemColumns+" FROM clipboard_items"+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
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

// GetPositionByID 返回指定 ID 在全量列表（按 id DESC）中的 0-based 偏移量。
// 用于搜索结果定位到时间线中的页码位置。
func (s *Store) GetPositionByID(ctx context.Context, id int64) (int, error) {
	var count int
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM clipboard_items WHERE id > ?),
		EXISTS(SELECT 1 FROM clipboard_items WHERE id = ?)`, id, id).Scan(&count, &exists)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("%w: %d", ErrItemNotFound, id)
	}
	return count, nil
}

// CountAll 返回 clipboard_items 表的总记录数（不含任何过滤条件）。
// 未激活时用于前端 badge 显示全量数据总数（转化钩子）。
func (s *Store) CountAll(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clipboard_items").Scan(&n)
	return n, err
}

func (s *Store) Star(ctx context.Context, id int64, starred bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE clipboard_items SET starred = ? WHERE id = ?`, starred, id)
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
	result, err := s.db.ExecContext(ctx, `UPDATE clipboard_items SET sensitive = ? WHERE id = ?`, sensitive, id)
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
	return tx.Commit()
}
