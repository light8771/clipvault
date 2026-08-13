package clipvault

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

const (
	isolatedDatabaseTimeLayout = "20060102-150405.000000000"
	isolatedDatabaseSwitchFile = "database-switch.json"
)

var (
	isolatedDatabaseIDPattern = regexp.MustCompile(`^\d{8}-\d{6}\.\d{9}$`)
	isolatedDatabaseMu        sync.Mutex

	ErrIsolatedDatabaseNotFound    = errors.New("isolated database not found")
	ErrIsolatedDatabaseUnavailable = errors.New("isolated database cannot be restored")
	ErrIsolatedDatabasePending     = errors.New("isolated database switch already pending")
)

type IsolatedDatabaseInfo struct {
	ID          string `json:"id"`
	IsolatedAt  string `json:"isolatedAt"`
	SizeBytes   int64  `json:"sizeBytes"`
	HasRecovery bool   `json:"hasRecovery"`
	CanSwitch   bool   `json:"canSwitch"`
}

type IsolatedDatabaseStatus struct {
	Databases []IsolatedDatabaseInfo `json:"databases"`
	PendingID string                 `json:"pendingId,omitempty"`
}

type isolatedDatabaseSwitch struct {
	TargetID       string `json:"targetId"`
	CurrentID      string `json:"currentId"`
	KeyFingerprint string `json:"keyFingerprint"`
}

func databaseGroupExists(directory string) (bool, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("database group is not a regular directory: %s", filepath.Base(directory))
	}
	return true, nil
}

func inspectDatabaseGroup(directory string) (size int64, hasDatabase, hasRecovery bool, err error) {
	exists, err := databaseGroupExists(directory)
	if err != nil || !exists {
		return 0, false, false, err
	}
	for _, name := range databaseFileNames {
		info, statErr := os.Lstat(databaseGroupFile(directory, name))
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return 0, false, false, statErr
		}
		if !info.Mode().IsRegular() {
			return 0, false, false, fmt.Errorf("database group contains a non-regular %s", name)
		}
		size += info.Size()
		hasDatabase = hasDatabase || name == databaseFileName
		hasRecovery = hasRecovery || name == databaseRecoveryFileName
	}
	return size, hasDatabase, hasRecovery, nil
}

func ListIsolatedDatabases(dataDir string) (IsolatedDatabaseStatus, error) {
	status := IsolatedDatabaseStatus{Databases: make([]IsolatedDatabaseInfo, 0)}
	root := isolatedDatabasesDirectory(dataDir)
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return status, fmt.Errorf("read isolated databases: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isolatedDatabaseIDPattern.MatchString(entry.Name()) {
			continue
		}
		size, hasDatabase, hasRecovery, err := inspectDatabaseGroup(isolatedDatabaseDirectory(dataDir, entry.Name()))
		if err != nil {
			return status, err
		}
		isolatedAt, err := time.ParseInLocation(isolatedDatabaseTimeLayout, entry.Name(), time.UTC)
		if err != nil {
			return status, err
		}
		status.Databases = append(status.Databases, IsolatedDatabaseInfo{
			ID:          entry.Name(),
			IsolatedAt:  isolatedAt.Format(time.RFC3339Nano),
			SizeBytes:   size,
			HasRecovery: hasRecovery,
			CanSwitch:   hasDatabase && hasRecovery,
		})
	}
	sort.Slice(status.Databases, func(i, j int) bool {
		return status.Databases[i].ID > status.Databases[j].ID
	})
	pending, err := readPendingIsolatedDatabaseSwitch(dataDir)
	if err == nil {
		status.PendingID = pending.TargetID
	} else if !errors.Is(err, os.ErrNotExist) {
		return status, err
	}
	return status, nil
}

func readPendingIsolatedDatabaseSwitch(dataDir string) (isolatedDatabaseSwitch, error) {
	var pending isolatedDatabaseSwitch
	raw, err := os.ReadFile(filepath.Join(dataDir, isolatedDatabaseSwitchFile))
	if err != nil {
		return pending, err
	}
	if err := decodeStrictJSON(raw, &pending); err != nil {
		return pending, fmt.Errorf("invalid database switch request: %w", err)
	}
	if !isolatedDatabaseIDPattern.MatchString(pending.TargetID) ||
		!isolatedDatabaseIDPattern.MatchString(pending.CurrentID) ||
		pending.TargetID == pending.CurrentID {
		return isolatedDatabaseSwitch{}, errors.New("invalid database switch request")
	}
	fingerprint, err := hex.DecodeString(pending.KeyFingerprint)
	if err != nil || len(fingerprint) != 8 {
		return isolatedDatabaseSwitch{}, errors.New("invalid database switch request")
	}
	return pending, nil
}

