package clipvault

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigUpdateFromRejectsStaleSnapshotWithoutSideEffects(t *testing.T) {
	store := &memoryConfigStore{cfg: DefaultConfig()}
	normalizeConfig(&store.cfg)
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	before := manager.Get()
	changed := cloneConfig(before)
	changed.Security.LockPasswordHash = "new-hash"
	changed.Source.ExeAliases = map[string]string{"app.exe": "new alias"}
	notifications := 0
	manager.OnChange(func(Config) error { notifications++; return nil })
	if err := manager.UpdateFrom(before, changed); err != nil {
		t.Fatal(err)
	}
	stale := cloneConfig(before)
	stale.UI.Language = I18nEnglish
	if err := manager.UpdateFrom(before, stale); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("stale commit: %v", err)
	}
	if notifications != 1 {
		t.Fatalf("conflict notified listeners %d times", notifications)
	}
	if store.cfg.Security.LockPasswordHash != "new-hash" || store.cfg.Source.ExeAliases["app.exe"] != "new alias" {
		t.Fatal("stale writer restored old secrets or metadata")
	}
	fresh := manager.Get()
	retry := cloneConfig(fresh)
	retry.UI.Language = I18nEnglish
	if err := manager.UpdateFrom(fresh, retry); err != nil {
		t.Fatal(err)
	}
	if manager.Get().Security.LockPasswordHash != "new-hash" {
		t.Fatal("retry lost latest password")
	}
}

func TestConfigUpdateFromRechecksAfterWaitingForConcurrentCommit(t *testing.T) {
	store := &serializedConfigStore{cfg: DefaultConfig(), firstEntered: make(chan struct{}), secondEntered: make(chan struct{}), releaseFirst: make(chan struct{})}
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	before := manager.Get()
	first, second := cloneConfig(before), cloneConfig(before)
	first.Brand.Title = "first"
	second.UI.Language = I18nEnglish
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- manager.UpdateFrom(before, first) }()
	<-store.firstEntered
	go func() { secondDone <- manager.UpdateFrom(before, second) }()
	close(store.releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("concurrent update: %v", err)
	}
	if store.saveCalls != 1 {
		t.Fatalf("conflicting write reached storage: %d", store.saveCalls)
	}
	if manager.Get().Brand.Title != "first" {
		t.Fatal("first commit lost")
	}
}

func TestConfigConflictHTTPResponse(t *testing.T) {
	s := &SearchServer{}
	result := httptest.NewRecorder()
	s.writeError(result, ErrConfigConflict)
	if result.Code != http.StatusConflict || !strings.Contains(result.Body.String(), `"code":"config_conflict"`) {
		t.Fatalf("%d %s", result.Code, result.Body.String())
	}
}

func TestConfigUpdateFromRuntimeFailureStillCommitsAndRejectsOldRetry(t *testing.T) {
	manager, err := NewConfigManager(&memoryConfigStore{cfg: DefaultConfig()})
	if err != nil {
		t.Fatal(err)
	}
	before := manager.Get()
	changed := cloneConfig(before)
	changed.Brand.Title = "committed"
	manager.OnChange(func(Config) error { return errors.New("listener failed") })
	var runtimeErr *ConfigRuntimeApplyError
	if err := manager.UpdateFrom(before, changed); !errors.As(err, &runtimeErr) {
		t.Fatalf("runtime error: %v", err)
	}
	if manager.Get().Brand.Title != "committed" {
		t.Fatal("committed config lost")
	}
	if err := manager.UpdateFrom(before, changed); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("old snapshot accepted: %v", err)
	}
}

func configUpdateRequest(server *SearchServer, body io.Reader) *http.Request {
	request := httptest.NewRequest(http.MethodPut, "/api/v1/config", body)
	_, version := server.cfgMgr.GetVersioned()
	request.Header.Set(configVersionHeader, version)
	return request
}

func TestConfigBrowserSnapshotConflict(t *testing.T) {
	manager, err := NewConfigManager(&memoryConfigStore{cfg: DefaultConfig()})
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{cfgMgr: manager, logger: logger, auth: NewAuthManager()}
	firstRead := httptest.NewRecorder()
	server.handleGetConfig(firstRead, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	version := firstRead.Header().Get(configVersionHeader)
	if version == "" {
		t.Fatal("missing snapshot version")
	}
	before := manager.Get()
	first := cloneConfig(before)
	first.Brand.Title = "first browser"
	send := func(cfg Config, token string) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
		request.Header.Set(configVersionHeader, token)
		result := httptest.NewRecorder()
		server.handlePutConfig(result, request)
		return result
	}
	saved := send(first, version)
	if saved.Code != http.StatusOK {
		t.Fatalf("first save: %d %s", saved.Code, saved.Body.String())
	}
	if saved.Header().Get(configVersionHeader) == version {
		t.Fatal("version did not advance")
	}
	stale := cloneConfig(before)
	stale.MaxTextChars++
	for _, token := range []string{version, ""} {
		result := send(stale, token)
		if result.Code != http.StatusConflict {
			t.Fatalf("stale save: %d %s", result.Code, result.Body.String())
		}
	}
	if manager.Get().Brand.Title != first.Brand.Title || manager.Get().MaxTextChars != before.MaxTextChars {
		t.Fatal("stale browser overwrote saved configuration")
	}
	fresh, token := manager.GetVersioned()
	fresh.MaxTextChars++
	if result := send(fresh, token); result.Code != http.StatusOK {
		t.Fatalf("fresh retry: %d %s", result.Code, result.Body.String())
	}
}

func TestConfigVersionTracksEditableStateAndReload(t *testing.T) {
	store := &memoryConfigStore{cfg: DefaultConfig()}
	normalizeConfig(&store.cfg)
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	cfg, version := manager.GetVersioned()
	cfg.Brand.LogoVersion = 123
	cfg.Brand.LogoContentType = "image/png"
	cfg.Background.Version = 456
	cfg.Background.ContentType = "image/png"
	if err := manager.Update(cfg); err != nil {
		t.Fatal(err)
	}
	_, afterAsset := manager.GetVersioned()
	if afterAsset != version {
		t.Fatal("server-owned asset metadata invalidated settings")
	}
	store.cfg.Security.LockPasswordHash = "changed"
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	_, afterReload := manager.GetVersioned()
	if afterReload == version {
		t.Fatal("reload failed to invalidate stale settings")
	}
	other, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	_, afterRestart := other.GetVersioned()
	if afterRestart == afterReload {
		t.Fatal("version reused across process instances")
	}
}
