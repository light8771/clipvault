package clipvault

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestHandleItemAddToGroupRejectsMissingReferences(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := &SearchServer{store: store}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/items/group", bytes.NewBufferString(`{"itemId":999,"groupId":999}`))
	result := httptest.NewRecorder()
	server.handleItemAddToGroup(result, request)
	if result.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", result.Code, http.StatusNotFound, result.Body.String())
	}
}

func TestItemGroupHandlersEnforceFreeTierWindow(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	saveFreeTierItems(t, store, []string{"outside-window"})
	items, _, err := store.Search(context.Background(), SearchOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	itemID := items[0].ID
	for i := 0; i < FreeTierVisibleLimit; i++ {
		saveFreeTierItems(t, store, []string{"newer-" + strconv.Itoa(i)})
	}
	group, err := store.CreateGroup(context.Background(), "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	server := newFreeTierTestServer(t, store, newUnlicensedManager(t))

	tests := []struct {
		name   string
		method string
		url    string
		body   string
		run    func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "add", method: http.MethodPost, url: "/api/v1/items/group",
			body: `{"itemId":` + strconv.FormatInt(itemID, 10) + `,"groupId":` + strconv.FormatInt(group.ID, 10) + `}`,
			run:  server.handleItemAddToGroup,
		},
		{
			name: "remove", method: http.MethodDelete,
			url: "/api/v1/items/group?item_id=" + strconv.FormatInt(itemID, 10) + "&group_id=" + strconv.FormatInt(group.ID, 10),
			run: server.handleItemRemoveFromGroup,
		},
		{
			name: "list", method: http.MethodGet,
			url: "/api/v1/items/groups?item_id=" + strconv.FormatInt(itemID, 10),
			run: server.handleItemGroups,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.url, strings.NewReader(tt.body))
			result := httptest.NewRecorder()
			tt.run(result, request)
			if result.Code != http.StatusPaymentRequired {
				t.Fatalf("status = %d, want %d: %s", result.Code, http.StatusPaymentRequired, result.Body.String())
			}
		})
	}
}

func TestItemGroupHandlersRequireSecondaryAuthorizationForSensitiveItems(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "sensitive", Sensitive: true,
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(context.Background(), SearchOptions{Limit: 1, IncludeSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	itemID := items[0].ID
	group, err := store.CreateGroup(context.Background(), "test", 0)
	if err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Security.SecondaryPasswordHash = "configured"
	auth := NewAuthManager()
	session, err := auth.CreateSession(10)
	if err != nil {
		t.Fatal(err)
	}
	server := &SearchServer{
		store:  store,
		cfgMgr: &ConfigManager{cfg: cfg, store: store},
		auth:   auth,
	}

	tests := []struct {
		name   string
		method string
		url    string
		body   string
		run    func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "add", method: http.MethodPost, url: "/api/v1/items/group",
			body: `{"itemId":` + strconv.FormatInt(itemID, 10) + `,"groupId":` + strconv.FormatInt(group.ID, 10) + `}`,
			run:  server.handleItemAddToGroup,
		},
		{
			name: "list", method: http.MethodGet,
			url: "/api/v1/items/groups?item_id=" + strconv.FormatInt(itemID, 10),
			run: server.handleItemGroups,
		},
		{
			name: "remove", method: http.MethodDelete,
			url: "/api/v1/items/group?item_id=" + strconv.FormatInt(itemID, 10) + "&group_id=" + strconv.FormatInt(group.ID, 10),
			run: server.handleItemRemoveFromGroup,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.url, strings.NewReader(tt.body))
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			result := httptest.NewRecorder()
			tt.run(result, request)
			if result.Code != http.StatusForbidden {
				t.Fatalf("unauthorized status = %d, want %d: %s", result.Code, http.StatusForbidden, result.Body.String())
			}

			secondaryToken, err := auth.GenerateSecondaryToken(session)
			if err != nil {
				t.Fatal(err)
			}
			request = httptest.NewRequest(tt.method, tt.url, strings.NewReader(tt.body))
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			request.Header.Set("X-ClipVault-Secondary-Token", secondaryToken)
			result = httptest.NewRecorder()
			tt.run(result, request)
			if result.Code != http.StatusOK {
				t.Fatalf("authorized status = %d, want %d: %s", result.Code, http.StatusOK, result.Body.String())
			}
		})
	}
}

func TestItemGroupListAndRemoveRejectMissingItem(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := &SearchServer{store: store}

	for _, tt := range []struct {
		name string
		url  string
		run  func(http.ResponseWriter, *http.Request)
	}{
		{name: "list", url: "/api/v1/items/groups?item_id=999", run: server.handleItemGroups},
		{name: "remove", url: "/api/v1/items/group?item_id=999&group_id=999", run: server.handleItemRemoveFromGroup},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := httptest.NewRecorder()
			tt.run(result, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if result.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d: %s", result.Code, http.StatusNotFound, result.Body.String())
			}
		})
	}
}
