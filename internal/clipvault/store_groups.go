package clipvault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ListGroups 返回所有分组，按 id 排序。
func (s *Store) ListGroups(ctx context.Context) ([]StoredGroup, error) {
	return listGroups(ctx, s.db)
}

func listGroups(ctx context.Context, queryer storeQueryer) ([]StoredGroup, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id, name, parent_id, created_at FROM groups ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []StoredGroup
	for rows.Next() {
		var g StoredGroup
		var createdAtMS int64
		if err := rows.Scan(&g.ID, &g.Name, &g.ParentID, &createdAtMS); err != nil {
			return nil, err
		}
		g.CreatedAt = time.UnixMilli(createdAtMS).UTC()
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

var (
	ErrInvalidGroupParent = errors.New("invalid group parent")
	ErrInvalidGroupName   = errors.New("invalid group name")
	ErrGroupParentMissing = errors.New("group parent not found")
	ErrGroupNotFound      = errors.New("group not found")
	ErrGroupCycle         = errors.New("group hierarchy cycle")
)

// CreateGroup 创建一个新分组。
func (s *Store) CreateGroup(ctx context.Context, name string, parentID int64) (StoredGroup, error) {
	name = strings.TrimSpace(name)
	if err := validateGroupName(name); err != nil {
		return StoredGroup{}, err
	}
	if parentID < 0 {
		return StoredGroup{}, fmt.Errorf("%w: id must not be negative", ErrInvalidGroupParent)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StoredGroup{}, err
	}
	defer tx.Rollback()
	if parentID > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groups WHERE id = ?)`, parentID).Scan(&exists); err != nil {
			return StoredGroup{}, err
		}
		if !exists {
			return StoredGroup{}, fmt.Errorf("%w: %d", ErrGroupParentMissing, parentID)
		}
	}
	now := time.Now().UTC()
	var id int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO groups (name, parent_id, created_at) VALUES (?, ?, ?) RETURNING id`,
		name, parentID, now.UnixMilli()).Scan(&id)
	if err != nil {
		return StoredGroup{}, err
	}
	if err := tx.Commit(); err != nil {
		return StoredGroup{}, err
	}
	return StoredGroup{ID: id, Name: name, ParentID: parentID, CreatedAt: now}, nil
}

// UpdateGroup 更新分组名称和父分组。
func (s *Store) UpdateGroup(ctx context.Context, id int64, name string, parentID int64) error {
	name = strings.TrimSpace(name)
	if err := validateGroupName(name); err != nil {
		return err
	}
	if parentID < 0 {
		return fmt.Errorf("%w: id must not be negative", ErrInvalidGroupParent)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if parentID > 0 {
		if parentID == id {
			return fmt.Errorf("%w: cannot set parent to self", ErrGroupCycle)
		}
		if err := checkDescendant(ctx, tx, id, parentID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %d", ErrGroupParentMissing, parentID)
			}
			return err
		}
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE groups SET name = ?, parent_id = ? WHERE id = ?`, name, parentID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %d", ErrGroupNotFound, id)
	}
	return tx.Commit()
}

func validateGroupName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidGroupName)
	}
	if utf8.RuneCountInString(name) > maxGroupNameRunes {
		return fmt.Errorf("%w: name exceeds %d characters", ErrInvalidGroupName, maxGroupNameRunes)
	}
	return nil
}

// checkDescendant 检查 targetID 是否是 groupID 的后代（防止循环引用）。
func checkDescendant(ctx context.Context, queryer storeQueryer, groupID, targetID int64) error {
	cursor := targetID
	for cursor > 0 {
		if cursor == groupID {
			return fmt.Errorf("%w: cannot move group into its own descendant", ErrGroupCycle)
		}
		var parent int64
		err := queryer.QueryRowContext(ctx, `SELECT parent_id FROM groups WHERE id = ?`, cursor).Scan(&parent)
		if err != nil {
			return err
		}
		cursor = parent
	}
	return nil
}

// DeleteGroup 删除分组及其所有后代分组，同时清理 item_groups 关联。
// 后代收集与删除在同一事务内完成，避免"收集后、删除前"新建子分组漏删；
// defer tx.Rollback() 统一回滚，任何失败路径都不会残留半删状态。
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groups WHERE id = ?)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %d", ErrGroupNotFound, id)
	}

	ids := []int64{id}
	queue := []int64{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		rows, err := tx.QueryContext(ctx, `SELECT id FROM groups WHERE parent_id = ?`, cur)
		if err != nil {
			return err
		}
		for rows.Next() {
			var child int64
			if err := rows.Scan(&child); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, child)
			queue = append(queue, child)
		}
		rows.Close()
	}

	for _, gid := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_groups WHERE group_id = ?`, gid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddItemToGroup 将条目加入分组。
func (s *Store) AddItemToGroup(ctx context.Context, itemID, groupID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `INSERT INTO item_groups (item_id, group_id)
		SELECT items.id, groups.id
		FROM clipboard_items AS items, groups
		WHERE items.id = ? AND groups.id = ?
		ON CONFLICT DO NOTHING`, itemID, groupID)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM item_groups WHERE item_id = ? AND group_id = ?
		)`, itemID, groupID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: item %d or group %d", errItemOrGroupNotFound, itemID, groupID)
		}
	}
	return tx.Commit()
}

// RemoveItemFromGroup 将条目从分组移除。
func (s *Store) RemoveItemFromGroup(ctx context.Context, itemID, groupID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM item_groups WHERE item_id = ? AND group_id = ?`, itemID, groupID)
	return err
}

// GetItemGroups 返回条目所属的所有分组 id。
func (s *Store) GetItemGroups(ctx context.Context, itemID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id FROM item_groups WHERE item_id = ? ORDER BY group_id ASC`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var gid int64
		if err := rows.Scan(&gid); err != nil {
			return nil, err
		}
		ids = append(ids, gid)
	}
	return ids, rows.Err()
}
