package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type serializedConfigStore struct {
	mu            sync.Mutex
	cfg           Config
	saveCalls     int
	firstEntered  chan struct{}
	secondEntered chan struct{}
	releaseFirst  chan struct{}
}

type memoryConfigStore struct {
	cfg Config
}

func (s *memoryConfigStore) GetConfig() (Config, error) { return cloneConfig(s.cfg), nil }
func (s *memoryConfigStore) SaveConfig(cfg Config) error {
	s.cfg = cloneConfig(cfg)
	return nil
}

func (s *serializedConfigStore) GetConfig() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneConfig(s.cfg), nil
}

func (s *serializedConfigStore) SaveConfig(cfg Config) error {
	s.mu.Lock()
	s.saveCalls++
	call := s.saveCalls
	s.cfg = cloneConfig(cfg)
	s.mu.Unlock()
	if call == 1 {
		close(s.firstEntered)
		<-s.releaseFirst
	} else if call == 2 {
		close(s.secondEntered)
	}
	return nil
}

func TestSecondaryTokenIsSessionBoundOneTimeAndExpiring(t *testing.T) {
	auth := NewAuthManager()
	sessionA, _ := auth.CreateSession(10)
	sessionB, _ := auth.CreateSession(10)
	token, err := auth.GenerateSecondaryToken(sessionA)
	if err != nil {
		t.Fatal(err)
	}
	if auth.ConsumeSecondaryToken(token, sessionB) {
		t.Fatal("secondary token was accepted by a different session")
	}
	if auth.ConsumeSecondaryToken(token, sessionA) {
		t.Fatal("failed cross-session attempt did not consume the token")
	}
	token, _ = auth.GenerateSecondaryToken(sessionA)
	if !auth.ConsumeSecondaryToken(token, sessionA) {
		t.Fatal("secondary token was rejected by its bound session")
	}
	if auth.ConsumeSecondaryToken(token, sessionA) {
		t.Fatal("secondary token was reusable")
	}
	token, _ = auth.GenerateSecondaryToken(sessionA)
	auth.mu.Lock()
	grant := auth.secondaryTokens[token]
	grant.ExpiresAt = time.Now().Add(-time.Second)
	auth.secondaryTokens[token] = grant
	auth.mu.Unlock()
	if auth.ConsumeSecondaryToken(token, sessionA) {
		t.Fatal("expired secondary token was accepted")
	}
}

func TestPasswordRateLimitsAreSeparatedByPurpose(t *testing.T) {
	auth := NewAuthManager()
	auth.RecordFailedAttemptFor(ratePurposeSecondary)
	auth.ResetRateLimitFor(ratePurposeLock)
	if got := auth.CheckRateLimitFor(ratePurposeSecondary).Remaining; got != rateLimitThreshold-1 {
		t.Fatalf("secondary failures were reset by lock success: remaining=%d", got)
	}
}

