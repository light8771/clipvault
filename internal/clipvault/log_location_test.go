package clipvault

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestConfigureLogDirectoryAppliesOnNextEnvironment(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	t.Setenv("APPDATA", "")
	t.Setenv("CLIPVAULT_DATA_DIR", "")

	current := filepath.Join(controlRoot, appDirName, "logs")
	target := t.TempDir()
	env := Environment{DataDir: t.TempDir(), LogDir: current, Source: "default"}
	if err := ConfigureLogDirectory(env, target); err != nil {
		t.Fatal(err)
	}
	status := env.StorageStatus()
	if !status.LogConfigurable || !status.LogPending || !samePath(status.PendingLogDir, target) {
		t.Fatalf("storage status after log directory change = %+v", status)
	}

	next, err := newEnvironment(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(next.LogDirectory(), target) {
		t.Fatalf("next log directory = %q, want %q", next.LogDirectory(), target)
	}
	if want := filepath.Join(target, "startup-error.log"); !samePath(StartupErrorLogFile(), want) {
		t.Fatalf("startup error log = %q, want %q", StartupErrorLogFile(), want)
	}
	if next.StorageStatus().LogPending {
		t.Fatalf("applied log directory still reported pending: %+v", next.StorageStatus())
	}

	secondTarget := t.TempDir()
	if err := ConfigureLogDirectory(next, secondTarget); err != nil {
		t.Fatal(err)
	}
	configured, err := configuredLogDirectory(environmentControlDir())
	if err != nil || !samePath(configured, secondTarget) {
		t.Fatalf("updated configured log directory = %q, err=%v", configured, err)
	}
}

func TestConfigureLogDirectoryValidatesTargetAndPortableMode(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	env := Environment{DataDir: t.TempDir(), LogDir: t.TempDir(), Source: "default"}

	if err := ConfigureLogDirectory(env, "relative-logs"); err == nil {
		t.Fatal("relative log directory was accepted")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := ConfigureLogDirectory(env, missing); err == nil {
		t.Fatal("missing log directory was accepted")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureLogDirectory(env, file); err == nil {
		t.Fatal("regular file was accepted as a log directory")
	}
	insideData := filepath.Join(env.DataDir, "logs")
	if err := os.Mkdir(insideData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureLogDirectory(env, insideData); err == nil {
		t.Fatal("log directory inside the data directory was accepted")
	}

	portable := env
	portable.Portable = true
	if err := ConfigureLogDirectory(portable, t.TempDir()); err == nil {
		t.Fatal("portable log directory change was accepted")
	}
	status := portable.StorageStatus()
	if status.LogConfigurable || status.LogReason == "" {
		t.Fatalf("portable log directory status = %+v", status)
	}
}

func TestConfiguredLogDirectoryRejectsUnknownFields(t *testing.T) {
	controlDir := t.TempDir()
	data := []byte(`{"logDir":"C:\\logs","unexpected":true}`)
	if err := os.WriteFile(filepath.Join(controlDir, logLocationFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredLogDirectory(controlDir); err == nil {
		t.Fatal("log directory configuration with unknown fields was accepted")
	}
}

func TestNewEnvironmentFallsBackWhenConfiguredLogDirectoryIsUnavailable(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	t.Setenv("APPDATA", "")
	t.Setenv("CLIPVAULT_DATA_DIR", "")
	controlDir := environmentControlDir()
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing-logs")
	data := []byte(`{"logDir":` + strconv.Quote(missing) + `}`)
	if err := os.WriteFile(filepath.Join(controlDir, logLocationFile), data, 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := newEnvironment(t.TempDir())
	if err != nil {
		t.Fatalf("new environment should fall back instead of failing: %v", err)
	}
	want := filepath.Join(controlRoot, appDirName, "logs")
	if !samePath(env.LogDirectory(), want) || env.LogWarning == "" {
		t.Fatalf("fallback environment = %+v, want log directory %q with warning", env, want)
	}
	if !env.StorageStatus().LogFallback {
		t.Fatalf("fallback was not exposed in storage status: %+v", env.StorageStatus())
	}
}

func TestHandleLogDirectoryConfigure(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	dataDir := t.TempDir()
	logDir := t.TempDir()
	target := t.TempDir()
	logger, err := NewDailyLogger(logDir)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	server := &SearchServer{env: Environment{DataDir: dataDir, LogDir: logDir, Source: "default"}, logger: logger}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/log-directory", bytes.NewBufferString(`{"logDir":`))
	result := httptest.NewRecorder()
	server.handleLogDirectoryConfigure(result, req)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want %d", result.Code, http.StatusBadRequest)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/storage/log-directory", bytes.NewBufferString(`{"logDir":`+strconv.Quote(target)+`}`))
	result = httptest.NewRecorder()
	server.handleLogDirectoryConfigure(result, req)
	if result.Code != http.StatusOK {
		t.Fatalf("configure status = %d, want %d: %s", result.Code, http.StatusOK, result.Body.String())
	}
	configured, err := configuredLogDirectory(environmentControlDir())
	if err != nil || !samePath(configured, target) {
		t.Fatalf("configured log directory = %q, err=%v", configured, err)
	}
}
