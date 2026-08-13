package clipvault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const isolatedTestPassword = "Old-Recovery-Password-2026"

type isolatedSwitchFixture struct {
	dataDir    string
	targetID   string
	targetKey  []byte
	currentKey []byte
}

func prepareIsolatedSwitchFixture(t *testing.T) isolatedSwitchFixture {
	t.Helper()
	dataDir := t.TempDir()
	targetID := "20260810-120000.000000000"

	targetStore, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	targetKey := targetStore.DBKey()
	if err := WriteRecoveryFile(activeDatabaseFile(dataDir, databaseRecoveryFileName), targetKey, []byte(isolatedTestPassword)); err != nil {
		t.Fatal(err)
	}
	if err := targetStore.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(isolatedDatabasesDirectory(dataDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(activeDatabaseDirectory(dataDir), isolatedDatabaseDirectory(dataDir, targetID)); err != nil {
		t.Fatal(err)
	}

	currentStore, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	currentKey := currentStore.DBKey()
	if err := WriteRecoveryFile(activeDatabaseFile(dataDir, databaseRecoveryFileName), currentKey, []byte("Current-Recovery-Password-2026")); err != nil {
		t.Fatal(err)
	}
	if err := currentStore.Close(); err != nil {
		t.Fatal(err)
	}
	return isolatedSwitchFixture{dataDir: dataDir, targetID: targetID, targetKey: targetKey, currentKey: currentKey}
}

func assertSwitchCompleted(t *testing.T, fixture isolatedSwitchFixture, currentID string) {
	t.Helper()
	activeKey, _, err := ReadDPAPIKeyFile(activeDatabaseFile(fixture.dataDir, databaseKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(activeKey, fixture.targetKey) {
		t.Fatal("target database was not activated")
	}
	retainedKey, _, err := ReadDPAPIKeyFile(databaseGroupFile(isolatedDatabaseDirectory(fixture.dataDir, currentID), databaseKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retainedKey, fixture.currentKey) {
		t.Fatal("current database was not retained")
	}
	if _, err := os.Stat(filepath.Join(fixture.dataDir, isolatedDatabaseSwitchFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("switch marker still exists: %v", err)
	}
}

func TestIsolatedDatabaseCanBeScheduledAndSwitched(t *testing.T) {
	fixture := prepareIsolatedSwitchFixture(t)
	status, err := ListIsolatedDatabases(fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Databases) != 1 || status.Databases[0].ID != fixture.targetID || !status.Databases[0].CanSwitch {
		t.Fatalf("isolated status = %#v", status)
	}
	if err := ScheduleIsolatedDatabaseSwitch(fixture.dataDir, fixture.targetID, "wrong-password"); !errors.Is(err, ErrIsolatedDatabaseUnavailable) {
		t.Fatalf("wrong password error = %v", err)
	}
	if err := ScheduleIsolatedDatabaseSwitch(fixture.dataDir, fixture.targetID, isolatedTestPassword); err != nil {
		t.Fatal(err)
	}
	pending, err := readPendingIsolatedDatabaseSwitch(fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	switchedID, err := applyPendingIsolatedDatabaseSwitch(fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if switchedID != fixture.targetID {
		t.Fatalf("switched id = %q", switchedID)
	}
	assertSwitchCompleted(t, fixture, pending.CurrentID)
	store, err := OpenStore(StoreOptions{DataDir: fixture.dataDir})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
}

func TestDatabaseSwitchResumesAfterCurrentDirectoryRename(t *testing.T) {
	fixture := prepareIsolatedSwitchFixture(t)
	if err := ScheduleIsolatedDatabaseSwitch(fixture.dataDir, fixture.targetID, isolatedTestPassword); err != nil {
		t.Fatal(err)
	}
	pending, err := readPendingIsolatedDatabaseSwitch(fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(activeDatabaseDirectory(fixture.dataDir), isolatedDatabaseDirectory(fixture.dataDir, pending.CurrentID)); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPendingIsolatedDatabaseSwitch(fixture.dataDir); err != nil {
		t.Fatal(err)
	}
	assertSwitchCompleted(t, fixture, pending.CurrentID)
}

func TestDatabaseSwitchFinishesAfterTargetDirectoryRename(t *testing.T) {
	fixture := prepareIsolatedSwitchFixture(t)
	if err := ScheduleIsolatedDatabaseSwitch(fixture.dataDir, fixture.targetID, isolatedTestPassword); err != nil {
		t.Fatal(err)
	}
	pending, err := readPendingIsolatedDatabaseSwitch(fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(activeDatabaseDirectory(fixture.dataDir), isolatedDatabaseDirectory(fixture.dataDir, pending.CurrentID)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(isolatedDatabaseDirectory(fixture.dataDir, pending.TargetID), activeDatabaseDirectory(fixture.dataDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPendingIsolatedDatabaseSwitch(fixture.dataDir); err != nil {
		t.Fatal(err)
	}
	assertSwitchCompleted(t, fixture, pending.CurrentID)
}

func TestCreateNewDatabaseKeepsUnreadableDatabaseDirectory(t *testing.T) {
	dataDir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	oldKey := store.DBKey()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CreateNewDatabase(dataDir); err != nil {
		t.Fatal(err)
	}
	status, err := ListIsolatedDatabases(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Databases) != 1 {
		t.Fatalf("isolated databases = %#v", status.Databases)
	}
	retainedKey, _, err := ReadDPAPIKeyFile(databaseGroupFile(isolatedDatabaseDirectory(dataDir, status.Databases[0].ID), databaseKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retainedKey, oldKey) {
		t.Fatal("old database directory was not retained")
	}
}

func TestListIsolatedDatabasesMarksMissingRecoveryUnavailable(t *testing.T) {
	dataDir := t.TempDir()
	id := "20260810-120000.000000000"
	directory := isolatedDatabaseDirectory(dataDir, id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databaseGroupFile(directory, databaseFileName), []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := ListIsolatedDatabases(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Databases) != 1 || status.Databases[0].CanSwitch || status.Databases[0].HasRecovery {
		t.Fatalf("isolated status = %#v", status)
	}
}

func TestCancelIsolatedDatabaseSwitchOnlyBeforeRename(t *testing.T) {
	fixture := prepareIsolatedSwitchFixture(t)
	if err := ScheduleIsolatedDatabaseSwitch(fixture.dataDir, fixture.targetID, isolatedTestPassword); err != nil {
		t.Fatal(err)
	}
	if err := CancelIsolatedDatabaseSwitch(fixture.dataDir); err != nil {
		t.Fatal(err)
	}
	if err := ScheduleIsolatedDatabaseSwitch(fixture.dataDir, fixture.targetID, isolatedTestPassword); err != nil {
		t.Fatal(err)
	}
	pending, err := readPendingIsolatedDatabaseSwitch(fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(activeDatabaseDirectory(fixture.dataDir), isolatedDatabaseDirectory(fixture.dataDir, pending.CurrentID)); err != nil {
		t.Fatal(err)
	}
	if err := CancelIsolatedDatabaseSwitch(fixture.dataDir); err == nil {
		t.Fatal("in-progress switch was cancelled")
	}
}
