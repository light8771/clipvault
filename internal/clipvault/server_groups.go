package clipvault

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// handleGroupsList 返回所有分组（平铺列表，前端构建树）。
func (s *SearchServer) handleGroupsList(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"groups": groups})
}

// handleGroupsCreate 创建新分组。
func (s *SearchServer) handleGroupsCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		ParentID int64  `json:"parentId"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_name", s.i18nT("api.error.group_name_empty", nil), nil)
		return
	}
	g, err := s.store.CreateGroup(r.Context(), strings.TrimSpace(req.Name), req.ParentID)
	if err != nil {
		if errors.Is(err, ErrInvalidGroupName) {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_group_name", s.i18nT("api.error.group_name_too_long", nil), err)
			return
		}
		if errors.Is(err, ErrInvalidGroupParent) {
			s.writeAPIError(w, http.StatusBadRequest, "invalid_parent_id", s.i18nT("api.error.invalid_parent_id", nil), err)
			return
		}
		if errors.Is(err, ErrGroupParentMissing) {
			s.writeAPIError(w, http.StatusNotFound, "parent_not_found", s.i18nT("api.error.parent_not_found", nil), err)
			return
		}
		s.writeError(w, err)
		return
	}
	s.logger.Info("group_created", map[string]any{"id": g.ID, "name": g.Name, "parentId": g.ParentID})
	s.writeJSON(w, map[string]any{"ok": true, "group": g})
}

// handleGroupsUpdate 更新分组（重命名 / 移动父级）。
func (s *SearchServer) handleGroupsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
		return
	}
	var req struct {
		Name     string `json:"name"`
		ParentID int64  `json:"parentId"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_name", s.i18nT("api.error.group_name_empty", nil), nil)
		return
	}
	if err := s.store.UpdateGroup(r.Context(), id, strings.TrimSpace(req.Name), req.ParentID); err != nil {
		switch {
		case errors.Is(err, ErrInvalidGroupName):
			s.writeAPIError(w, http.StatusBadRequest, "invalid_group_name", s.i18nT("api.error.group_name_too_long", nil), err)
		case errors.Is(err, ErrGroupNotFound), errors.Is(err, ErrGroupParentMissing):
			s.writeAPIError(w, http.StatusNotFound, "group_not_found", s.i18nT("api.error.group_or_parent_not_found", nil), err)
		case errors.Is(err, ErrInvalidGroupParent), errors.Is(err, ErrGroupCycle):
			s.writeAPIError(w, http.StatusBadRequest, "invalid_parent_id", s.i18nT("api.error.invalid_parent_cycle", nil), err)
		default:
			s.writeError(w, err)
		}
		return
	}
	s.logger.Info("group_updated", map[string]any{"id": id, "name": req.Name, "parentId": req.ParentID})
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleGroupsDelete 删除分组（含后代）。
func (s *SearchServer) handleGroupsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
		return
	}
	if err := s.store.DeleteGroup(r.Context(), id); err != nil {
		if errors.Is(err, ErrGroupNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "group_not_found", s.i18nT("api.error.group_not_found", nil), err)
			return
		}
		s.writeError(w, err)
		return
	}
	s.logger.Info("group_deleted", map[string]any{"id": id})
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleItemAddToGroup 将条目加入分组。
func (s *SearchServer) handleItemAddToGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemID  int64 `json:"itemId"`
		GroupID int64 `json:"groupId"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	if req.ItemID <= 0 || req.GroupID <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_item_or_group_id", nil), nil)
		return
	}
	if !s.authorizeItemAccess(w, r, req.ItemID) {
		return
	}
	if err := s.store.AddItemToGroup(r.Context(), req.ItemID, req.GroupID); err != nil {
		if errors.Is(err, errItemOrGroupNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.item_or_group_not_found", nil), err)
			return
		}
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleItemRemoveFromGroup 将条目从分组移除。
func (s *SearchServer) handleItemRemoveFromGroup(w http.ResponseWriter, r *http.Request) {
	itemID, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_item_id", s.i18nT("api.error.invalid_item_id", nil), nil)
		return
	}
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_id", s.i18nT("api.error.invalid_group_id", nil), nil)
		return
	}
	if !s.authorizeItemAccess(w, r, itemID) {
		return
	}
	if err := s.store.RemoveItemFromGroup(r.Context(), itemID, groupID); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleItemGroups 返回指定条目所属的分组 id 列表。
func (s *SearchServer) handleItemGroups(w http.ResponseWriter, r *http.Request) {
	itemID, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_item_id", s.i18nT("api.error.invalid_item_id", nil), nil)
		return
	}
	if !s.authorizeItemAccess(w, r, itemID) {
		return
	}
	ids, err := s.store.GetItemGroups(r.Context(), itemID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if ids == nil {
		ids = []int64{}
	}
	s.writeJSON(w, map[string]any{"groupIds": ids})
}

func (s *SearchServer) authorizeItemAccess(w http.ResponseWriter, r *http.Request, itemID int64) bool {
	item, err := s.store.GetByID(r.Context(), itemID)
	if err != nil {
		s.writeItemLookupError(w, err)
		return false
	}
	if s.enforceFreeWindow(w, r, itemID) {
		return false
	}
	return !item.Sensitive || s.authorizeSecondaryRequest(w, r)
}