func TestFailedSecondaryAuthenticationDoesNotCreateSession(t *testing.T) {
	cfg := DefaultConfig()
	var err error
	cfg.Security.SecondaryPasswordHash, err = newPasswordHash("correct-secondary-password")
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAuthManager()
	server := &SearchServer{
		cfgMgr: &ConfigManager{cfg: cfg},
		auth:   auth,
	}

	result := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/session/secondary", strings.NewReader(`{"password":"wrong-secondary-password"}`))
	server.handleAuthSecondary(result, request)
	if result.Code != http.StatusUnauthorized || !strings.Contains(result.Body.String(), `"code":"invalid_password"`) {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	auth.mu.RLock()
	sessionCount := len(auth.sessions)
	auth.mu.RUnlock()
	if sessionCount != 0 {
		t.Fatalf("failed secondary authentication created %d session(s)", sessionCount)
	}
}

func TestConfigManagerSnapshotsOwnReferenceFields(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := manager.Get()
	snapshot.Source.ExeAliases["app.exe"] = "mutated"
	snapshot.Source.ExeWhitelist = append(snapshot.Source.ExeWhitelist, "app.exe")
	snapshot.Sensitive.CustomPatterns = append(snapshot.Sensitive.CustomPatterns, "secret")
	fresh := manager.Get()
	if len(fresh.Source.ExeAliases) != 0 || len(fresh.Source.ExeWhitelist) != 0 || len(fresh.Sensitive.CustomPatterns) != 0 {
		t.Fatalf("Get returned shared config references: %+v", fresh)
	}

	input := manager.Get()
	input.Source.ExeAliases["app.exe"] = "Application"
	input.Source.ExeWhitelist = []string{"app.exe"}
	input.Sensitive.CustomPatterns = []string{"secret"}
	var secondListenerAlias string
	manager.OnChange(func(cfg Config) error {
		cfg.Source.ExeAliases["app.exe"] = "listener mutation"
		cfg.Source.ExeWhitelist[0] = "listener.exe"
		cfg.Sensitive.CustomPatterns[0] = "listener"
		return nil
	})
	manager.OnChange(func(cfg Config) error {
		secondListenerAlias = cfg.Source.ExeAliases["app.exe"]
		return nil
	})
	if err := manager.Update(input); err != nil {
		t.Fatal(err)
	}
	input.Source.ExeAliases["app.exe"] = "caller mutation"
	input.Source.ExeWhitelist[0] = "caller.exe"
	input.Sensitive.CustomPatterns[0] = "caller"
	fresh = manager.Get()
	if secondListenerAlias != "Application" || fresh.Source.ExeAliases["app.exe"] != "Application" ||
		fresh.Source.ExeWhitelist[0] != "app.exe" || fresh.Sensitive.CustomPatterns[0] != "secret" {
		t.Fatalf("Update or listeners retained shared references: listener=%q config=%+v", secondListenerAlias, fresh)
	}
}

func TestConfigManagerSerializesPersistenceAndCacheUpdates(t *testing.T) {
	store := &serializedConfigStore{
		cfg: DefaultConfig(), firstEntered: make(chan struct{}), secondEntered: make(chan struct{}), releaseFirst: make(chan struct{}),
	}
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	first, second := manager.Get(), manager.Get()
	first.Brand.Title = "first"
	second.Brand.Title = "second"
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- manager.Update(first) }()
	<-store.firstEntered
	go func() { secondDone <- manager.Update(second) }()
	select {
	case <-store.secondEntered:
		t.Fatal("second config persistence entered before the first update completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(store.releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Brand.Title != "second" || manager.Get().Brand.Title != "second" {
		t.Fatalf("persistence/cache diverged: persisted=%q cached=%q", persisted.Brand.Title, manager.Get().Brand.Title)
	}
}

func TestPasswordChangeRevokesSecondaryAuthAfterRuntimeApplyFailure(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Security.SecondaryPasswordHash, err = newPasswordHash("old-secondary-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	manager.OnChange(func(Config) error { return errors.New("runtime apply failed") })
	auth := NewAuthManager()
	session, err := auth.CreateSession(10)
	if err != nil {
		t.Fatal(err)
	}
	auth.GrantSecondaryAuth(session, 10)
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{cfgMgr: manager, auth: auth, logger: logger}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/session/password", strings.NewReader(
		`{"type":"secondary","action":"change","oldPassword":"old-secondary-password","newPassword":"new-secondary-password"}`,
	))
	result := httptest.NewRecorder()
	server.handleAuthSetPassword(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var response struct {
		OK           bool   `json:"ok"`
		RuntimeError string `json:"runtimeError"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RuntimeError != "runtime apply failed" {
		t.Fatalf("response=%+v", response)
	}
	if auth.HasSecondaryAuth(session) {
		t.Fatal("secondary authentication survived a committed password change")
	}
	if !auth.VerifySecondaryPassword(manager.Get(), "new-secondary-password") {
		t.Fatal("new secondary password was not committed")
	}
}

func TestPutConfigRefreshesAuthTimeoutsAfterRuntimeApplyFailure(t *testing.T) {
	initial := DefaultConfig()
	initial.Security.LockSessionTimeout = 10
	initial.Security.SecondaryAuthTimeout = 10
	store := &memoryConfigStore{cfg: initial}
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	manager.OnChange(func(Config) error { return &HotkeyError{Msg: "hotkey conflict"} })

	auth := NewAuthManager()
	defer auth.Close()
	session, err := auth.CreateSession(initial.Security.LockSessionTimeout)
	if err != nil {
		t.Fatal(err)
	}
	auth.GrantSecondaryAuth(session, initial.Security.SecondaryAuthTimeout)
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{cfgMgr: manager, auth: auth, logger: logger}

	updated := manager.Get()
	updated.Security.LockSessionTimeout = 1
	updated.Security.SecondaryAuthTimeout = 1
	body, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	result := httptest.NewRecorder()
	server.handlePutConfig(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var response struct {
		OK           bool   `json:"ok"`
		HotkeyError  string `json:"hotkeyError"`
		RuntimeError string `json:"runtimeError"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.HotkeyError != "hotkey conflict" || response.RuntimeError != "hotkey conflict" {
		t.Fatalf("response=%+v", response)
	}
	snapshot := auth.GetSession(session)
	if snapshot == nil {
		t.Fatal("current session was destroyed")
	}
	remainingSession := time.Until(snapshot.ExpiresAt)
	remainingSecondary := time.Until(snapshot.SecondaryExpiry)
	if remainingSession < 50*time.Second || remainingSession > 70*time.Second {
		t.Fatalf("session timeout was not refreshed: %v", remainingSession)
	}
	if !snapshot.SecondaryAuth || remainingSecondary < 50*time.Second || remainingSecondary > 70*time.Second {
		t.Fatalf("secondary timeout was not refreshed: auth=%v remaining=%v", snapshot.SecondaryAuth, remainingSecondary)
	}
}

func TestPutConfigEnablesSecondaryCacheImmediatelyFromOneTimeAuthorization(t *testing.T) {
	initial := DefaultConfig()
	var err error
	initial.Security.SecondaryPasswordHash, err = newPasswordHash("secondary-password")
	if err != nil {
		t.Fatal(err)
	}
	initial.Security.SecondaryAuthTimeout = 0
	store := &memoryConfigStore{cfg: initial}
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager()
	defer auth.Close()
	session, err := auth.CreateSession(initial.Security.LockSessionTimeout)
	if err != nil {
		t.Fatal(err)
	}
	oneTimeToken, err := auth.GenerateSecondaryToken(session)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{cfgMgr: manager, auth: auth, logger: logger}

	updated := manager.Get()
	updated.Security.SecondaryAuthTimeout = 1111
	body, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	request.Header.Set("X-ClipVault-Secondary-Token", oneTimeToken)
	result := httptest.NewRecorder()
	server.requireSecondary(server.handlePutConfig)(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}

	snapshot := auth.GetSession(session)
	if snapshot == nil || !snapshot.SecondaryAuth {
		t.Fatal("new secondary cache duration did not take effect for the authorized session")
	}
	remaining := time.Until(snapshot.SecondaryExpiry)
	want := 1111 * time.Minute
	if remaining < want-time.Minute || remaining > want+time.Minute {
		t.Fatalf("secondary cache remaining=%v, want about %v", remaining, want)
	}
}

func TestStoreDBKeyReturnsIndependentCopy(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := store.DBKey()
	original := first[0]
	first[0] ^= 0xff
	if store.DBKey()[0] != original {
		t.Fatal("DBKey exposed the store's mutable encryption key")
	}
}

func TestBackupServiceStopIsIdempotentAndRejectsNewWork(t *testing.T) {
	service := &BackupService{}
	service.Stop()
	service.Stop()
	if err := service.doDirBackup(DefaultConfig(), backupTriggerManual, false); !errors.Is(err, errBackupStopped) {
		t.Fatalf("directory backup after Stop error=%v", err)
	}
	if err := service.doEmailBackup(DefaultConfig(), backupTriggerManual, false); !errors.Is(err, errBackupStopped) {
		t.Fatalf("email backup after Stop error=%v", err)
	}
}

func TestBackupServiceConcurrentStartStop(t *testing.T) {
	for i := 0; i < 100; i++ {
		cfgMgr := &ConfigManager{cfg: DefaultConfig()}
		backup := NewBackupService(cfgMgr, nil, nil, nil)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); backup.Start() }()
		go func() { defer wg.Done(); backup.Stop() }()
		wg.Wait()
		backup.Stop()
	}
}

