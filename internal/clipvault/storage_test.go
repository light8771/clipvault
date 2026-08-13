package clipvault

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
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

func writeStorageKey(t *testing.T, dir, value string) {
	t.Helper()
	if err := os.MkdirAll(activeDatabaseDirectory(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeDatabaseFile(dir, databaseKeyFileName), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustStorageMoveJSON(t *testing.T, source, target string) []byte {
	t.Helper()
	digest, err := storageKeyDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	return mustStorageMoveJSONWithDigest(t, source, target, digest)
}

func mustStorageMoveJSONWithDigest(t *testing.T, source, target, digest string) []byte {
	t.Helper()
	move, err := json.Marshal(storageMove{SourceDir: source, TargetDir: target, SourceKeyDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	return move
}

func TestPrepareEnvironmentForRunAppliesDataDirectoryMove(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	source := t.TempDir()
	target := t.TempDir()
	writeStorageKey(t, source, "key")
	if err := os.Mkdir(filepath.Join(source, "license"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "license", "clipvault.lic"), []byte("license"), 0o600); err != nil {
		t.Fatal(err)
	}
	isolatedID := "20260810-120000.000000000"
	isolatedFile := databaseGroupFile(isolatedDatabaseDirectory(source, isolatedID), databaseFileName)
	if err := os.MkdirAll(filepath.Dir(isolatedFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(isolatedFile, []byte("isolated database"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := Environment{DataDir: source, Source: "default"}
	if err := ScheduleDataDirectoryMove(env, target); err != nil {
		t.Fatal(err)
	}
	controlDir := environmentControlDir()
	prepared, warning, err := prepareEnvironmentForRun(env)
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" {
		t.Fatalf("unexpected relocation warning: %s", warning)
	}
	if !samePath(prepared.DataDir, target) || prepared.Source != "configured" {
		t.Fatalf("prepared environment = %+v", prepared)
	}
	if data, err := os.ReadFile(activeDatabaseFile(target, databaseKeyFileName)); err != nil || string(data) != "key" {
		t.Fatalf("moved key = %q, %v", data, err)
	}
	if _, err := os.Stat(activeDatabaseFile(source, databaseKeyFileName)); !os.IsNotExist(err) {
		t.Fatalf("source key should be removed, got %v", err)
	}
	movedIsolatedFile := databaseGroupFile(isolatedDatabaseDirectory(target, isolatedID), databaseFileName)
	if data, err := os.ReadFile(movedIsolatedFile); err != nil || string(data) != "isolated database" {
		t.Fatalf("moved isolated database = %q, %v", data, err)
	}
	if _, err := os.Stat(isolatedFile); !os.IsNotExist(err) {
		t.Fatalf("source isolated database should be removed, got %v", err)
	}
	configured, err := configuredDataDirectory(controlDir)
	if err != nil || !samePath(configured, target) {
		t.Fatalf("configured directory = %q, %v", configured, err)
	}
}

func TestPrepareEnvironmentForRunMovesClosedDatabase(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	source := t.TempDir()
	target := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: source})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Save(ctx, ClipboardItem{
		Kind:     "text",
		Format:   "CF_UNICODETEXT",
		Text:     "migrated database record",
		ByteLen:  24,
		CharLen:  24,
		SHA256:   "migrated-database-record",
		MetaJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	env := Environment{DataDir: source, Source: "default"}
	if err := ScheduleDataDirectoryMove(env, target); err != nil {
		t.Fatal(err)
	}
	prepared, warning, err := prepareEnvironmentForRun(env)
	if err != nil || warning != "" || !samePath(prepared.DataDir, target) {
		t.Fatalf("prepared=%+v warning=%q err=%v", prepared, warning, err)
	}
	migrated, err := OpenStore(StoreOptions{DataDir: prepared.DataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	items, total, err := migrated.Search(ctx, SearchOptions{Query: "migrated database record", Limit: 10})
	if err != nil || total != 1 || len(items) != 1 || items[0].Text != "migrated database record" {
		t.Fatalf("migrated search = items=%#v total=%d err=%v", items, total, err)
	}
}

func TestPrepareEnvironmentForRunCanReturnToControlDirectory(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	controlDir := environmentControlDir()
	if err := os.MkdirAll(filepath.Join(controlDir, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, "logs", "existing.log"), []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	writeStorageKey(t, source, "key")
	location, err := json.Marshal(storageLocation{DataDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(filepath.Join(controlDir, storageLocationFile), location); err != nil {
		t.Fatal(err)
	}

	env := Environment{DataDir: source, Source: "configured"}
	if err := ScheduleDataDirectoryMove(env, controlDir); err != nil {
		t.Fatal(err)
	}
	prepared, warning, err := prepareEnvironmentForRun(env)
	if err != nil || warning != "" || !samePath(prepared.DataDir, controlDir) {
		t.Fatalf("prepared=%+v warning=%q err=%v", prepared, warning, err)
	}
	if _, err := os.Stat(filepath.Join(controlDir, "logs", "existing.log")); err != nil {
		t.Fatalf("control logs were not retained: %v", err)
	}
	if data, err := os.ReadFile(activeDatabaseFile(controlDir, databaseKeyFileName)); err != nil || string(data) != "key" {
		t.Fatalf("moved key = %q, %v", data, err)
	}
}

func TestNewEnvironmentUsesConfiguredDirectoryAfterExplicitOverrides(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	t.Setenv("APPDATA", "")
	configuredDir := t.TempDir()
	if err := os.MkdirAll(environmentControlDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	location, err := json.Marshal(storageLocation{DataDir: configuredDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(filepath.Join(environmentControlDir(), storageLocationFile), location); err != nil {
		t.Fatal(err)
	}

	commandDir := t.TempDir()
	env, err := newEnvironment(commandDir)
	if err != nil || !samePath(env.DataDir, commandDir) || env.Source != "command" {
		t.Fatalf("command override environment = %+v, err=%v", env, err)
	}

	environmentDir := t.TempDir()
	t.Setenv("CLIPVAULT_DATA_DIR", environmentDir)
	env, err = newEnvironment("")
	if err != nil || !samePath(env.DataDir, environmentDir) || env.Source != "environment" {
		t.Fatalf("environment override environment = %+v, err=%v", env, err)
	}

	t.Setenv("CLIPVAULT_DATA_DIR", "")
	env, err = newEnvironment("")
	if err != nil || !samePath(env.DataDir, configuredDir) || env.Source != "configured" {
		t.Fatalf("configured environment = %+v, err=%v", env, err)
	}
}

func TestNewEnvironmentRejectsRelativeDirectoryOverrides(t *testing.T) {
	t.Setenv("CLIPVAULT_DATA_DIR", "relative-data")
	if _, err := newEnvironment(""); err == nil {
		t.Fatal("relative CLIPVAULT_DATA_DIR was accepted")
	}
	t.Setenv("CLIPVAULT_DATA_DIR", "")
	if _, err := newEnvironment("relative-data"); err == nil {
		t.Fatal("relative data directory override was accepted")
	}
}

func TestConfiguredDataDirectoryRejectsNonAbsolutePath(t *testing.T) {
	for _, dataDir := range []string{"", "   ", "relative-data", "  relative-data  "} {
		t.Run(strconv.Quote(dataDir), func(t *testing.T) {
			controlDir := t.TempDir()
			data, err := json.Marshal(storageLocation{DataDir: dataDir})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(controlDir, storageLocationFile), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := configuredDataDirectory(controlDir); err == nil {
				t.Fatalf("configured relative path %q was accepted", dataDir)
			}
		})
	}
}

func TestPendingDataDirectoryMoveRejectsNonAbsolutePaths(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		target string
	}{
		{name: "empty source", source: "", target: t.TempDir()},
		{name: "empty target", source: t.TempDir(), target: ""},
		{name: "source", source: "relative-source", target: t.TempDir()},
		{name: "target", source: t.TempDir(), target: "relative-target"},
	} {
		t.Run(test.name, func(t *testing.T) {
			controlDir := t.TempDir()
			if filepath.IsAbs(test.source) {
				if err := os.WriteFile(filepath.Join(test.source, "source.txt"), []byte("source"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			move, err := json.Marshal(storageMove{SourceDir: test.source, TargetDir: test.target})
			if err != nil {
				t.Fatal(err)
			}
			pendingPath := filepath.Join(controlDir, storageMoveFile)
			if err := os.WriteFile(pendingPath, move, 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := applyPendingDataDirectoryMove(controlDir); err == nil {
				t.Fatalf("pending move with invalid %s path was accepted", test.name)
			}
			if _, err := os.Stat(pendingPath); err != nil {
				t.Fatalf("invalid pending move should remain for manual repair: %v", err)
			}
			if filepath.IsAbs(test.source) {
				if data, err := os.ReadFile(filepath.Join(test.source, "source.txt")); err != nil || string(data) != "source" {
					t.Fatalf("source data changed: %q, %v", data, err)
				}
			}
			if filepath.IsAbs(test.target) {
				entries, err := os.ReadDir(test.target)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Fatalf("target data changed: %v", entries)
				}
			}
		})
	}
}

func TestPortableFlagRequiresRegularFile(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "portable.flag")
	if err := os.Mkdir(flag, 0o700); err != nil {
		t.Fatal(err)
	}
	if isPortableFlag(flag) {
		t.Fatal("portable.flag directory enabled portable mode")
	}
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !isPortableFlag(flag) {
		t.Fatal("portable.flag regular file did not enable portable mode")
	}
	link := filepath.Join(t.TempDir(), "portable-link.flag")
	if err := os.Symlink(flag, link); err == nil {
		// Only assert when the link truly has symlink semantics; some
		// environments report success but create a non-link entry.
		if info, statErr := os.Lstat(link); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			if isPortableFlag(link) {
				t.Fatal("portable.flag symlink enabled portable mode")
			}
		}
	}
}

func TestAtomicWriteStorageControlFileReplacesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := atomicWriteStorageControlFile(path, []byte("new"))
	if err != nil || !written {
		t.Fatalf("atomic write = written %t, error %v", written, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new" {
		t.Fatalf("written data = %q, error %v", data, err)
	}
}

func TestScheduleDataDirectoryMoveRejectsNonEmptyTarget(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	source := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleDataDirectoryMove(Environment{DataDir: source, Source: "default"}, target); err == nil {
		t.Fatal("expected non-empty target to be rejected")
	}
}

func TestScheduleDataDirectoryMoveRejectsUserFileInControlDirectory(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	controlDir := environmentControlDir()
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, "user-file.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleDataDirectoryMove(Environment{DataDir: t.TempDir(), Source: "configured"}, controlDir); err == nil {
		t.Fatal("expected control directory with a user file to be rejected")
	}
}

func TestVerifyDirectoryWritableLeavesNoProbeFiles(t *testing.T) {
	dir := t.TempDir()
	if err := verifyDirectoryWritable(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("permission check left entries behind: %v", entries)
	}
}

func TestScheduleDataDirectoryMoveRejectsExistingPendingMove(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	source := t.TempDir()
	firstTarget := t.TempDir()
	writeStorageKey(t, source, "source")
	if err := ScheduleDataDirectoryMove(Environment{DataDir: source, Source: "default"}, firstTarget); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleDataDirectoryMove(Environment{DataDir: source, Source: "default"}, t.TempDir()); err == nil {
		t.Fatal("expected existing pending move to be rejected")
	}
}

func TestScheduleDataDirectoryMoveAllowsOnlyOnePendingMove(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	source := t.TempDir()
	targets := []string{t.TempDir(), t.TempDir()}
	env := Environment{DataDir: source, Source: "default"}

	writeStorageKey(t, source, "source")
	results := make(chan error, len(targets))
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- ScheduleDataDirectoryMove(env, target)
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful schedules = %d, want 1", successes)
	}
}

func TestStorageStatusReportsPendingMove(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	source := t.TempDir()
	target := t.TempDir()
	env := Environment{DataDir: source, Source: "default"}
	writeStorageKey(t, source, "source")
	if err := ScheduleDataDirectoryMove(env, target); err != nil {
		t.Fatal(err)
	}
	if status := env.StorageStatus(); !status.Movable || !status.Pending {
		t.Fatalf("storage status = %#v", status)
	}
}

func TestPendingMoveWithActiveTargetOnlyFinalizesCleanup(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	controlDir := environmentControlDir()
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	target := t.TempDir()
	writeStorageKey(t, source, "old")
	writeStorageKey(t, target, "new")
	location, err := json.Marshal(storageLocation{DataDir: target, CleanupSourceDir: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(filepath.Join(controlDir, storageLocationFile), location); err != nil {
		t.Fatal(err)
	}
	move := mustStorageMoveJSON(t, source, target)
	pendingPath := filepath.Join(controlDir, storageMoveFile)
	if err := atomicWriteFile(pendingPath, move); err != nil {
		t.Fatal(err)
	}

	warning, err := applyPendingDataDirectoryMove(controlDir)
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" {
		t.Fatalf("unexpected relocation warning: %s", warning)
	}
	if data, err := os.ReadFile(activeDatabaseFile(target, databaseKeyFileName)); err != nil || string(data) != "new" {
		t.Fatalf("target data changed during finalization: %q, %v", data, err)
	}
	if _, err := os.Stat(activeDatabaseFile(source, databaseKeyFileName)); !os.IsNotExist(err) {
		t.Fatalf("source data should be removed, got %v", err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending move should be removed, got %v", err)
	}
	completedLocation, exists, err := configuredStorageLocation(controlDir)
	if err != nil || !exists || completedLocation.CleanupSourceDir != "" {
		t.Fatalf("completed storage location = %#v, exists %t, error %v", completedLocation, exists, err)
	}
}

func TestPendingMoveWithNonEmptyTargetCancelsWithoutDeletingData(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	controlDir := environmentControlDir()
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	target := t.TempDir()
	writeStorageKey(t, source, "source")
	if err := os.WriteFile(filepath.Join(target, "unrelated.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	move := mustStorageMoveJSON(t, source, target)
	pendingPath := filepath.Join(controlDir, storageMoveFile)
	if err := atomicWriteFile(pendingPath, move); err != nil {
		t.Fatal(err)
	}

	prepared, warning, err := prepareEnvironmentForRun(Environment{DataDir: source, Source: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(prepared.DataDir, source) || warning == "" {
		t.Fatalf("prepared=%+v warning=%q", prepared, warning)
	}
	if data, err := os.ReadFile(activeDatabaseFile(source, databaseKeyFileName)); err != nil || string(data) != "source" {
		t.Fatalf("source data changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "unrelated.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("target data changed: %q, %v", data, err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending move should be removed, got %v", err)
	}
}

func TestPendingMoveWithConfiguredTargetAndMissingSourceCompletesCleanup(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	controlDir := environmentControlDir()
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	writeStorageKey(t, target, "target")
	missingSource := filepath.Join(t.TempDir(), "missing")
	location, err := json.Marshal(storageLocation{DataDir: target, CleanupSourceDir: missingSource})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(filepath.Join(controlDir, storageLocationFile), location); err != nil {
		t.Fatal(err)
	}
	move := mustStorageMoveJSONWithDigest(t, missingSource, target, strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(controlDir, storageMoveFile)
	if err := atomicWriteFile(pendingPath, move); err != nil {
		t.Fatal(err)
	}

	warning, err := applyPendingDataDirectoryMove(controlDir)
	if err != nil || warning == "" {
		t.Fatalf("warning=%q err=%v", warning, err)
	}
	if data, err := os.ReadFile(activeDatabaseFile(target, databaseKeyFileName)); err != nil || string(data) != "target" {
		t.Fatalf("target data changed: %q, %v", data, err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending move should be removed, got %v", err)
	}
}

func TestPendingMoveRejectsMismatchedCommittedCleanupSource(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	controlDir := environmentControlDir()
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	committedSource := t.TempDir()
	untrustedSource := t.TempDir()
	target := t.TempDir()
	writeStorageKey(t, committedSource, "committed")
	writeStorageKey(t, untrustedSource, "untrusted")
	if err := os.WriteFile(filepath.Join(untrustedSource, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStorageKey(t, target, "target")
	location, err := json.Marshal(storageLocation{DataDir: target, CleanupSourceDir: committedSource})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(filepath.Join(controlDir, storageLocationFile), location); err != nil {
		t.Fatal(err)
	}
	move := mustStorageMoveJSONWithDigest(t, untrustedSource, target, func() string {
		digest, err := storageKeyDigest(committedSource)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}())
	if err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(controlDir, storageMoveFile)
	if err := atomicWriteFile(pendingPath, move); err != nil {
		t.Fatal(err)
	}

	if _, err := applyPendingDataDirectoryMove(controlDir); err == nil {
		t.Fatal("mismatched cleanup source was accepted")
	}
	if data, err := os.ReadFile(filepath.Join(untrustedSource, "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("untrusted source changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(activeDatabaseFile(committedSource, databaseKeyFileName)); err != nil || string(data) != "committed" {
		t.Fatalf("committed source changed: %q, %v", data, err)
	}
	if _, err := os.Stat(pendingPath); err != nil {
		t.Fatalf("mismatched pending move should remain for manual repair: %v", err)
	}
}

func TestPendingMoveWithUnavailableTargetCancelsWithoutTouchingSource(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	controlDir := environmentControlDir()
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	writeStorageKey(t, source, "source")
	target := filepath.Join(t.TempDir(), "missing")
	move := mustStorageMoveJSON(t, source, target)
	pendingPath := filepath.Join(controlDir, storageMoveFile)
	if err := atomicWriteFile(pendingPath, move); err != nil {
		t.Fatal(err)
	}

	prepared, warning, err := prepareEnvironmentForRun(Environment{DataDir: source, Source: "default"})
	if err != nil || warning == "" || !samePath(prepared.DataDir, source) {
		t.Fatalf("prepared=%+v warning=%q err=%v", prepared, warning, err)
	}
	if data, err := os.ReadFile(activeDatabaseFile(source, databaseKeyFileName)); err != nil || string(data) != "source" {
		t.Fatalf("source data changed: %q, %v", data, err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending move should be removed, got %v", err)
	}
}

func TestPendingMoveWithLinkedEntryCancelsWithoutFollowingIt(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	source := t.TempDir()
	target := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(source, "linked.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	// Some environments report success yet create an entry without symlink
	// semantics; the assertions below depend on a real link.
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Skipf("symlink has no link semantics in this environment")
	}

	writeStorageKey(t, source, "source")
	if err := ScheduleDataDirectoryMove(Environment{DataDir: source, Source: "default"}, target); err != nil {
		t.Fatal(err)
	}
	prepared, warning, err := prepareEnvironmentForRun(Environment{DataDir: source, Source: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if warning == "" || !samePath(prepared.DataDir, source) {
		t.Fatalf("prepared=%+v warning=%q", prepared, warning)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("source link was modified: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "linked.txt")); !os.IsNotExist(err) {
		t.Fatalf("external target was copied through link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(environmentControlDir(), storageMoveFile)); !os.IsNotExist(err) {
		t.Fatalf("pending move should be removed, got %v", err)
	}
}

func TestPendingMoveRejectsSourceDirectoryReplacedWithLink(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStorageKey(t, source, "source")
	target := t.TempDir()
	if err := ScheduleDataDirectoryMove(Environment{DataDir: source, Source: "default"}, target); err != nil {
		t.Fatal(err)
	}
	originalSource := filepath.Join(parent, "original-source")
	if err := os.Rename(source, originalSource); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, source); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if info, statErr := os.Lstat(source); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Skipf("symlink has no link semantics in this environment")
	}

	if _, err := applyPendingDataDirectoryMove(environmentControlDir()); err == nil {
		t.Fatal("linked source directory was accepted")
	}
	if data, err := os.ReadFile(filepath.Join(external, "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("linked directory target changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(activeDatabaseFile(originalSource, databaseKeyFileName)); err != nil || string(data) != "source" {
		t.Fatalf("original source changed: %q, %v", data, err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("target changed: %v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(environmentControlDir(), storageMoveFile)); err != nil {
		t.Fatalf("pending move should remain for manual repair: %v", err)
	}
}

func TestPendingMoveRejectsSourceDirectoryReplacedWithOrdinaryDirectory(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStorageKey(t, source, "source")
	target := t.TempDir()
	if err := ScheduleDataDirectoryMove(Environment{DataDir: source, Source: "default"}, target); err != nil {
		t.Fatal(err)
	}
	originalSource := filepath.Join(parent, "original-source")
	if err := os.Rename(source, originalSource); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStorageKey(t, source, "replacement")
	if err := os.WriteFile(filepath.Join(source, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := applyPendingDataDirectoryMove(environmentControlDir()); err == nil {
		t.Fatal("replaced source directory was accepted")
	}
	if data, err := os.ReadFile(filepath.Join(source, "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("replacement directory changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(activeDatabaseFile(originalSource, databaseKeyFileName)); err != nil || string(data) != "source" {
		t.Fatalf("original source changed: %q, %v", data, err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("target changed: %v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(environmentControlDir(), storageMoveFile)); err != nil {
		t.Fatalf("pending move should remain for manual repair: %v", err)
	}
}

func TestDataMovePreservesControlFilesThroughControlDirectoryLink(t *testing.T) {
	controlRoot := t.TempDir()
	actualControlDir := t.TempDir()
	controlLink := filepath.Join(controlRoot, appDirName)
	if err := os.Symlink(actualControlDir, controlLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	// Some environments report success yet produce a link without symlink
	// semantics (writes through the link do not land in the target). Verify
	// the link actually forwards before relying on it.
	probeFile := "probe-symlink"
	if err := os.WriteFile(filepath.Join(controlLink, probeFile), []byte("x"), 0o600); err != nil {
		t.Skipf("symlink not writable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(actualControlDir, probeFile)); err != nil {
		t.Skipf("symlink does not forward to control directory: %v", err)
	}
	if err := os.Remove(filepath.Join(actualControlDir, probeFile)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", controlRoot)
	target := t.TempDir()
	writeStorageKey(t, actualControlDir, "key")

	env := Environment{DataDir: actualControlDir, Source: "default"}
	if err := ScheduleDataDirectoryMove(env, target); err != nil {
		t.Fatal(err)
	}
	prepared, warning, err := prepareEnvironmentForRun(env)
	if err != nil || warning != "" || !samePath(prepared.DataDir, target) {
		t.Fatalf("prepared=%+v warning=%q err=%v", prepared, warning, err)
	}
	if _, err := os.Stat(filepath.Join(actualControlDir, storageLocationFile)); err != nil {
		t.Fatalf("storage location was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(actualControlDir, storageMoveFile)); !os.IsNotExist(err) {
		t.Fatalf("pending move should be removed, got %v", err)
	}
	if _, err := os.Stat(activeDatabaseFile(target, databaseKeyFileName)); err != nil {
		t.Fatalf("key was not moved: %v", err)
	}
}

func TestHandleStorageRelocate(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	source := t.TempDir()
	target := t.TempDir()
	invalidTarget := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidTarget, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	writeStorageKey(t, source, "source")
	server := &SearchServer{env: Environment{DataDir: source, Source: "default"}, logger: logger}

	req := httptest.NewRequest(http.MethodPost, "/api/storage/relocate", bytes.NewBufferString(`{"dataDir":`))
	result := httptest.NewRecorder()
	server.handleStorageRelocate(result, req)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want %d", result.Code, http.StatusBadRequest)
	}
	if contentType := result.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("invalid JSON content type = %q", contentType)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/storage/relocate", bytes.NewBufferString(`{"dataDir":`+strconv.Quote(invalidTarget)+`}`))
	result = httptest.NewRecorder()
	server.handleStorageRelocate(result, req)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid relocate status = %d, want %d", result.Code, http.StatusBadRequest)
	}
	if contentType := result.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("invalid relocate content type = %q", contentType)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/storage/relocate", bytes.NewBufferString(`{"dataDir":`+strconv.Quote(target)+`}`))
	result = httptest.NewRecorder()
	server.handleStorageRelocate(result, req)
	if result.Code != http.StatusOK {
		t.Fatalf("relocate status = %d, want %d: %s", result.Code, http.StatusOK, result.Body.String())
	}
	if _, err := os.Stat(filepath.Join(environmentControlDir(), storageMoveFile)); err != nil {
		t.Fatalf("migration was not scheduled: %v", err)
	}
}

func TestStorageAndDiagnosticsEndpointsThroughHTTPServer(t *testing.T) {
	controlRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", controlRoot)
	dataDir := t.TempDir()
	logDir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listen port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release listen port: %v", err)
	}
	cfg := manager.Get()
	cfg.Search.ListenAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("configure search server: %v", err)
	}
	logger, err := NewDailyLogger(logDir)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()
	server := NewSearchServer(manager, store, NewRuntimeState(false), logger, Environment{DataDir: dataDir, LogDir: logDir, Source: "default"}, nil, nil)
	url, err := server.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}

	statusRes, err := client.Get(url + "api/storage/status")
	if err != nil {
		t.Fatalf("get storage status: %v", err)
	}
	var status StorageStatus
	decodeErr := json.NewDecoder(statusRes.Body).Decode(&status)
	statusRes.Body.Close()
	if statusRes.StatusCode != http.StatusOK || decodeErr != nil || !samePath(status.DataDir, dataDir) || !status.Movable {
		t.Fatalf("storage status = status %d, value %#v, error %v", statusRes.StatusCode, status, decodeErr)
	}

	target := t.TempDir()
	body := []byte(`{"dataDir":` + strconv.Quote(target) + `}`)
	blockedReq, err := http.NewRequest(http.MethodPost, url+"api/storage/relocate", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new blocked relocation request: %v", err)
	}
	blockedReq.Header.Set("Content-Type", "application/json")
	blockedRes, err := client.Do(blockedReq)
	if err != nil {
		t.Fatalf("send blocked relocation request: %v", err)
	}
	blockedRes.Body.Close()
	if blockedRes.StatusCode != http.StatusForbidden {
		t.Fatalf("relocation without CSRF status = %d, want %d", blockedRes.StatusCode, http.StatusForbidden)
	}

	csrfRes, err := client.Get(url + "api/csrf")
	if err != nil {
		t.Fatalf("get csrf token: %v", err)
	}
	var csrf struct {
		Token string `json:"token"`
	}
	decodeErr = json.NewDecoder(csrfRes.Body).Decode(&csrf)
	csrfRes.Body.Close()
	if csrfRes.StatusCode != http.StatusOK || decodeErr != nil || csrf.Token == "" {
		t.Fatalf("csrf response = status %d, token %q, error %v", csrfRes.StatusCode, csrf.Token, decodeErr)
	}

	relocateReq, err := http.NewRequest(http.MethodPost, url+"api/storage/relocate", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new relocation request: %v", err)
	}
	relocateReq.Header.Set("Content-Type", "application/json")
	relocateReq.Header.Set(csrfHeaderName, csrf.Token)
	relocateRes, err := client.Do(relocateReq)
	if err != nil {
		t.Fatalf("send relocation request: %v", err)
	}
	responseData, readErr := io.ReadAll(relocateRes.Body)
	relocateRes.Body.Close()
	if relocateRes.StatusCode != http.StatusOK || readErr != nil {
		t.Fatalf("relocation response = status %d, body %s, error %v", relocateRes.StatusCode, responseData, readErr)
	}
	if _, err := os.Stat(filepath.Join(environmentControlDir(), storageMoveFile)); err != nil {
		t.Fatalf("relocation was not scheduled: %v", err)
	}
	pendingRes, err := client.Get(url + "api/storage/status")
	if err != nil {
		t.Fatalf("get pending storage status: %v", err)
	}
	var pendingStatus StorageStatus
	decodeErr = json.NewDecoder(pendingRes.Body).Decode(&pendingStatus)
	pendingRes.Body.Close()
	if pendingRes.StatusCode != http.StatusOK || decodeErr != nil || !pendingStatus.Pending {
		t.Fatalf("pending storage status = status %d, value %#v, error %v", pendingRes.StatusCode, pendingStatus, decodeErr)
	}

	diagnosticRes, err := client.Get(url + "api/diagnostics/export")
	if err != nil {
		t.Fatalf("export diagnostics: %v", err)
	}
	diagnosticData, readErr := io.ReadAll(diagnosticRes.Body)
	diagnosticRes.Body.Close()
	if diagnosticRes.StatusCode != http.StatusOK || diagnosticRes.Header.Get("Content-Type") != "application/zip" || readErr != nil {
		t.Fatalf("diagnostic response = status %d, content type %q, error %v", diagnosticRes.StatusCode, diagnosticRes.Header.Get("Content-Type"), readErr)
	}
	if _, err := zip.NewReader(bytes.NewReader(diagnosticData), int64(len(diagnosticData))); err != nil {
		t.Fatalf("diagnostic archive is invalid: %v", err)
	}

	lockHash, err := newPasswordHash("lock-password")
	if err != nil {
		t.Fatal(err)
	}
	secondaryHash, err := newPasswordHash("secondary-password")
	if err != nil {
		t.Fatal(err)
	}
	securedCfg := manager.Get()
	securedCfg.Security.LockPasswordHash = lockHash
	securedCfg.Security.SecondaryPasswordHash = secondaryHash
	securedCfg.Security.SecondaryAuthTimeout = 5
	if err := manager.Update(securedCfg); err != nil {
		t.Fatal(err)
	}

	unauthenticatedRes, err := client.Get(url + "api/storage/status")
	if err != nil {
		t.Fatal(err)
	}
	unauthenticatedRes.Body.Close()
	if unauthenticatedRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("locked storage status = %d, want %d", unauthenticatedRes.StatusCode, http.StatusUnauthorized)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	securedClient := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	loginReq, err := http.NewRequest(http.MethodPost, url+"api/auth/login", bytes.NewBufferString(`{"password":"lock-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set(csrfHeaderName, csrf.Token)
	loginRes, err := securedClient.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	loginRes.Body.Close()
	if loginRes.StatusCode != http.StatusOK {
		t.Fatalf("lock login = %d, want %d", loginRes.StatusCode, http.StatusOK)
	}
	lockedStatusRes, err := securedClient.Get(url + "api/storage/status")
	if err != nil {
		t.Fatal(err)
	}
	lockedStatusRes.Body.Close()
	if lockedStatusRes.StatusCode != http.StatusOK {
		t.Fatalf("storage status after lock login = %d, want %d", lockedStatusRes.StatusCode, http.StatusOK)
	}

	secondaryBlockedRes, err := securedClient.Get(url + "api/diagnostics/export")
	if err != nil {
		t.Fatal(err)
	}
	secondaryBlockedRes.Body.Close()
	if secondaryBlockedRes.StatusCode != http.StatusForbidden {
		t.Fatalf("diagnostics without secondary auth = %d, want %d", secondaryBlockedRes.StatusCode, http.StatusForbidden)
	}

	secondaryReq, err := http.NewRequest(http.MethodPost, url+"api/auth/secondary", bytes.NewBufferString(`{"password":"secondary-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	secondaryReq.Header.Set("Content-Type", "application/json")
	secondaryReq.Header.Set(csrfHeaderName, csrf.Token)
	secondaryRes, err := securedClient.Do(secondaryReq)
	if err != nil {
		t.Fatal(err)
	}
	secondaryRes.Body.Close()
	if secondaryRes.StatusCode != http.StatusOK {
		t.Fatalf("secondary auth = %d, want %d", secondaryRes.StatusCode, http.StatusOK)
	}

	securedDiagnosticRes, err := securedClient.Get(url + "api/diagnostics/export")
	if err != nil {
		t.Fatal(err)
	}
	securedDiagnosticData, readErr := io.ReadAll(securedDiagnosticRes.Body)
	securedDiagnosticRes.Body.Close()
	if securedDiagnosticRes.StatusCode != http.StatusOK || readErr != nil {
		t.Fatalf("diagnostics with secondary auth = %d, read error = %v", securedDiagnosticRes.StatusCode, readErr)
	}
	if _, err := zip.NewReader(bytes.NewReader(securedDiagnosticData), int64(len(securedDiagnosticData))); err != nil {
		t.Fatalf("secondary-authenticated diagnostic archive is invalid: %v", err)
	}
}
