package clipvault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

// Each row is requested alone: visibility must not depend on whether its
// predecessor happened to survive the caller's filter, page, or batch.
func TestDuplicateVisibilityAcrossReadSurfaces(t *testing.T) {
	store := openDataAPITestStore(t)
	ctx := context.Background()
	seeds := []struct {
		text            string
		sensitive       bool
		publicDuplicate bool
		rawDuplicate    bool
	}{
		{"shared", false, false, false},
		{"shared", false, true, true},
		{"shared", true, false, true},
		{"shared", false, false, true},
		{"shared", false, true, true},
		{"different", false, false, false},
	}
	var ids []int64
	for i, seed := range seeds {
		if i == 1 || i == 3 {
			if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeEvent, EventCode: domain.EventClipboardEmpty}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Save(ctx, ClipboardItem{
			RecordType: RecordTypeContent, Kind: domain.KindText, Format: domain.FormatUnicodeText,
			Text: seed.text, Sensitive: seed.sensitive,
		}); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := store.db.QueryRowContext(ctx, "SELECT MAX(id) FROM clipboard_items").Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	env, err := newEnvironment(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewExternalReadService(store, env)
	server, auth, session := newMaskTestServer(t, store)
	for _, authorized := range []bool{false, true} {
		if authorized {
			auth.GrantSecondaryAuth(session, 1)
		}
		for i, seed := range seeds {
			want := seed.publicDuplicate
			if authorized {
				want = seed.rawDuplicate
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/items?limit=1&offset="+formatGroupID(int64(len(seeds)-1-i)), nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			response := httptest.NewRecorder()
			server.handleSearch(response, request)
			var body struct {
				Items []struct {
					ID        int64 `json:"id"`
					Duplicate bool  `json:"consecutiveDuplicate"`
				} `json:"items"`
			}
			if response.Code != http.StatusOK {
				t.Fatalf("web status=%d", response.Code)
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Items) != 1 || body.Items[0].ID != ids[i] || body.Items[0].Duplicate != want {
				t.Fatalf("web row=%d authorized=%v got=%+v want=%v", i, authorized, body.Items, want)
			}
			if seed.sensitive && !authorized {
				continue
			}
			access := ReadAccess{AllowSensitive: authorized}
			item, err := svc.GetStoredItem(ctx, access, ids[i])
			if err != nil || item.ConsecutiveDuplicate != want {
				t.Fatalf("single row=%d authorized=%v duplicate=%v err=%v", i, authorized, item.ConsecutiveDuplicate, err)
			}
			batch, err := svc.GetStoredItems(ctx, access, []int64{ids[i]})
			if err != nil || len(batch) != 1 || batch[0].ConsecutiveDuplicate != want {
				t.Fatalf("batch row=%d authorized=%v err=%v", i, authorized, err)
			}
		}
		access := ReadAccess{AllowSensitive: authorized}
		check := func(items []StoredItem, err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
			wantCount := len(seeds)
			if !authorized {
				wantCount--
			}
			if len(items) != wantCount {
				t.Fatalf("count=%d want=%d", len(items), wantCount)
			}
			for _, item := range items {
				for i, id := range ids {
					if id != item.ID {
						continue
					}
					want := seeds[i].publicDuplicate
					if authorized {
						want = seeds[i].rawDuplicate
					}
					if item.ConsecutiveDuplicate != want {
						t.Fatalf("list row=%d authorized=%v duplicate=%v want=%v", i, authorized, item.ConsecutiveDuplicate, want)
					}
				}
			}
		}
		items, _, err := store.Search(ctx, SearchOptions{IncludeSensitive: authorized})
		check(items, err)
		items, _, err = svc.QueryItems(ctx, access, &dataAPIQueryPlan{orderSQL: "id DESC", limit: 50})
		check(items, err)
		result, err := svc.Search(ctx, access, SearchRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != len(items) {
			t.Fatalf("external search count=%d want=%d", len(result.Items), len(items))
		}
		for _, item := range result.Items {
			id, err := strconv.ParseInt(item.ID, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := svc.GetStoredItem(ctx, access, id)
			if err != nil || stored.ConsecutiveDuplicate != item.ConsecutiveDuplicate {
				t.Fatalf("external list differs from single: id=%s err=%v", item.ID, err)
			}
		}
	}
	t.Run("authorized sensitive list", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/items/sensitive", nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		response := httptest.NewRecorder()
		server.requireSecondary(server.handleSensitiveSearch)(response, request)
		var body struct {
			Items []StoredItem `json:"items"`
		}
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 1 || body.Items[0].ID != ids[2] || !body.Items[0].ConsecutiveDuplicate {
			t.Fatalf("authorized sensitive duplicate missing: %+v", body.Items)
		}
	})
	t.Run("sensitive filter is not permission", func(t *testing.T) {
		items, total, err := store.Search(ctx, SearchOptions{SensitiveOnly: true})
		if err != nil || total != 0 || len(items) != 0 {
			t.Fatalf("unauthorized sensitive filter count=%d total=%d err=%v", len(items), total, err)
		}
	})
	t.Run("web filtered page without secondary auth", func(t *testing.T) {
		unauthed, err := auth.CreateSession(10)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/items?q=shared&limit=1&offset=1", nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: unauthed})
		response := httptest.NewRecorder()
		server.handleSearch(response, request)
		var body struct {
			Items []StoredItem `json:"items"`
			Total int          `json:"total"`
		}
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d", response.Code)
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Total != 4 || len(body.Items) != 1 || body.Items[0].ID != ids[3] || body.Items[0].ConsecutiveDuplicate {
			t.Fatalf("filtered page changed adjacency or exposed sensitive row: %+v", body)
		}
	})
	// A filtered group excludes both predecessors. Its two visible equal rows
	// must retain their different flags from the full content timeline.
	group, err := store.CreateGroup(ctx, "duplicate visibility", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{ids[1], ids[3]} {
		if err := store.AddItemToGroup(ctx, id, group.ID); err != nil {
			t.Fatal(err)
		}
	}
	filtered, _, err := store.Search(ctx, SearchOptions{GroupID: group.ID})
	if err != nil || len(filtered) != 2 || filtered[0].ConsecutiveDuplicate || !filtered[1].ConsecutiveDuplicate {
		t.Fatalf("filtered duplicate flags incorrect: count=%d err=%v", len(filtered), err)
	}
	groupResult, err := svc.ListGroupItems(ctx, deniedAccess, formatGroupID(group.ID), 50, "")
	if err != nil || len(groupResult.Items) != 2 {
		t.Fatalf("group count=%d err=%v", len(groupResult.Items), err)
	}
	for _, item := range groupResult.Items {
		if item.ConsecutiveDuplicate != (item.ID == formatGroupID(ids[1])) {
			t.Fatalf("group duplicate flag incorrect: id=%s", item.ID)
		}
	}
	// Popup filtering and total counts must use the same public flag. The row
	// after a sensitive predecessor remains visible, even with duplicates hidden.
	items, total, err := store.Search(ctx, SearchOptions{HideConsecutiveDuplicates: true})
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("popup total=%d count=%d err=%v", total, len(items), err)
	}
	for i, id := range []int64{ids[5], ids[3], ids[0]} {
		if items[i].ID != id || items[i].ConsecutiveDuplicate {
			t.Fatalf("popup row=%d id=%d want=%d", i, items[i].ID, id)
		}
	}
	// A sensitive flag change must take effect immediately without rewriting
	// the stored duplicate flags or waiting for another clipboard capture.
	if err := store.SetSensitive(ctx, ids[2], false); err != nil {
		t.Fatal(err)
	}
	item, err := svc.GetStoredItem(ctx, deniedAccess, ids[3])
	if err != nil || !item.ConsecutiveDuplicate {
		t.Fatalf("unmarked predecessor duplicate=%v err=%v", item.ConsecutiveDuplicate, err)
	}
	if err := store.SetSensitive(ctx, ids[2], true); err != nil {
		t.Fatal(err)
	}
	item, err = svc.GetStoredItem(ctx, deniedAccess, ids[3])
	if err != nil || item.ConsecutiveDuplicate {
		t.Fatalf("marked predecessor duplicate=%v err=%v", item.ConsecutiveDuplicate, err)
	}
}

// Keep scale checks opt-in: this measures real encrypted DuckDB reads without
// imposing machine-dependent timing thresholds on the regression suite.
func BenchmarkDuplicateVisibility(b *testing.B) {
	for _, count := range []int{10000, 100000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			store, err := OpenStore(StoreOptions{DataDir: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			_, err = store.db.ExecContext(ctx, `INSERT INTO clipboard_items
				(id, created_at, record_type, kind, format, text, byte_len, char_len,
				content_sha256, consecutive_duplicate, meta_json, sensitive)
				SELECT n, n, 'content', 'text', 'CF_UNICODETEXT', 'sample', 6, 6,
				?, n > 1, '{}', n % 10 = 0 FROM range(1, ?) AS seed(n)`, hashString("sample"), count+1)
			if err != nil {
				b.Fatal(err)
			}
			svc := NewExternalReadService(store, Environment{})
			b.Run("single", func(b *testing.B) {
				for b.Loop() {
					if _, err := svc.GetStoredItem(ctx, deniedAccess, int64(count-1)); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("page", func(b *testing.B) {
				for b.Loop() {
					if _, _, err := store.Search(ctx, SearchOptions{Limit: 50}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("hide-duplicates", func(b *testing.B) {
				for b.Loop() {
					if _, _, err := store.Search(ctx, SearchOptions{Limit: 50, HideConsecutiveDuplicates: true}); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