func TestJSONRequestDecoderRejectsUnknownAndTrailingValues(t *testing.T) {
	for _, body := range []string{
		`{"password":"ok","unexpected":true}`,
		`{"password":"ok"}{"password":"again"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		result := httptest.NewRecorder()
		var payload struct {
			Password string `json:"password"`
		}
		if err := decodeJSONRequest(result, request, &payload, 1024); err == nil {
			t.Fatalf("invalid JSON request was accepted: %s", body)
		}
	}
}

func TestStoreRejectsIncompleteOrUnsupportedSchema(t *testing.T) {
	incompleteDir := t.TempDir()
	if err := os.MkdirAll(activeDatabaseDirectory(incompleteDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeDatabaseFile(incompleteDir, databaseFileName), []byte("not-a-database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(StoreOptions{DataDir: incompleteDir}); err == nil || !strings.Contains(err.Error(), "must either both exist or both be absent") {
		t.Fatalf("incomplete store error=%v", err)
	}

	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE settings SET value = '"unsupported"' WHERE key = 'schema.version'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(StoreOptions{DataDir: dir}); err == nil || !strings.Contains(err.Error(), "unsupported database schema version") {
		t.Fatalf("unsupported schema error=%v", err)
	}

	identityDir := t.TempDir()
	identityStore, err := OpenStore(StoreOptions{DataDir: identityDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identityStore.db.Exec(`DELETE FROM settings WHERE key = 'schema.identity'`); err != nil {
		t.Fatal(err)
	}
	if err := identityStore.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(StoreOptions{DataDir: identityDir}); err == nil || !strings.Contains(err.Error(), "unsupported database schema") {
		t.Fatalf("missing schema identity error=%v", err)
	}
}

func TestNewStoreInitializationFailureRemovesCreatedFiles(t *testing.T) {
	dir := t.TempDir()
	injectedErr := errors.New("injected schema failure")
	if _, err := openStore(StoreOptions{DataDir: dir}, func(*Store) error { return injectedErr }); !errors.Is(err, injectedErr) {
		t.Fatalf("openStore error=%v, want injected failure", err)
	}
	for _, name := range []string{"clipvault.key", "clipvault.duckdb", "clipvault.duckdb.wal"} {
		if _, err := os.Stat(activeDatabaseFile(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived failed initialization: %v", name, err)
		}
	}
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("retry after failed initialization: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryServerRejectsMutationAfterCompletion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(activeDatabaseDirectory(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := activeDatabaseFile(dir, databaseKeyFileName)
	if err := os.WriteFile(keyPath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewRecoveryServer(dir)
	server.complete = true
	request := httptest.NewRequest(http.MethodPost, "/api/v1/recovery/reset", strings.NewReader(`{"confirmation":"CREATE NEW"}`))
	result := httptest.NewRecorder()
	server.handleNew(result, request)
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data != nil {
		t.Fatalf("completed recovery server accepted another mutation: %s", result.Body.String())
	}
	data, err := os.ReadFile(keyPath)
	if err != nil || string(data) != "keep" {
		t.Fatalf("completed recovery mutation touched existing data: data=%q err=%v", data, err)
	}
}

func TestRecoveryResetSeparatesValidationAndIOErrors(t *testing.T) {
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	blockedDataDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedDataDir, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &SearchServer{
		env:    Environment{DataDir: blockedDataDir},
		store:  &Store{dbKey: make([]byte, wrappingLen)},
		logger: logger,
	}

	requestReset := func(password string) *httptest.ResponseRecorder {
		result := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/recovery/reset", strings.NewReader(`{"password":`+strconv.Quote(password)+`}`))
		request = request.WithContext(context.WithValue(request.Context(), freshSecondaryContextKey{}, true))
		server.handleRecoveryReset(result, request)
		return result
	}
	if result := requestReset("short"); result.Code != http.StatusBadRequest {
		t.Fatalf("weak password status=%d body=%s", result.Code, result.Body.String())
	}
	if result := requestReset("Valid-Recovery-Password-2026"); result.Code != http.StatusInternalServerError {
		t.Fatalf("I/O failure status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestCorruptConfigFailsClosedAndRestoresLastGood(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := DefaultConfig()
	lockHash, err := newPasswordHash("RestoreConfigPass1")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Security.LockPasswordHash = lockHash
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE settings SET value = 'null' WHERE key = 'config'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetConfig(); !errors.Is(err, ErrConfigCorrupt) {
		t.Fatalf("corrupt config error=%v, want ErrConfigCorrupt", err)
	}
	restored, err := store.RestoreLastGoodConfig()
	if err != nil {
		t.Fatal(err)
	}
	if restored.Security.LockPasswordHash != lockHash {
		t.Fatalf("restored lock hash=%q", restored.Security.LockPasswordHash)
	}
	if _, err := store.db.Exec(`DELETE FROM settings WHERE key IN ('config', 'config.last_good')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetConfig(); !errors.Is(err, ErrConfigCorrupt) {
		t.Fatalf("missing config error=%v, want ErrConfigCorrupt", err)
	}
	if _, err := store.RestoreLastGoodConfig(); err == nil {
		t.Fatal("missing last-known-good config was silently replaced")
	}
}

func TestStoredConfigAndImportsRejectUnknownJSONFields(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var original string
	if err := store.db.QueryRow(`SELECT CAST(value AS VARCHAR) FROM settings WHERE key = 'config'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	raw := strings.TrimSuffix(original, "}") + `,"unknown":true}`
	if _, err := store.db.Exec(`UPDATE settings SET value = ? WHERE key = 'config'`, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetConfig(); !errors.Is(err, ErrConfigCorrupt) {
		t.Fatalf("config with unknown field error=%v", err)
	}
	var configObject map[string]any
	if err := json.Unmarshal([]byte(original), &configObject); err != nil {
		t.Fatal(err)
	}
	security := configObject["security"].(map[string]any)
	delete(security, "secondaryPasswordHash")
	missingSecurityField, err := json.Marshal(configObject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE settings SET value = ? WHERE key = 'config'`, string(missingSecurityField)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetConfig(); !errors.Is(err, ErrConfigCorrupt) {
		t.Fatalf("config with missing security field error=%v", err)
	}
	if err := json.Unmarshal([]byte(original), &configObject); err != nil {
		t.Fatal(err)
	}
	configObject["security"].(map[string]any)["lockPasswordHash"] = "damaged-hash"
	damagedHash, err := json.Marshal(configObject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE settings SET value = ? WHERE key = 'config'`, string(damagedHash)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetConfig(); !errors.Is(err, ErrConfigCorrupt) {
		t.Fatalf("config with damaged password hash error=%v", err)
	}

	export := ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		Items:         []StoredItem{},
		Groups:        []StoredGroup{},
		ItemGroups:    []ItemGroupLink{},
		Settings:      ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{}}},
	}
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	data = append(bytes.TrimSuffix(data, []byte("}")), []byte(`,"unknown":true}`)...)
	if _, err := decodeImportJSON(data); err == nil {
		t.Fatal("import with unknown field was accepted")
	}
}

func TestStoreRejectsInvalidMetadataAndDatabaseRejectsInvalidTimestamp(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "bad", MetaJSON: "{"}); err == nil {
		t.Fatal("invalid metadata was silently replaced")
	}
	if err := store.Save(context.Background(), ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: "valid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE clipboard_items SET created_at = 'invalid'`); err == nil {
		t.Fatal("BIGINT event-time column accepted an invalid timestamp string")
	}
	if items, _, err := store.Search(context.Background(), SearchOptions{Limit: 10, IncludeSensitive: true}); err != nil || len(items) != 1 {
		t.Fatalf("rejected timestamp update changed valid data: items=%d err=%v", len(items), err)
	}
}

