package clipvault

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestValidateDirBackupEnabled(t *testing.T) {
	tests := []struct {
		name    string
		cfg     DirBackupConfig
		wantErr bool
	}{
		{name: "complete cvexport", cfg: DirBackupConfig{Interval: 24, Dir: `D:\bak`, Format: "cvexport", Password: "pw"}},
		{name: "complete json no password", cfg: DirBackupConfig{Interval: 24, Dir: `D:\bak`, Format: "json"}},
		{name: "complete xlsx no password", cfg: DirBackupConfig{Interval: 24, Dir: `D:\bak`, Format: "xlsx"}},
		{name: "missing format", cfg: DirBackupConfig{Interval: 24, Dir: `D:\bak`, Format: ""}, wantErr: true},
		{name: "unknown format", cfg: DirBackupConfig{Interval: 24, Dir: `D:\bak`, Format: "csv"}, wantErr: true},
		{name: "missing dir", cfg: DirBackupConfig{Interval: 24, Format: "json"}, wantErr: true},
		{name: "missing interval", cfg: DirBackupConfig{Dir: `D:\bak`, Format: "json"}, wantErr: true},
		{name: "interval above maximum", cfg: DirBackupConfig{Interval: 721, Dir: `D:\bak`, Format: "json"}, wantErr: true},
		{name: "cvexport missing password", cfg: DirBackupConfig{Interval: 24, Dir: `D:\bak`, Format: "cvexport"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validateDirBackupEnabled(tc.cfg)
			if tc.wantErr && got == "" {
				t.Fatal("expected validation error, got empty")
			}
			if !tc.wantErr && got != "" {
				t.Fatalf("expected no error, got %q", got)
			}
		})
	}
}

func TestValidateEmailBackupEnabled(t *testing.T) {
	complete := EmailBackupConfig{
		Interval: 24, SMTPHost: "smtp.example.com", SMTPPort: 587,
		SMTPUser: "user@example.com", SMTPPass: "pass", From: "a@x.com", To: "b@x.com",
		Format: "cvexport", Password: "pw", Subject: "备份 {time}", Body: "文件 {filename}", Style: "card", AccentColor: "#16A34A",
	}
	tests := []struct {
		name    string
		cfg     EmailBackupConfig
		wantErr bool
	}{
		{name: "complete", cfg: complete},
		{name: "missing host", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.SMTPHost = ""; return c }(complete), wantErr: true},
		{name: "missing port", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.SMTPPort = 0; return c }(complete), wantErr: true},
		{name: "invalid port", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.SMTPPort = 70000; return c }(complete), wantErr: true},
		{name: "interval above maximum", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Interval = 721; return c }(complete), wantErr: true},
		{name: "unauthenticated relay", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.SMTPUser = ""; c.SMTPPass = ""; return c }(complete)},
		{name: "username without password", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.SMTPPass = ""; return c }(complete), wantErr: true},
		{name: "password without username", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.SMTPUser = ""; return c }(complete), wantErr: true},
		{name: "username must be an email", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.SMTPUser = "clipvalue"; return c }(complete), wantErr: true},
		{name: "missing from", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.From = ""; return c }(complete), wantErr: true},
		{name: "missing to", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.To = ""; return c }(complete), wantErr: true},
		{name: "invalid from", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.From = "not-an-email"; return c }(complete), wantErr: true},
		{name: "invalid to", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.To = "not-an-email"; return c }(complete), wantErr: true},
		{name: "empty subject", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Subject = ""; return c }(complete), wantErr: true},
		{name: "subject with newline", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Subject = "a\nb"; return c }(complete), wantErr: true},
		{name: "subject too long", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Subject = strings.Repeat("a", 201); return c }(complete), wantErr: true},
		{name: "body too long", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Body = strings.Repeat("a", 5001); return c }(complete), wantErr: true},
		{name: "invalid style", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Style = "custom"; return c }(complete), wantErr: true},
		{name: "invalid accent color", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.AccentColor = "green"; return c }(complete), wantErr: true},
		{name: "missing export password", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Password = ""; return c }(complete), wantErr: true},
		{name: "unknown format", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Format = "csv"; return c }(complete), wantErr: true},
		{name: "json no password ok", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Format = "json"; c.Password = ""; return c }(complete)},
		{name: "xlsx no password ok", cfg: func(c EmailBackupConfig) EmailBackupConfig { c.Format = "xlsx"; c.Password = ""; return c }(complete)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validateEmailBackupEnabled(tc.cfg)
			if tc.wantErr && got == "" {
				t.Fatal("expected validation error, got empty")
			}
			if !tc.wantErr && got != "" {
				t.Fatalf("expected no error, got %q", got)
			}
		})
	}
}

