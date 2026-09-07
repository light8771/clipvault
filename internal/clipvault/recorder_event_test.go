package clipvault

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

func TestManagedImagesUseExclusiveUniqueFiles(t *testing.T) {
	root := t.TempDir()
	recorder := &Recorder{imagesDir: filepath.Join(root, "images")}
	payload := []byte("png payload")
	var storedPaths, fullPaths []string
	for range 2 {
		stored, full, err := recorder.saveManagedImage(payload, 1_788_400_000_000, hashBytes(payload))
		if err != nil {
			t.Fatal(err)
		}
		storedPaths = append(storedPaths, stored)
		fullPaths = append(fullPaths, full)
	}
	if storedPaths[0] == storedPaths[1] || fullPaths[0] == fullPaths[1] {
		t.Fatalf("identical captures reused a cleanup-unsafe path: %q", storedPaths[0])
	}
	for i, full := range fullPaths {
		data, err := os.ReadFile(full)
		if err != nil || string(data) != string(payload) || filepath.Ext(full) != ".png" {
			t.Fatalf("managed image %d: path=%q data=%q err=%v", i, full, data, err)
		}
	}
}

func TestCaptureEventClassificationIsFormatDriven(t *testing.T) {
	tests := []struct {
		name    string
		formats ClipboardFormatSet
		want    domain.ClipboardEventCode
	}{
		{name: "empty", want: domain.EventClipboardEmpty},
		{name: "unknown", formats: ClipboardFormatSet{Names: []string{"FileContents"}}, want: domain.EventUnsupportedFormat},
		{name: "virtual wide", formats: ClipboardFormatSet{Names: []string{"filecontents", "FILEGROUPDESCRIPTORW"}}, want: domain.EventVirtualFile},
		{name: "virtual ansi", formats: ClipboardFormatSet{Names: []string{"FileGroupDescriptor", "FileContents"}}, want: domain.EventVirtualFile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyEmptyCapture(test.formats); got != test.want {
				t.Fatalf("classification = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEventFingerprintStableAndEventsHaveNoContentHash(t *testing.T) {
	formatA := ClipboardFormatSet{IDs: []uint32{49413, 49401}, Names: []string{"FileGroupDescriptorW", "FileContents"}}
	// Registered clipboard format IDs are reallocated between Windows sessions;
	// names, case and enumeration order are the stable identity.
	formatB := ClipboardFormatSet{IDs: []uint32{62001, 62002}, Names: []string{"filecontents", "FILEGROUPDESCRIPTORW"}}
	a := buildEventFingerprint(domain.EventVirtualFile, formatA, "classify", "", "AweSun.exe")
	b := buildEventFingerprint(domain.EventVirtualFile, formatB, "classify", "", "awesun.EXE")
	if a != b {
		t.Fatalf("equivalent events produced different fingerprints: %s != %s", a, b)
	}
	if a == buildEventFingerprint(domain.EventVirtualFile, formatB, "classify", "", "other.exe") {
		t.Fatal("source application must participate in the fingerprint")
	}

	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recorder := &Recorder{store: store, logger: &DailyLogger{}}
	recorder.saveCaptureEvent(SourceInfo{
		ExeName: "AweSun.exe", ExePath: `D:\Apps\AweSun.exe`, WindowTitle: "Remote files", Method: "foreground",
	}, time.Now().UnixMilli(), 42,
		domain.EventVirtualFile, "classify", 1, formatA, "", nil)
	items, _, err := store.Search(context.Background(), SearchOptions{Limit: 10, IncludeSensitive: true, RecordType: domain.HistoryRecordEvent})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	item := items[0]
	if item.ContentSHA256 != nil || item.Text != "" || item.EventFingerprint != a {
		t.Fatalf("invalid event persistence: %+v", item)
	}
	if item.SourceExePath != `D:\Apps\AweSun.exe` || item.SourceWindowTitle != "Remote files" || item.SourceAttribution != "foreground" {
		t.Fatalf("event source context was not preserved: %+v", item)
	}
	payload, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal(payload, &dto); err != nil {
		t.Fatal(err)
	}
	if value, ok := dto["contentSha256"]; !ok || value != nil {
		t.Fatalf("contentSha256 must be explicit null: %s", payload)
	}
	for _, legacyField := range []string{"sha256", "contentAvailable", "unavailableReason", "isDuplicate"} {
		if _, exists := dto[legacyField]; exists {
			t.Fatalf("legacy field %q leaked into event DTO: %s", legacyField, payload)
		}
	}
	if _, exists := dto["kind"]; exists {
		t.Fatalf("event DTO must not carry a pseudo content kind: %s", payload)
	}
	if _, exists := dto["format"]; exists {
		t.Fatalf("event DTO must not carry a pseudo content format: %s", payload)
	}
}

func TestCanceledCaptureContextCannotPersistEvent(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recorder := &Recorder{store: store, logger: &DailyLogger{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder.saveCaptureEventWithContext(ctx, SourceInfo{}, time.Now().UnixMilli(), 7,
		domain.EventCaptureFailed, "read", 1, ClipboardFormatSet{}, string(domain.EventCaptureFailed), nil)
	items, total, err := store.Search(context.Background(), SearchOptions{
		RecordType: RecordTypeEvent, Limit: 10, IncludeSensitive: true,
	})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("canceled event persisted: items=%d total=%d err=%v", len(items), total, err)
	}
}

func TestEventSearchGroupsFiveMinuteBurstsWithoutMutatingRows(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	formats := ClipboardFormatSet{IDs: []uint32{50001}, Names: []string{"PrivateFormat"}}
	save := func(at time.Time, source string) {
		if err := store.Save(ctx, ClipboardItem{
			CapturedAtMS: at.UnixMilli(), RecordType: RecordTypeEvent,
			EventCode:    domain.EventUnsupportedFormat,
			CaptureStage: "classify", CaptureAttempts: 1, EventFormats: formats.Info(),
			SourceExeName: source, SourceAttribution: "foreground",
		}); err != nil {
			t.Fatal(err)
		}
	}
	save(base, "remote.exe")
	// Windows process names are case-insensitive; raw casing must not split a
	// burst after fingerprint normalization has identified the same source.
	save(base.Add(4*time.Minute), "REMOTE.EXE")
	save(base.Add(10*time.Minute), "remote.exe")
	save(base.Add(11*time.Minute), "other.exe")

	items, total, err := store.Search(ctx, SearchOptions{RecordType: RecordTypeEvent, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("grouped result total=%d items=%d, want 3", total, len(items))
	}
	foundBurst := false
	for _, item := range items {
		if item.OccurrenceCount == 2 {
			foundBurst = true
			if !item.FirstOccurredAt.Equal(base) || !item.LastOccurredAt.Equal(base.Add(4*time.Minute)) {
				t.Fatalf("burst bounds = %s..%s", item.FirstOccurredAt, item.LastOccurredAt)
			}
		}
	}
	if !foundBurst {
		t.Fatalf("missing two-occurrence burst: %+v", items)
	}

	var rawCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clipboard_items WHERE record_type = 'event'`).Scan(&rawCount); err != nil {
		t.Fatal(err)
	}
	if rawCount != 4 {
		t.Fatalf("query grouping mutated raw rows: got %d, want 4", rawCount)
	}
}

func TestGroupedEventsUseLatestOccurrenceAsRepresentative(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	latest := time.Date(2026, 9, 3, 12, 4, 0, 0, time.UTC)

	// Save the latest occurrence first so its ID is lower than the older row.
	for _, occurrence := range []struct {
		at       time.Time
		sequence uint32
	}{{at: latest, sequence: 200}, {at: latest.Add(-4 * time.Minute), sequence: 100}} {
		if err := store.Save(ctx, ClipboardItem{
			CapturedAtMS: occurrence.at.UnixMilli(), RecordType: RecordTypeEvent,
			EventCode:       domain.EventClipboardBusy,
			CaptureSequence: occurrence.sequence, CaptureStage: "open", CaptureAttempts: 4,
			SourceExeName: "editor.exe", SourceAttribution: "foreground",
		}); err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := store.Search(ctx, SearchOptions{RecordType: RecordTypeEvent, Limit: 10})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("grouped search total=%d items=%d err=%v", total, len(items), err)
	}
	if items[0].CaptureSequence != 200 || !items[0].LastOccurredAt.Equal(latest) {
		t.Fatalf("representative is not latest occurrence: %+v", items[0])
	}
}