func TestStartupGateSetsRecoveryAndSecondaryPasswordsTogether(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	gate := NewStartupGate(store, dir, startupGateSetCredentials)
	recoveryPassword := "Recovery-Pass-2026"
	secondaryPassword := "Secondary-Pass-2026"

	invalid := []struct {
		name    string
		body    string
		message string
	}{
		{name: "weak recovery", body: `{"password":"short","confirm":"short","secondaryPassword":"` + secondaryPassword + `","secondaryConfirm":"` + secondaryPassword + `"}`, message: "至少需要"},
		{name: "recovery mismatch", body: `{"password":"` + recoveryPassword + `","confirm":"different-recovery","secondaryPassword":"` + secondaryPassword + `","secondaryConfirm":"` + secondaryPassword + `"}`, message: "恢复密码不一致"},
		{name: "weak secondary", body: `{"password":"` + recoveryPassword + `","confirm":"` + recoveryPassword + `","secondaryPassword":"short","secondaryConfirm":"short"}`, message: "至少需要"},
		{name: "secondary mismatch", body: `{"password":"` + recoveryPassword + `","confirm":"` + recoveryPassword + `","secondaryPassword":"` + secondaryPassword + `","secondaryConfirm":"different-secondary"}`, message: "二次密码不一致"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			result := httptest.NewRecorder()
			gate.handleComplete(result, httptest.NewRequest(http.MethodPost, "/api/v1/startup/credentials", bytes.NewBufferString(tt.body)))
			if !strings.Contains(result.Body.String(), tt.message) {
				t.Fatalf("result=%s", result.Body.String())
			}
			if exists, _ := HasRecoveryPassword(dir); exists {
				t.Fatal("invalid credentials created a recovery file")
			}
			cfg, err := store.GetConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Security.SecondaryPasswordHash != "" {
				t.Fatal("invalid credentials persisted a secondary password")
			}
		})
	}

	strong := httptest.NewRequest(http.MethodPost, "/api/v1/startup/credentials", bytes.NewBufferString(`{"password":"`+recoveryPassword+`","confirm":"`+recoveryPassword+`","secondaryPassword":"`+secondaryPassword+`","secondaryConfirm":"`+secondaryPassword+`"}`))
	result := httptest.NewRecorder()
	gate.handleComplete(result, strong)
	if exists, _ := HasRecoveryPassword(dir); !exists || !strings.Contains(result.Body.String(), `"data":{}`) {
		t.Fatalf("combined setup result=%s", result.Body.String())
	}
	if err := VerifyRecoveryPassword(dir, recoveryPassword); err != nil {
		t.Fatalf("verify recovery password: %v", err)
	}
	cfg, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPasswordHash(cfg.Security.SecondaryPasswordHash, secondaryPassword) {
		t.Fatal("secondary password was not persisted")
	}
	select {
	case <-gate.Done():
	case <-time.After(time.Second):
		t.Fatal("combined startup gate did not complete")
	}
}

func TestStartupGatePublishesRecordURLOnce(t *testing.T) {
	gate := NewStartupGate(nil, t.TempDir(), startupGateSetCredentials)
	type statusResponse struct {
		Data struct {
			Ready     bool   `json:"ready"`
			RecordURL string `json:"recordURL"`
		} `json:"data"`
	}
	readStatus := func() statusResponse {
		t.Helper()
		result := httptest.NewRecorder()
		gate.handleStatus(result, httptest.NewRequest(http.MethodGet, "/api/v1/startup/status", nil))
		var status statusResponse
		if err := json.NewDecoder(result.Body).Decode(&status); err != nil {
			t.Fatalf("decode startup status: %v", err)
		}
		return status
	}

	if status := readStatus(); status.Data.Ready || status.Data.RecordURL != "" {
		t.Fatalf("record URL was published before the main server was ready: %+v", status)
	}

	const firstURL = "http://127.0.0.1:15678/#first"
	gate.SetRecordURL(firstURL)
	gate.SetRecordURL("http://127.0.0.1:15678/#second")
	if status := readStatus(); !status.Data.Ready || status.Data.RecordURL != firstURL {
		t.Fatalf("startup status = %+v, want first published URL %q", status, firstURL)
	}
}

func TestSecondaryPasswordCanOnlyBeChanged(t *testing.T) {
	for _, action := range []string{"set", "remove"} {
		t.Run(action, func(t *testing.T) {
			server := &SearchServer{}
			body := `{"type":"secondary","action":"` + action + `","oldPassword":"Secondary-Pass-2026","newPassword":"Replacement-Pass-2026"}`
			result := httptest.NewRecorder()
			server.handleAuthSetPassword(result, httptest.NewRequest(http.MethodPost, "/api/v1/session/password", bytes.NewBufferString(body)))
			if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "secondary_password_required") {
				t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
			}
		})
	}
}

