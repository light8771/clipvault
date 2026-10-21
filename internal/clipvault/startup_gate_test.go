package clipvault

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newGateForTest 构造 set_credentials 模式的门禁与配套 store。
func newGateForTest(t *testing.T) (*StartupGate, *Store) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	gate := NewStartupGate(store, dataDir, startupGateSetCredentials)
	if err := gate.i18n.SetLanguage(I18nChinese); err != nil {
		t.Fatal(err)
	}
	return gate, store
}

func TestNewStartupGateDoesNotReplaceApplicationTemplateI18n(t *testing.T) {
	previous := templateI18nService.Load()
	gate, _ := newGateForTest(t)
	if got := templateI18nService.Load(); got != previous {
		t.Fatal("constructing a startup gate replaced the application template i18n service")
	}

	result := httptest.NewRecorder()
	gate.handleIndex(result, httptest.NewRequest(http.MethodGet, "/", nil))
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), gate.t("startup_page.step_recovery", nil)) {
		t.Fatalf("startup gate did not render with its own i18n service: status=%d", result.Code)
	}
}

func postGateCredentials(t *testing.T, gate *StartupGate, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/startup/credentials", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	gate.handleComplete(result, request)
	return result
}

// TestStartupGateSetsLockPassword 验证带锁屏密码提交后三层凭据全部写入。
func TestStartupGateSetsLockPassword(t *testing.T) {
	gate, store := newGateForTest(t)
	result := postGateCredentials(t, gate, `{
		"password": "recovery-password-1",
		"confirm": "recovery-password-1",
		"secondaryPassword": "secondary-1",
		"secondaryConfirm": "secondary-1",
		"lockPassword": "lock-screen-1",
		"lockConfirm": "lock-screen-1"
	}`)
	if result.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", result.Code, result.Body.String())
	}
	select {
	case <-gate.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("gate did not finish")
	}
	if !gate.Success() {
		t.Fatal("gate success = false")
	}
	cfg, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAuthManager()
	if !auth.HasSecondaryPassword(cfg) {
		t.Fatal("secondary password not set")
	}
	if !auth.HasLockPassword(cfg) {
		t.Fatal("lock password not set")
	}
	if !verifyPasswordHash(cfg.Security.LockPasswordHash, "lock-screen-1") {
		t.Fatal("lock password does not verify")
	}
	if verifyPasswordHash(cfg.Security.LockPasswordHash, "wrong") {
		t.Fatal("wrong lock password accepted")
	}
}

// TestStartupGateSkipsLockPassword 验证留空跳过锁屏密码时其余凭据正常写入。
func TestStartupGateSkipsLockPassword(t *testing.T) {
	gate, store := newGateForTest(t)
	result := postGateCredentials(t, gate, `{
		"password": "recovery-password-1",
		"confirm": "recovery-password-1",
		"secondaryPassword": "secondary-1",
		"secondaryConfirm": "secondary-1"
	}`)
	if result.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", result.Code, result.Body.String())
	}
	cfg, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAuthManager()
	if !auth.HasSecondaryPassword(cfg) {
		t.Fatal("secondary password not set")
	}
	if auth.HasLockPassword(cfg) {
		t.Fatal("lock password unexpectedly set")
	}
}

// TestStartupGateLockMismatchRejected 验证锁屏密码不一致被拒绝且无副作用。
func TestStartupGateLockMismatchRejected(t *testing.T) {
	gate, store := newGateForTest(t)
	result := postGateCredentials(t, gate, `{
		"password": "recovery-password-1",
		"confirm": "recovery-password-1",
		"secondaryPassword": "secondary-1",
		"secondaryConfirm": "secondary-1",
		"lockPassword": "lock-screen-1",
		"lockConfirm": "different"
	}`)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", result.Code, result.Body.String())
	}
	if !strings.Contains(result.Body.String(), "锁屏密码不一致") {
		t.Fatalf("error message = %s", result.Body.String())
	}
	select {
	case <-gate.Done():
		t.Fatal("gate finished despite rejection")
	default:
	}
	cfg, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.SecondaryPasswordHash != "" || cfg.Security.LockPasswordHash != "" {
		t.Fatal("rejected submission left credentials behind")
	}
}
