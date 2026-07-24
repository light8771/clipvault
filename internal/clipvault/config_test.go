package clipvault

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotificationsDefault(t *testing.T) {
	if !DefaultConfig().Notifications.PauseResume {
		t.Fatal("pause/resume notifications must be enabled by default")
	}
}

func TestNotificationContentIsNormalized(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notifications = NotificationConfig{
		PauseTitle:  "  暂停  ",
		PauseBody:   string(make([]rune, 241)),
		ResumeTitle: "  恢复  ",
		ResumeBody:  string(make([]rune, 241)),
	}
	normalizeConfig(&cfg)
	if cfg.Notifications.PauseTitle != "暂停" || cfg.Notifications.ResumeTitle != "恢复" {
		t.Fatalf("notification titles = %#v", cfg.Notifications)
	}
	if len([]rune(cfg.Notifications.PauseBody)) != 240 || len([]rune(cfg.Notifications.ResumeBody)) != 240 {
		t.Fatalf("notification body length = %#v", cfg.Notifications)
	}
}

func TestNotificationsCanBeDisabledAndPersisted(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notifications = NotificationConfig{
		PauseResume: false,
		PauseTitle:  "暂停标题",
		PauseBody:   "暂停内容",
		ResumeTitle: "恢复标题",
		ResumeBody:  "恢复内容",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var persisted Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshal persisted config: %v", err)
	}
	if persisted.Notifications.PauseResume {
		t.Fatal("persisted notification setting must remain disabled")
	}
	if persisted.Notifications.PauseTitle != cfg.Notifications.PauseTitle ||
		persisted.Notifications.PauseBody != cfg.Notifications.PauseBody ||
		persisted.Notifications.ResumeTitle != cfg.Notifications.ResumeTitle ||
		persisted.Notifications.ResumeBody != cfg.Notifications.ResumeBody {
		t.Fatalf("persisted notification content = %#v", persisted.Notifications)
	}
}

func TestAssetVersionsUseJSONStrings(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Brand.LogoVersion = 1750000000000000123
	cfg.Brand.FaviconVersion = 1750000000000000456
	cfg.Background.Version = 1750000000000000789

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	for _, version := range [][]byte{
		[]byte(`"logoVersion":"1750000000000000123"`),
		[]byte(`"faviconVersion":"1750000000000000456"`),
		[]byte(`"version":"1750000000000000789"`),
	} {
		if !bytes.Contains(data, version) {
			t.Fatalf("asset version is not an exact JSON string: %s", data)
		}
	}

	var restored Config
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if restored.Brand.LogoVersion != cfg.Brand.LogoVersion ||
		restored.Brand.FaviconVersion != cfg.Brand.FaviconVersion ||
		restored.Background.Version != cfg.Background.Version {
		t.Fatalf("asset versions changed after JSON round trip: %#v", restored)
	}
}

func TestBackgroundSizingIsNormalized(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Background.Fit = "invalid"
	cfg.Background.Scale = 12
	normalizeConfig(&cfg)
	if cfg.Background.Fit != "cover" || cfg.Background.Scale != 25 {
		t.Fatalf("normalized background = %#v", cfg.Background)
	}

	cfg.Background.Fit = "contain"
	cfg.Background.Scale = 240
	normalizeConfig(&cfg)
	if cfg.Background.Fit != "contain" || cfg.Background.Scale != 200 {
		t.Fatalf("normalized background = %#v", cfg.Background)
	}
}

func TestPutConfigPreservesAssetMetadata(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	current := manager.Get()
	current.Brand.LogoContentType = "image/png"
	current.Brand.LogoVersion = 10
	current.Brand.FaviconContentType = "image/x-icon"
	current.Brand.FaviconVersion = 11
	current.Background.ContentType = "video/mp4"
	current.Background.Version = 12
	if err := manager.Update(current); err != nil {
		t.Fatalf("seed asset metadata: %v", err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()
	server := &SearchServer{cfgMgr: manager, logger: logger}

	incoming := manager.Get()
	incoming.Brand.Title = "Updated title"
	incoming.Brand.LogoContentType = "image/jpeg"
	incoming.Brand.LogoVersion = 100
	incoming.Brand.FaviconContentType = "image/png"
	incoming.Brand.FaviconVersion = 101
	incoming.Background.ContentType = "image/png"
	incoming.Background.Version = 102
	body, err := json.Marshal(incoming)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	result := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	server.handlePutConfig(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("config update status = %d, want %d: %s", result.Code, http.StatusOK, result.Body.String())
	}
	updated := manager.Get()
	if updated.Brand.Title != "Updated title" ||
		updated.Brand.LogoVersion != current.Brand.LogoVersion ||
		updated.Brand.FaviconVersion != current.Brand.FaviconVersion ||
		updated.Background.Version != current.Background.Version ||
		updated.Brand.LogoContentType != current.Brand.LogoContentType ||
		updated.Brand.FaviconContentType != current.Brand.FaviconContentType ||
		updated.Background.ContentType != current.Background.ContentType {
		t.Fatalf("asset metadata was modified through general config: %#v", updated)
	}
}

func TestInvalidSensitiveRulesAreRejectedBeforeConfigPersistence(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}

	valid := manager.Get()
	valid.Sensitive.CustomPatterns = []string{"^before-update$"}
	if err := manager.Update(valid); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	invalid := manager.Get()
	invalid.Sensitive.CustomPatterns = []string{"[invalid"}
	if err := manager.Update(invalid); err == nil {
		t.Fatal("invalid sensitive rule was accepted")
	}
	current := manager.Get()
	if len(current.Sensitive.CustomPatterns) != 1 || current.Sensitive.CustomPatterns[0] != "^before-update$" {
		t.Fatalf("invalid rule changed in-memory config: %#v", current.Sensitive)
	}
	persisted, err := store.GetConfig()
	if err != nil {
		t.Fatalf("get persisted config: %v", err)
	}
	if len(persisted.Sensitive.CustomPatterns) != 1 || persisted.Sensitive.CustomPatterns[0] != "^before-update$" {
		t.Fatalf("invalid rule changed persisted config: %#v", persisted.Sensitive)
	}

	body, err := json.Marshal(invalid)
	if err != nil {
		t.Fatalf("marshal invalid config: %v", err)
	}
	server := &SearchServer{cfgMgr: manager}
	result := httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid sensitive rule status = %d, body=%s", result.Code, result.Body.String())
	}
}