func writePendingIsolatedDatabaseSwitch(dataDir string, pending isolatedDatabaseSwitch) error {
	raw, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(filepath.Join(dataDir, isolatedDatabaseSwitchFile), raw); err != nil {
		return fmt.Errorf("save database switch request: %w", err)
	}
	return nil
}

func newIsolatedDatabaseID(dataDir string, excluded ...string) (string, error) {
	excludedIDs := make(map[string]struct{}, len(excluded))
	for _, id := range excluded {
		excludedIDs[id] = struct{}{}
	}
	base := time.Now().UTC()
	for attempt := 0; attempt < 100; attempt++ {
		id := base.Add(time.Duration(attempt)).Format(isolatedDatabaseTimeLayout)
		if _, skip := excludedIDs[id]; skip {
			continue
		}
		exists, err := databaseGroupExists(isolatedDatabaseDirectory(dataDir, id))
		if err != nil {
			return "", err
		}
		if !exists {
			return id, nil
		}
	}
	return "", errors.New("cannot allocate isolated database id")
}

func verifyDatabaseGroup(directory, expectedFingerprint string) error {
	dbKey, fingerprint, err := ReadDPAPIKeyFile(databaseGroupFile(directory, databaseKeyFileName))
	if err != nil || hex.EncodeToString(fingerprint) != expectedFingerprint {
		return errors.New("database key is unavailable")
	}
	if err := verifyDuckDBKey(databaseGroupFile(directory, databaseFileName), hex.EncodeToString(dbKey)); err != nil {
		return fmt.Errorf("database verification failed: %w", err)
	}
	return nil
}

