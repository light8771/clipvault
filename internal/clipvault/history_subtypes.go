package clipvault

import (
	"context"
	"net/http"
	"strings"
)

func validHistorySubtypeKind(kind string) bool {
	switch kind {
	case "text", "image", "video", "archive", "office", "files":
		return true
	default:
		return false
	}
}

func validHistorySubtype(kind, subtype string) bool {
	if kind == "text" {
		return subtype == "plain" || subtype == "rich"
	}
	if !validHistorySubtypeKind(kind) {
		return false
	}
	if kind == "image" && subtype == "embedded" {
		return true
	}
	return strings.HasPrefix(subtype, "ext:") &&
		!strings.ContainsAny(subtype[4:], "\\/\r\n\x00") && subtype == strings.ToLower(subtype) &&
		fileExtKind("file."+subtype[4:]) == kind
}

func (s *Store) historySubtypeOptions(ctx context.Context, kind string, includeSensitive bool, latest int) ([]string, error) {
	where := "record_type = 'content'"
	if !includeSensitive {
		where += " AND sensitive = false"
	}
	args := []any{}
	if latest > 0 {
		where += " AND id IN (SELECT id FROM clipboard_items WHERE record_type = 'content' ORDER BY id DESC LIMIT ?)"
		args = append(args, latest)
	}
	args = append(args, kind)
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT types.value->>'subtype' AS subtype FROM (SELECT content_types FROM clipboard_items WHERE "+where+") items, LATERAL json_each(items.content_types) types WHERE types.value->>'kind' = ? ORDER BY subtype", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []string{}
	for rows.Next() {
		var subtype string
		if err := rows.Scan(&subtype); err != nil {
			return nil, err
		}
		if validHistorySubtype(kind, subtype) {
			options = append(options, subtype)
		}
	}
	return options, rows.Err()
}

func (s *SearchServer) handleHistorySubtypes(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if !validHistorySubtypeKind(kind) {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_kind", s.i18nT("api.error.invalid_search_parameters", nil), nil)
		return
	}
	cfg := s.cfgMgr.Get()
	includeSensitive := !s.auth.HasSecondaryPassword(cfg) || s.auth.HasSecondaryAuth(GetSessionToken(r))
	latest := 0
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		latest = FreeTierVisibleLimit
	}
	options, err := s.store.historySubtypeOptions(r.Context(), kind, includeSensitive, latest)
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, options)
}