func TestPutConfigRejectsIncompleteBackupWhenEnabled(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()
	server := &SearchServer{cfgMgr: manager, logger: logger}

	// 启用目录备份但缺少目标目录和加密密码
	incomplete := manager.Get()
	incomplete.Backup.DirBackup.Enabled = true
	incomplete.Backup.DirBackup.Interval = 24
	incomplete.Backup.DirBackup.Format = "cvexport"
	incomplete.Backup.DirBackup.Dir = ""
	body, err := json.Marshal(incomplete)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result := httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("incomplete dir backup status = %d, body=%s", result.Code, result.Body.String())
	}

	// 启用邮件备份但缺少 SMTP 配置
	incomplete = manager.Get()
	incomplete.Backup.EmailBackup.Enabled = true
	incomplete.Backup.EmailBackup.Interval = 24
	incomplete.Backup.EmailBackup.SMTPHost = ""
	body, err = json.Marshal(incomplete)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("incomplete email backup status = %d, body=%s", result.Code, result.Body.String())
	}

	// 邮件字段齐全但认证信息不成对时，后端也必须拒绝启用。
	incomplete = manager.Get()
	incomplete.Backup.EmailBackup = EmailBackupConfig{
		Enabled: true, Interval: 24, SMTPHost: "smtp.example.com", SMTPPort: 587,
		SMTPUser: "user@example.com", From: "from@example.com", To: "to@example.com", Format: "json",
	}
	body, err = json.Marshal(incomplete)
	if err != nil {
		t.Fatalf("marshal incomplete SMTP authentication: %v", err)
	}
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("incomplete SMTP authentication status = %d, body=%s", result.Code, result.Body.String())
	}

	invalidUser := manager.Get()
	invalidUser.Backup.EmailBackup = DefaultConfig().Backup.EmailBackup
	invalidUser.Backup.EmailBackup.Enabled = true
	invalidUser.Backup.EmailBackup.Interval = 24
	invalidUser.Backup.EmailBackup.SMTPHost = "smtp.example.com"
	invalidUser.Backup.EmailBackup.SMTPPort = 587
	invalidUser.Backup.EmailBackup.SMTPUser = "clipvalue"
	invalidUser.Backup.EmailBackup.SMTPPass = "smtp-password"
	invalidUser.Backup.EmailBackup.From = "from@example.com"
	invalidUser.Backup.EmailBackup.To = "to@example.com"
	invalidUser.Backup.EmailBackup.Format = "json"
	body, err = json.Marshal(invalidUser)
	if err != nil {
		t.Fatalf("marshal invalid SMTP username: %v", err)
	}
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "SMTP 用户名必须为邮箱") {
		t.Fatalf("invalid SMTP username status = %d, body=%s", result.Code, result.Body.String())
	}
}

func TestPutConfigAcceptsCompleteBackupWhenEnabled(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()
	server := &SearchServer{cfgMgr: manager, logger: logger}

	complete := manager.Get()
	complete.Backup.DirBackup.Enabled = true
	complete.Backup.DirBackup.Interval = 24
	complete.Backup.DirBackup.Dir = t.TempDir()
	complete.Backup.DirBackup.Format = "cvexport"
	complete.Backup.DirBackup.Password = "export-pw"
	complete.Backup.EmailBackup.Enabled = true
	complete.Backup.EmailBackup.Interval = 24
	complete.Backup.EmailBackup.SMTPHost = "smtp.example.com"
	complete.Backup.EmailBackup.SMTPPort = 587
	complete.Backup.EmailBackup.SMTPUser = "user@example.com"
	complete.Backup.EmailBackup.SMTPPass = "smtp-pw"
	complete.Backup.EmailBackup.From = "a@x.com"
	complete.Backup.EmailBackup.To = "b@x.com"
	complete.Backup.EmailBackup.Format = "cvexport"
	complete.Backup.EmailBackup.Password = "export-pw"
	invalidTemplate := complete
	invalidTemplate.Backup.EmailBackup.Style = "custom"
	body, err := json.Marshal(invalidTemplate)
	if err != nil {
		t.Fatalf("marshal invalid email template: %v", err)
	}
	result := httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid email template status = %d, body=%s", result.Code, result.Body.String())
	}
	body, err = json.Marshal(complete)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusOK {
		t.Fatalf("complete backup status = %d, body=%s", result.Code, result.Body.String())
	}

	// 已认证的 SMTP 配置可以切换为无认证 relay，并清除已保存的密码。
	noAuthRelay := manager.Get()
	noAuthRelay.Backup.EmailBackup.SMTPUser = ""
	noAuthRelay.Backup.EmailBackup.SMTPPass = "__clear__"
	body, err = json.Marshal(noAuthRelay)
	if err != nil {
		t.Fatalf("marshal unauthenticated relay: %v", err)
	}
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusOK {
		t.Fatalf("unauthenticated relay status = %d, body=%s", result.Code, result.Body.String())
	}
	if got := manager.Get().Backup.EmailBackup.SMTPPass; got != "" {
		t.Fatalf("unauthenticated relay SMTP password = %q, want empty", got)
	}
}