func ScheduleIsolatedDatabaseSwitch(dataDir, id, password string) error {
	isolatedDatabaseMu.Lock()
	defer isolatedDatabaseMu.Unlock()

	if _, err := readPendingIsolatedDatabaseSwitch(dataDir); err == nil {
		return ErrIsolatedDatabasePending
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !isolatedDatabaseIDPattern.MatchString(id) {
		return ErrIsolatedDatabaseNotFound
	}
	targetDir := isolatedDatabaseDirectory(dataDir, id)
	_, hasDatabase, hasRecovery, err := inspectDatabaseGroup(targetDir)
	if err != nil {
		return err
	}
	if !hasDatabase {
		return ErrIsolatedDatabaseNotFound
	}
	if !hasRecovery {
		return ErrIsolatedDatabaseUnavailable
	}
	dbKey, fingerprint, err := ReadRecoveryFile(databaseGroupFile(targetDir, databaseRecoveryFileName), []byte(password))
	if err != nil {
		time.Sleep(500 * time.Millisecond)
		return fmt.Errorf("%w: recovery password verification failed", ErrIsolatedDatabaseUnavailable)
	}
	if err := verifyDuckDBKey(databaseGroupFile(targetDir, databaseFileName), hex.EncodeToString(dbKey)); err != nil {
		return fmt.Errorf("%w: database key verification failed", ErrIsolatedDatabaseUnavailable)
	}
	if err := WriteDPAPIKeyFile(databaseGroupFile(targetDir, databaseKeyFileName), dbKey); err != nil {
		return fmt.Errorf("prepare isolated database key: %w", err)
	}
	currentID, err := newIsolatedDatabaseID(dataDir, id)
	if err != nil {
		return err
	}
	return writePendingIsolatedDatabaseSwitch(dataDir, isolatedDatabaseSwitch{
		TargetID:       id,
		CurrentID:      currentID,
		KeyFingerprint: hex.EncodeToString(fingerprint),
	})
}

func databaseSwitchState(dataDir string, pending isolatedDatabaseSwitch) (active, target, current bool, err error) {
	active, err = databaseGroupExists(activeDatabaseDirectory(dataDir))
	if err != nil {
		return false, false, false, err
	}
	target, err = databaseGroupExists(isolatedDatabaseDirectory(dataDir, pending.TargetID))
	if err != nil {
		return false, false, false, err
	}
	current, err = databaseGroupExists(isolatedDatabaseDirectory(dataDir, pending.CurrentID))
	return active, target, current, err
}

func CancelIsolatedDatabaseSwitch(dataDir string) error {
	isolatedDatabaseMu.Lock()
	defer isolatedDatabaseMu.Unlock()
	pending, err := readPendingIsolatedDatabaseSwitch(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	active, target, current, err := databaseSwitchState(dataDir, pending)
	if err != nil {
		return err
	}
	if !active || !target || current {
		return errors.New("database switch is already in progress")
	}
	return os.Remove(filepath.Join(dataDir, isolatedDatabaseSwitchFile))
}

func advanceDatabaseSwitch(dataDir string, pending isolatedDatabaseSwitch) (string, error) {
	activeDir := activeDatabaseDirectory(dataDir)
	targetDir := isolatedDatabaseDirectory(dataDir, pending.TargetID)
	currentDir := isolatedDatabaseDirectory(dataDir, pending.CurrentID)
	for {
		active, target, current, err := databaseSwitchState(dataDir, pending)
		if err != nil {
			return "", err
		}
		switch {
		case active && target && !current:
			if err := verifyDatabaseGroup(targetDir, pending.KeyFingerprint); err != nil {
				return "", err
			}
			if err := os.Rename(activeDir, currentDir); err != nil {
				return "", fmt.Errorf("isolate current database: %w", err)
			}
		case !active && target && current:
			if err := verifyDatabaseGroup(targetDir, pending.KeyFingerprint); err != nil {
				return "", err
			}
			if err := os.Rename(targetDir, activeDir); err != nil {
				return "", fmt.Errorf("activate target database: %w", err)
			}
		case active && !target && current:
			if err := verifyDatabaseGroup(activeDir, pending.KeyFingerprint); err != nil {
				return "", err
			}
			if err := os.Remove(filepath.Join(dataDir, isolatedDatabaseSwitchFile)); err != nil {
				return "", fmt.Errorf("finish database switch: %w", err)
			}
			return pending.TargetID, nil
		default:
			return "", errors.New("database switch has an invalid directory state")
		}
	}
}

func applyPendingIsolatedDatabaseSwitch(dataDir string) (string, error) {
	isolatedDatabaseMu.Lock()
	defer isolatedDatabaseMu.Unlock()
	pending, err := readPendingIsolatedDatabaseSwitch(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return advanceDatabaseSwitch(dataDir, pending)
}

func createNewDatabaseGroup(dataDir string) error {
	isolatedDatabaseMu.Lock()
	defer isolatedDatabaseMu.Unlock()
	if _, err := readPendingIsolatedDatabaseSwitch(dataDir); err == nil {
		return ErrIsolatedDatabasePending
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	targetID, err := newIsolatedDatabaseID(dataDir)
	if err != nil {
		return err
	}
	currentID, err := newIsolatedDatabaseID(dataDir, targetID)
	if err != nil {
		return err
	}
	targetDir := isolatedDatabaseDirectory(dataDir, targetID)
	store, err := openStoreDirectory(targetDir, (*Store).initializeSchema)
	if err != nil {
		_ = os.RemoveAll(targetDir)
		return fmt.Errorf("create new database: %w", err)
	}
	fingerprint := KeyFingerprint(store.DBKey())
	if err := store.Close(); err != nil {
		_ = os.RemoveAll(targetDir)
		return err
	}
	if err := writeDatabaseStateFile(targetDir, AppState{
		Engine:         "duckdb",
		KeyFingerprint: hex.EncodeToString(fingerprint),
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		_ = os.RemoveAll(targetDir)
		return err
	}
	pending := isolatedDatabaseSwitch{
		TargetID:       targetID,
		CurrentID:      currentID,
		KeyFingerprint: hex.EncodeToString(fingerprint),
	}
	if err := writePendingIsolatedDatabaseSwitch(dataDir, pending); err != nil {
		_ = os.RemoveAll(targetDir)
		return err
	}
	_, err = advanceDatabaseSwitch(dataDir, pending)
	return err
}