func TestStartupGateRejectsInvalidHostOriginAndCapability(t *testing.T) {
	gate := &StartupGate{token: "startup-token", port: 18766}
	handler := gate.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	tests := []struct {
		name   string
		host   string
		origin string
		token  string
		want   int
	}{
		{name: "valid", host: "127.0.0.1:18766", origin: "http://127.0.0.1:18766", token: "startup-token", want: http.StatusNoContent},
		{name: "invalid host", host: "attacker.example:18766", origin: "http://attacker.example:18766", token: "startup-token", want: http.StatusForbidden},
		{name: "invalid origin", host: "127.0.0.1:18766", origin: "https://attacker.example", token: "startup-token", want: http.StatusForbidden},
		{name: "invalid capability", host: "127.0.0.1:18766", origin: "http://127.0.0.1:18766", token: "wrong", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://"+tt.host+"/api/v1/startup/credentials", nil)
			req.Host = tt.host
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("X-ClipVault-Startup-Token", tt.token)
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, req)
			if result.Code != tt.want {
				t.Fatalf("status=%d, want %d", result.Code, tt.want)
			}
		})
	}
}

func TestStartupGateAcceptsSessionCookieAndRejectsMissingCapability(t *testing.T) {
	gate := &StartupGate{token: "startup-token", port: 18766}
	handler := gate.secureHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func(cookie *http.Cookie) int {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18766/api/v1/startup/credentials", nil)
		req.Header.Set("Origin", "http://127.0.0.1:18766")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, req)
		return result.Code
	}
	if got := request(nil); got != http.StatusForbidden {
		t.Fatalf("missing capability status=%d", got)
	}
	if got := request(&http.Cookie{Name: startupGateCookieName, Value: "startup-token"}); got != http.StatusNoContent {
		t.Fatalf("valid startup session cookie status=%d", got)
	}
}

