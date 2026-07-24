package clipvault

import (
	"context"
	"testing"
)

func TestBackupDataCanBeImported(t *testing.T) {
	ctx := context.Background()
	source, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	if err := source.Save(ctx, ClipboardItem{
		Kind:     "text",
		Format:   "CF_UNICODETEXT",
		Text:     "backup record",
		ByteLen:  13,
		CharLen:  13,
		SHA256:   "backup-record",
		MetaJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Sensitive.CustomPatterns = []string{"(?i)backup-secret"}
	if err := source.SaveConfig(config); err != nil {
		t.Fatal(err)
	}

	backup := &BackupService{store: source}
	for _, test := range []struct {
		name     string
		format   string
		password string
	}{
		{name: "JSON", format: "json"},
		{name: "Encrypted", format: "cvexport", password: "backup-password"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, extension, err := backup.exportBackupData(test.format, test.password)
			if err != nil {
				t.Fatal(err)
			}
			if extension != test.format {
				t.Fatalf("backup extension = %q, want %q", extension, test.format)
			}
			if test.format == "cvexport" {
				if !IsCVExport(data) {
					t.Fatal("encrypted backup is not a cvexport file")
				}
				data, err = DecryptExport(data, test.password)
				if err != nil {
					t.Fatal(err)
				}
			}

			target, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if err := target.ImportFullJSON(ctx, data); err != nil {
				t.Fatal(err)
			}

			items, total, err := target.Search(ctx, SearchOptions{Limit: 10, IncludeSensitive: true})
			if err != nil {
				t.Fatal(err)
			}
			if total != 1 || items[0].Text != "backup record" {
				t.Fatalf("imported backup records = %#v, total = %d", items, total)
			}
			importedConfig, err := target.GetConfig()
			if err != nil {
				t.Fatal(err)
			}
			if !sameStrings(importedConfig.Sensitive.CustomPatterns, config.Sensitive.CustomPatterns) {
				t.Fatalf("imported sensitive rules = %#v", importedConfig.Sensitive.CustomPatterns)
			}
		})
	}
}