// TestPutConfigClearsBackupPassword 验证 "__clear__" 哨兵能清除已保存的加密密码，
// 而 "__set__" / 空值保留原密码。
func TestPutConfigClearsBackupPassword(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()
	server := &SearchServer{cfgMgr: manager, logger: logger}

	// 1) 先保存一份带密码的目录备份配置（cvexport + password）
	seed := manager.Get()
	seed.Backup.DirBackup.Enabled = true
	seed.Backup.DirBackup.Interval = 24
	seed.Backup.DirBackup.Dir = t.TempDir()
	seed.Backup.DirBackup.Format = "cvexport"
	seed.Backup.DirBackup.Password = "secret-pw"
	body, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result := httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusOK {
		t.Fatalf("seed save status = %d, body=%s", result.Code, result.Body.String())
	}
	if got := manager.Get().Backup.DirBackup.Password; got != "secret-pw" {
		t.Fatalf("seed password = %q, want %q", got, "secret-pw")
	}

	// 2) 发送 "__set__" 应保留原密码
	keep := manager.Get()
	keep.Backup.DirBackup.Password = "__set__"
	body, _ = json.Marshal(keep)
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusOK {
		t.Fatalf("keep save status = %d, body=%s", result.Code, result.Body.String())
	}
	if got := manager.Get().Backup.DirBackup.Password; got != "secret-pw" {
		t.Fatalf("after __set__ password = %q, want %q", got, "secret-pw")
	}

	// 3) 发送空字符串也应保留原密码
	keep = manager.Get()
	keep.Backup.DirBackup.Password = ""
	body, _ = json.Marshal(keep)
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusOK {
		t.Fatalf("empty save status = %d, body=%s", result.Code, result.Body.String())
	}
	if got := manager.Get().Backup.DirBackup.Password; got != "secret-pw" {
		t.Fatalf("after empty password = %q, want %q", got, "secret-pw")
	}

	// 4) 发送 "__clear__" 应清除密码；切到 json 格式以避免校验报错
	clear := manager.Get()
	clear.Backup.DirBackup.Format = "json"
	clear.Backup.DirBackup.Password = "__clear__"
	body, _ = json.Marshal(clear)
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusOK {
		t.Fatalf("clear save status = %d, body=%s", result.Code, result.Body.String())
	}
	if got := manager.Get().Backup.DirBackup.Password; got != "" {
		t.Fatalf("after __clear__ password = %q, want empty", got)
	}

	// 5) cvexport 启用时发送 "__clear__" 应被校验拦截（加密格式不能没有密码）
	reject := manager.Get()
	reject.Backup.DirBackup.Format = "cvexport"
	reject.Backup.DirBackup.Enabled = true
	reject.Backup.DirBackup.Password = "__clear__"
	body, _ = json.Marshal(reject)
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusBadRequest {
		t.Fatalf("cvexport clear should be rejected, status = %d, body=%s", result.Code, result.Body.String())
	}

	// 6) 新密码值应直接替换旧密码
	replace := manager.Get()
	replace.Backup.DirBackup.Format = "cvexport"
	replace.Backup.DirBackup.Enabled = true
	replace.Backup.DirBackup.Password = "brand-new-pw"
	body, _ = json.Marshal(replace)
	result = httptest.NewRecorder()
	server.handlePutConfig(result, httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body)))
	if result.Code != http.StatusOK {
		t.Fatalf("replace save status = %d, body=%s", result.Code, result.Body.String())
	}
	if got := manager.Get().Backup.DirBackup.Password; got != "brand-new-pw" {
		t.Fatalf("after replace password = %q, want %q", got, "brand-new-pw")
	}
}

func TestIsBackupFormat(t *testing.T) {
	for _, format := range []string{"cvexport", "json", "xlsx"} {
		if !isBackupFormat(format) {
			t.Fatalf("isBackupFormat(%q) = false, want true", format)
		}
	}
	for _, format := range []string{"", "csv", "excel"} {
		if isBackupFormat(format) {
			t.Fatalf("isBackupFormat(%q) = true, want false", format)
		}
	}
}

func TestBackupTemplateDefaults(t *testing.T) {
	backup := DefaultConfig().Backup.EmailBackup
	if backup.RunOnStartup {
		t.Fatal("email startup backup must be disabled by default")
	}
	if errors := emailTemplateErrors(backup); len(errors) > 0 {
		t.Fatalf("default email template errors = %v", errors)
	}
	if DefaultConfig().Backup.DirBackup.RunOnStartup {
		t.Fatal("directory startup backup must be disabled by default")
	}
}

func TestResolveSecretField(t *testing.T) {
	tests := []struct {
		name, newVal, curVal, want string
	}{
		{"empty keeps current", "", "secret", "secret"},
		{"set sentinel keeps current", "__set__", "secret", "secret"},
		{"clear sentinel empties", "__clear__", "secret", ""},
		{"clear on empty stays empty", "__clear__", "", ""},
		{"new value replaces", "new-pw", "old-pw", "new-pw"},
		{"set on empty stays empty", "__set__", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSecretField(tc.newVal, tc.curVal)
			if got != tc.want {
				t.Fatalf("resolveSecretField(%q, %q) = %q, want %q", tc.newVal, tc.curVal, got, tc.want)
			}
		})
	}
}
