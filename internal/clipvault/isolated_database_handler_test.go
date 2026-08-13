package clipvault

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsolatedDatabaseSwitchHandlerRequiresFreshSecondaryAuth(t *testing.T) {
	server := &SearchServer{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/recovery/isolated-switch", strings.NewReader(`{}`))
	result := httptest.NewRecorder()
	server.handleIsolatedDatabaseSwitch(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", result.Code, result.Body.String())
	}
}

func TestIsolatedDatabaseSwitchHandlersScheduleAndCancel(t *testing.T) {
	fixture := prepareIsolatedSwitchFixture(t)
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{env: Environment{DataDir: fixture.dataDir}, logger: logger}

	body := `{"id":"` + fixture.targetID + `","password":"` + isolatedTestPassword + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/recovery/isolated-switch", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), freshSecondaryContextKey{}, true))
	result := httptest.NewRecorder()
	server.handleIsolatedDatabaseSwitch(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("schedule status = %d, body = %s", result.Code, result.Body.String())
	}
	status, err := ListIsolatedDatabases(fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingID != fixture.targetID {
		t.Fatalf("pending id = %q", status.PendingID)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/v1/recovery/isolated-switch", nil)
	request = request.WithContext(context.WithValue(request.Context(), freshSecondaryContextKey{}, true))
	result = httptest.NewRecorder()
	server.handleIsolatedDatabaseSwitchCancel(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", result.Code, result.Body.String())
	}
	status, err = ListIsolatedDatabases(fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingID != "" {
		t.Fatalf("pending switch survived cancellation: %q", status.PendingID)
	}
}
