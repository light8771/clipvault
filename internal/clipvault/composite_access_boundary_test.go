package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCompositeExternalMembershipAndSensitiveScope(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := compositeFixture(t, `C:\source.png`)
	for _, sensitive := range []bool{false, true} {
		if err := store.Save(ctx, ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, Sensitive: sensitive}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := exportAll(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewExternalReadService(store, Environment{DataDir: store.dataDir})
	var request dataAPIQueryRequest
	if err := json.Unmarshal([]byte(`{"resource":"clipboardItems","groupBy":["kind"],"aggregate":{"count":"*"},"filter":{"kind":{"in":["image","table"]}}}`), &request); err != nil {
		t.Fatal(err)
	}
	plan, apiErr := parseDataAPIQuery(&request)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for _, allow := range []bool{false, true} {
		want := int64(1)
		if allow {
			want = 2
		}
		access := ReadAccess{AllowSensitive: allow}
		rows, err := svc.QueryAggregate(ctx, access, plan)
		if err != nil || len(rows) != len(snapshot.Content.Kinds) {
			t.Fatalf("aggregate: %v %v", rows, err)
		}
		for _, row := range rows {
			if row["count"] != want {
				t.Fatalf("membership double count or scope leak: %v", row)
			}
		}
		stats, err := svc.Stats(ctx, access, StatsRequest{Bucket: "day"})
		if err != nil || stats.Total != want {
			t.Fatalf("stats: %+v %v", stats, err)
		}
		for _, bucket := range stats.ByKind {
			if bucket.Count != want {
				t.Fatalf("stats membership: %+v", bucket)
			}
		}
	}
	for _, item := range items {
		content, err := svc.GetContent(ctx, ReadAccess{}, item.ID)
		if item.Sensitive {
			if !errors.Is(err, ErrExternalNotFound) {
				t.Fatalf("sensitive content not hidden: %v", err)
			}
		} else if err != nil || content.Text != compositeJSONString(snapshot.Content) || content.MimeType != "application/json" {
			t.Fatalf("public content contract: %+v %v", content, err)
		}
	}
}