func TestStartupGateCapabilityURLCompletesWithSessionCookie(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	gate := NewStartupGate(store, dir, startupGateSetCredentials)
	capabilityURL, err := gate.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	safeURL := strings.SplitN(capabilityURL, "#", 2)[0]
	if browserURLFile := os.Getenv("CLIPVAULT_STARTUP_GATE_BROWSER_URL_FILE"); browserURLFile != "" {
		if err := os.WriteFile(browserURLFile, []byte(capabilityURL), 0o600); err != nil {
			t.Fatalf("publish browser test URL: %v", err)
		}
		select {
		case <-gate.Done():
			if !gate.Success() {
				t.Fatal("startup gate completed without success")
			}
		case <-time.After(2 * time.Minute):
			t.Fatal("browser did not complete startup gate")
		}
		gate.SetRecordURL("about:blank")
		time.Sleep(3 * time.Second)
		return
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	indexResponse, err := client.Get(safeURL)
	if err != nil {
		t.Fatalf("open safe startup URL: %v", err)
	}
	indexBody, err := io.ReadAll(indexResponse.Body)
	_ = indexResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if indexResponse.StatusCode != http.StatusOK {
		t.Fatalf("safe startup URL status=%d", indexResponse.StatusCode)
	}
	if len(indexResponse.Cookies()) != 0 {
		t.Fatal("opening the token-free URL must not grant a startup session")
	}
	html := string(indexBody)
	if strings.Count(html, `<form `) != 3 || strings.Count(html, `autocomplete="username"`) != 3 {
		t.Fatalf("startup credential forms are not separated or lack usernames")
	}
	for path, contentType := range map[string]string{
		"startup.css": "text/css; charset=utf-8",
		"startup.js":  "application/javascript; charset=utf-8",
	} {
		response, err := client.Get(safeURL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != contentType {
			t.Fatalf("%s status=%d contentType=%q", path, response.StatusCode, response.Header.Get("Content-Type"))
		}
	}
	for _, path := range []string{"api/status", "api/session", "api/complete"} {
		response, err := client.Get(safeURL + path)
		if err != nil {
			t.Fatalf("get old startup route %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("old startup route %s status=%d, want 404", path, response.StatusCode)
		}
	}

	capability := strings.SplitN(capabilityURL, "#", 2)[1]
	sessionRequest, err := http.NewRequest(http.MethodPost, safeURL+"api/v1/startup/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionRequest.Header.Set("Origin", strings.TrimSuffix(safeURL, "/"))
	sessionRequest.Header.Set("X-ClipVault-Startup-Token", capability)
	sessionResponse, err := client.Do(sessionRequest)
	if err != nil {
		t.Fatalf("establish startup session: %v", err)
	}
	_ = sessionResponse.Body.Close()
	var sessionCookie *http.Cookie
	for _, cookie := range sessionResponse.Cookies() {
		if cookie.Name == startupGateCookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionResponse.StatusCode != http.StatusOK || sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("startup session handshake did not issue an HttpOnly/SameSite=Strict cookie: status=%d cookie=%#v", sessionResponse.StatusCode, sessionCookie)
	}

	body := bytes.NewBufferString(`{"password":"Recovery-Pass-2026","confirm":"Recovery-Pass-2026","secondaryPassword":"Secondary-Pass-2026","secondaryConfirm":"Secondary-Pass-2026"}`)
	request, err := http.NewRequest(http.MethodPost, safeURL+"api/v1/startup/credentials", body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", strings.TrimSuffix(safeURL, "/"))
	completeResponse, err := client.Do(request)
	if err != nil {
		t.Fatalf("complete from safe startup URL: %v", err)
	}
	completeBody, err := io.ReadAll(completeResponse.Body)
	_ = completeResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if completeResponse.StatusCode != http.StatusOK || !strings.Contains(string(completeBody), `"data":{}`) {
		t.Fatalf("complete status=%d body=%s", completeResponse.StatusCode, completeBody)
	}
	select {
	case <-gate.Done():
		if !gate.Success() {
			t.Fatal("startup gate completed without success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup gate did not complete")
	}
}

func TestConfigEndpointCannotReplacePasswordHashes(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Security.LockPasswordHash, err = newPasswordHash("Original-Lock-2026")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Security.SecondaryPasswordHash, err = newPasswordHash("Original-Secondary-2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: store, cfgMgr: manager, auth: NewAuthManager(), logger: logger}

	requestConfig := manager.Get()
	requestConfig.Security.LockPasswordHash, err = newPasswordHash("Injected-Lock-2026")
	if err != nil {
		t.Fatal(err)
	}
	requestConfig.Security.SecondaryPasswordHash, err = newPasswordHash("Injected-Secondary-2026")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(requestConfig)
	if err != nil {
		t.Fatal(err)
	}
	result := httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body)))
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	stored := manager.Get()
	if !verifyPasswordHash(stored.Security.LockPasswordHash, "Original-Lock-2026") {
		t.Fatal("config endpoint replaced the lock password hash")
	}
	if !verifyPasswordHash(stored.Security.SecondaryPasswordHash, "Original-Secondary-2026") {
		t.Fatal("config endpoint replaced the secondary password hash")
	}
}

func TestSensitiveFileRequiresBoundSecondaryAuthorization(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(file, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), ClipboardItem{Kind: "files", Format: "CF_HDROP", Text: file, Sensitive: true}); err != nil {
		t.Fatal(err)
	}
	items, _, _ := store.Search(context.Background(), SearchOptions{Limit: 1, IncludeSensitive: true})
	cfg := DefaultConfig()
	cfg.Security.SecondaryPasswordHash = "configured"
	manager := &ConfigManager{cfg: cfg, store: store}
	auth := NewAuthManager()
	sessionA, _ := auth.CreateSession(10)
	sessionB, _ := auth.CreateSession(10)
	server := &SearchServer{store: store, cfgMgr: manager, auth: auth}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/files/check?item_id="+strconv.FormatInt(items[0].ID, 10)+"&file_index=0", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionA})
	result := httptest.NewRecorder()
	server.handleFileCheck(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("unverified sensitive file status=%d", result.Code)
	}

	oneTime, _ := auth.GenerateSecondaryToken(sessionB)
	request = httptest.NewRequest(http.MethodGet, "/api/v1/files/check?item_id="+strconv.FormatInt(items[0].ID, 10)+"&file_index=0", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionA})
	request.Header.Set("X-ClipVault-Secondary-Token", oneTime)
	result = httptest.NewRecorder()
	server.handleFileCheck(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("cross-session sensitive file status=%d", result.Code)
	}

	oneTime, _ = auth.GenerateSecondaryToken(sessionA)
	request = httptest.NewRequest(http.MethodGet, "/api/v1/files/check?item_id="+strconv.FormatInt(items[0].ID, 10)+"&file_index=0", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionA})
	request.Header.Set("X-ClipVault-Secondary-Token", oneTime)
	result = httptest.NewRecorder()
	server.handleFileCheck(result, request)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"exists":true`) {
		t.Fatalf("authorized sensitive file status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestSensitiveItemMutationsRequireSecondaryAuthorization(t *testing.T) {
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
	cfg := DefaultConfig()
	cfg.Security.SecondaryPasswordHash = "configured"
	auth := NewAuthManager()
	session, err := auth.CreateSession(10)
	if err != nil {
		t.Fatal(err)
	}
	server := &SearchServer{store: store, cfgMgr: &ConfigManager{cfg: cfg, store: store}, auth: auth}

	tests := []struct {
		name   string
		method string
		url    string
		run    func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "star", method: http.MethodPatch,
			url: "/api/v1/items?id=" + strconv.FormatInt(itemID, 10) + "&starred=1",
			run: server.handleStarItem,
		},
		{
			name: "delete", method: http.MethodDelete,
			url: "/api/v1/items?id=" + strconv.FormatInt(itemID, 10),
			run: server.handleItems,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.url, nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			result := httptest.NewRecorder()
			tt.run(result, request)
			if result.Code != http.StatusForbidden {
				t.Fatalf("unauthorized status=%d, want %d: %s", result.Code, http.StatusForbidden, result.Body.String())
			}

			secondaryToken, err := auth.GenerateSecondaryToken(session)
			if err != nil {
				t.Fatal(err)
			}
			request = httptest.NewRequest(tt.method, tt.url, nil)
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
			request.Header.Set("X-ClipVault-Secondary-Token", secondaryToken)
			result = httptest.NewRecorder()
			tt.run(result, request)
			if result.Code != http.StatusOK {
				t.Fatalf("authorized status=%d, want %d: %s", result.Code, http.StatusOK, result.Body.String())
			}
		})
	}
}

func TestMissingFileRecordIsAClientError(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := &SearchServer{store: store}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/files/check?item_id=999&file_index=0", nil)
	result := httptest.NewRecorder()
	server.handleFileCheck(result, request)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("missing file record status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestMissingItemPositionAndMutationsReturnNotFound(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.GetPositionByID(context.Background(), 999); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("missing position error=%v", err)
	}
	if _, err := store.GetByID(context.Background(), 999); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("missing item error=%v", err)
	}
	server := &SearchServer{store: store}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/items/position?id=999", nil),
		httptest.NewRequest(http.MethodPatch, "/api/v1/items?id=999&starred=1", nil),
		httptest.NewRequest(http.MethodDelete, "/api/v1/items?id=999", nil),
	} {
		result := httptest.NewRecorder()
		switch request.Method {
		case http.MethodGet:
			server.handleItemPosition(result, request)
		case http.MethodPatch:
			server.handleStarItem(result, request)
		case http.MethodDelete:
			server.handleItems(result, request)
		}
		if result.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d body=%s", request.Method, request.URL.Path, result.Code, result.Body.String())
		}
	}
}

func TestLocalDateBoundsUseConfiguredTimeZone(t *testing.T) {
	from, before, err := localDateFilterBounds("2026-08-07", "2026-08-07", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	// 边界以 Unix 毫秒表示（2026-08-06T16:00:00Z / 2026-08-07T16:00:00Z）。
	wantFrom := time.Date(2026, 8, 6, 16, 0, 0, 0, time.UTC).UnixMilli()
	wantBefore := time.Date(2026, 8, 7, 16, 0, 0, 0, time.UTC).UnixMilli()
	if from != wantFrom || before != wantBefore {
		t.Fatalf("bounds=(%d,%d)", from, before)
	}
}

func TestExportRangeNormalizesRFC3339OffsetsBeforeSQLComparison(t *testing.T) {
	scope, err := normalizeExportRange(ExportScope{
		Mode: "range",
		From: "2026-08-07T08:00:00+08:00",
		To:   "2026-08-07T02:00:00Z",
	}, "system")
	if err != nil {
		t.Fatal(err)
	}
	if scope.From != "2026-08-07T00:00:00Z" || scope.To != "2026-08-07T02:00:00Z" {
		t.Fatalf("normalized scope=%+v", scope)
	}
	if _, err := normalizeExportRange(ExportScope{
		Mode: "range", From: "2026-08-07", To: "2026-08-08T00:00:00Z",
	}, "Asia/Shanghai"); err == nil {
		t.Fatal("mixed date and RFC3339 range was accepted")
	}
}

func TestConfigRejectsInvalidBackupScopesAndDoesNotPersistInternalBounds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Backup.DirBackup.Scope.Mode = "unknown"
	if err := validateConfig(cfg); err == nil {
		t.Fatal("unknown backup scope was accepted")
	}
	cfg = DefaultConfig()
	cfg.Backup.EmailBackup.Scope = ExportScope{Mode: "range", From: "2026-08-02", To: "2026-08-01"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("reversed backup range was accepted")
	}
	raw, err := json.Marshal(ExportScope{Mode: "incremental", SinceID: 10, MaxID: 20})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sinceId") || strings.Contains(string(raw), "maxId") {
		t.Fatalf("internal export bounds leaked into JSON: %s", raw)
	}
}

func TestCreateGroupRejectsMissingParent(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateGroup(context.Background(), "orphan", 999); err == nil {
		t.Fatal("missing parent was accepted")
	}
}

func TestGroupNamesAreTrimmedAndBounded(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	group, err := store.CreateGroup(ctx, "  project  ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if group.Name != "project" {
		t.Fatalf("group name = %q, want trimmed name", group.Name)
	}
	if _, err := store.CreateGroup(ctx, " \t ", 0); !errors.Is(err, ErrInvalidGroupName) {
		t.Fatalf("blank group name error = %v", err)
	}
	if err := store.UpdateGroup(ctx, group.ID, strings.Repeat("组", maxGroupNameRunes+1), 0); !errors.Is(err, ErrInvalidGroupName) {
		t.Fatalf("oversized group name error = %v", err)
	}
}

func TestStoreNormalizesContentMetadata(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const content = "你好🙂"
	if err := store.Save(ctx, ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: content,
		ByteLen: 1, CharLen: 99, SHA256: "caller-controlled", MetaJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(ctx, SearchOptions{Limit: 1, IncludeSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.ByteLen != len([]byte(content)) || item.CharLen != textRuneLen(content) || item.SHA256 != hashString(content) {
		t.Fatalf("content metadata was not normalized: %+v", item)
	}
}

func TestImportRejectsInvalidItemAndGroupInvariants(t *testing.T) {
	now := time.Now().UTC()
	validItem := StoredItem{
		ID: 1, CreatedAt: now, Kind: "text", Format: "CF_UNICODETEXT", Text: "hello",
		ByteLen: 5, CharLen: 5, SHA256: hashString("hello"), MetaJSON: "{}",
	}

	tests := []struct {
		name   string
		mutate func(*ExportData)
	}{
		{name: "kind and format mismatch", mutate: func(data *ExportData) { data.Items[0].Format = "CF_HDROP" }},
		{name: "content metadata mismatch", mutate: func(data *ExportData) { data.Items[0].SHA256 = "wrong" }},
		{name: "blank group name", mutate: func(data *ExportData) {
			data.Groups = []StoredGroup{{ID: 1, Name: " ", CreatedAt: now}}
		}},
		{name: "oversized group name", mutate: func(data *ExportData) {
			data.Groups = []StoredGroup{{ID: 1, Name: strings.Repeat("a", maxGroupNameRunes+1), CreatedAt: now}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := ExportData{Items: []StoredItem{validItem}}
			test.mutate(&data)
			if err := validateExportData(data); err == nil {
				t.Fatal("invalid import data was accepted")
			}
		})
	}
}

func TestSearchQueryParametersRejectMalformedValues(t *testing.T) {
	for _, rawURL := range []string{
		"/api/v1/items?limit=invalid",
		"/api/v1/items?limit=0",
		"/api/v1/items?limit=501",
		"/api/v1/items?offset=-1",
		"/api/v1/items?source_exe=",
		"/api/v1/items?q=" + strings.Repeat("x", maxSearchQueryBytes+1),
		"/api/v1/items?q=one+two+three+four+five+six+seven+eight+nine",
	} {
		req := httptest.NewRequest(http.MethodGet, rawURL, nil)
		if _, _, _, err := parseSearchParameters(req); err == nil {
			t.Fatalf("malformed query was accepted: %s", rawURL)
		}
	}
	if _, err := parseOptionalPositiveInt64("invalid", "group_id"); err == nil {
		t.Fatal("malformed group id was accepted")
	}
	if _, err := parseOptionalFlag("true", "starred"); err == nil {
		t.Fatal("malformed starred flag was accepted")
	}
}

func TestStoreRejectsSearchQueriesBeyondTermLimit(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(context.Background(), ClipboardItem{
		Kind: "text", Format: "CF_UNICODETEXT", Text: "one two three four five six seven eight",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Search(context.Background(), SearchOptions{
		Query: "one two three four five six seven eight missing", Limit: 10,
	}); err == nil {
		t.Fatal("search silently truncated terms beyond the configured limit")
	}
}

func TestBlankSearchDoesNotRevealSensitiveCount(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, item := range []ClipboardItem{
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "public"},
		{Kind: "text", Format: "CF_UNICODETEXT", Text: "private", Sensitive: true},
	} {
		if err := store.Save(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultConfig()
	cfg.Security.SecondaryPasswordHash = "configured"
	server := &SearchServer{
		store:  store,
		cfgMgr: &ConfigManager{cfg: cfg, store: store},
		auth:   NewAuthManager(),
	}

	result := httptest.NewRecorder()
	server.handleSearch(result, httptest.NewRequest(http.MethodGet, "/api/v1/items?q=%20%20%20", nil))
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var response struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 2 {
		t.Fatalf("blank search total=%d, want the same 2-item result as an unfiltered request", response.Total)
	}
}

func TestRangeBackupDoesNotAdvanceIncrementalWatermark(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	original := BackupWatermark{LastItemID: 7, LastSentAt: "2026-08-01T00:00:00Z", LastFullAt: "2026-07-01T00:00:00Z"}
	if err := store.SetBackupWatermark(context.Background(), "dir", original); err != nil {
		t.Fatal(err)
	}
	got, err := updateBackupWatermark(store, "dir", ExportScope{Mode: "range"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Fatalf("range backup changed incremental watermark: got=%+v want=%+v", got, original)
	}
}

func TestExportEndpointReportsInvalidScopeAsBadRequest(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{store: store, logger: logger}

	for _, rawURL := range []string{
		"/api/v1/transfer/export?format=json&mode=incremental&sinceId=bad",
		"/api/v1/transfer/export?format=json&mode=incremental",
		"/api/v1/transfer/export?format=json&mode=full&sinceId=1",
		"/api/v1/transfer/export?format=json&mode=full&from=2026-08-01&to=2026-08-02",
		"/api/v1/transfer/export?format=json&mode=range&from=2026-08-02&to=2026-08-01",
		"/api/v1/transfer/export?format=json&mode=unknown",
	} {
		w := httptest.NewRecorder()
		server.handleExportData(w, httptest.NewRequest(http.MethodGet, rawURL, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, body=%s", rawURL, w.Code, w.Body.String())
		}
	}
}

func TestCorruptBackupWatermarkStopsIncrementalResolution(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`INSERT INTO settings (key, value) VALUES ('backup.email.watermark', '{"lastItemId":-1}')`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveBackupScope(store, "email", ExportScope{Mode: "incremental"}, 7, false); err == nil {
		t.Fatal("corrupt backup watermark silently degraded to a full backup")
	}
}

func TestForceFullClearsConfiguredRange(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	got, autoFallback, err := resolveBackupScope(store, "dir", ExportScope{
		Mode: "range", From: "2026-08-01T00:00:00Z", To: "2026-08-02T00:00:00Z",
	}, 7, true)
	if err != nil {
		t.Fatalf("force-full range resolution failed: %v", err)
	}
	if autoFallback || got.Mode != "full" || got.From != "" || got.To != "" || got.SinceID != 0 || got.MaxID != 0 {
		t.Fatalf("force-full scope = %+v, autoFallback=%v", got, autoFallback)
	}
	if err := validateExportScope(got); err != nil {
		t.Fatalf("force-full scope is not a valid full export: %v", err)
	}
}

func TestGroupHierarchyRejectsCyclesAndMissingIDs(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root, err := store.CreateGroup(ctx, "root", 0)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateGroup(ctx, "child", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateGroup(ctx, root.ID, "root", child.ID); !errors.Is(err, ErrGroupCycle) {
		t.Fatalf("cycle error=%v", err)
	}
	if err := store.UpdateGroup(ctx, child.ID, "child", 999); !errors.Is(err, ErrGroupParentMissing) {
		t.Fatalf("missing parent error=%v", err)
	}
	if err := store.UpdateGroup(ctx, 999, "missing", 0); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("missing group update error=%v", err)
	}
	if err := store.DeleteGroup(ctx, 999); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("missing group delete error=%v", err)
	}
	if err := store.DeleteGroup(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	groups, err := store.ListGroups(ctx)
	if err != nil || len(groups) != 0 {
		t.Fatalf("groups after recursive delete=%v err=%v", groups, err)
	}
}

func TestXLSXRoundTripPreservesSourceApplication(t *testing.T) {
	export := &ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		Items: []StoredItem{{
			ID: 1, CreatedAt: time.Now().UTC(), Kind: "text", Format: "CF_UNICODETEXT", Text: "hello",
			ByteLen: 5, CharLen: 5, SHA256: hashString("hello"), MetaJSON: "{}", SourceExeName: "editor.exe",
			SourceExePath: `C:\Apps\editor.exe`, SourceWindowTitle: "draft", SourceMethod: "foreground",
			ContentAvailable: true,
		}},
	}
	data, err := encodeExportXLSX(export)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeImportXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.Items[0]
	if got.SourceExeName != "editor.exe" || got.SourceExePath != `C:\Apps\editor.exe` || got.SourceWindowTitle != "draft" || got.SourceMethod != "foreground" {
		t.Fatalf("source metadata was not preserved: %+v", got)
	}
}

func TestEmailPartsRespectFinalEstimatedSize(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 3; i++ {
		text := strings.Repeat(string(rune('a'+i)), 3000)
		if err := store.Save(context.Background(), ClipboardItem{Kind: "text", Format: "CF_UNICODETEXT", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	service := &BackupService{store: store}
	emailCfg := DefaultConfig().Backup.EmailBackup
	emailCfg.Format = "json"
	emailCfg.Password = "part-size-password"
	firstID, err := store.NthIDInScopeAfter(context.Background(), ExportScope{Mode: "full"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	one, err := service.exportEmailPartFile("json", emailCfg.Password, ExportScope{Mode: "full", MaxID: firstID})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(one.path)
	oneSize, err := emailBackupFileMessageSize(emailCfg, "cvexport", time.Unix(0, 0), maxEmailParts-1, maxEmailParts, one.path)
	if err != nil {
		t.Fatal(err)
	}
	limit := int(oneSize) + 64
	emailCfg.MaxPartSize = limit
	parts, _, _, err := service.exportEmailPartFilesSized(emailCfg, ExportScope{Mode: "full"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupEmailPartFiles(parts)
	if len(parts) < 2 {
		t.Fatalf("parts=%d, want adaptive split", len(parts))
	}
	for i, part := range parts {
		size, err := emailBackupFileMessageSize(emailCfg, "cvexport", time.Unix(0, 0), maxEmailParts-1, maxEmailParts, part.path)
		if err != nil {
			t.Fatal(err)
		}
		if size > int64(limit) {
			t.Fatalf("part %d MIME size=%d > %d", i, size, limit)
		}
	}
}
