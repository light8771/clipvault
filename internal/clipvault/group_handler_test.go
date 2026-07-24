package clipvault

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleItemAddToGroupRejectsMissingReferences(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := &SearchServer{store: store}
	request := httptest.NewRequest(http.MethodPost, "/api/items/group", bytes.NewBufferString(`{"itemId":999,"groupId":999}`))
	result := httptest.NewRecorder()
	server.handleItemAddToGroup(result, request)
	if result.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", result.Code, http.StatusNotFound, result.Body.String())
	}
}
