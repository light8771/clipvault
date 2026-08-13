package clipvault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionFilesStayModular(t *testing.T) {
	root := filepath.Join("..", "..")
	limits := map[string]int{".go": 700, ".js": 500, ".css": 800, ".html": 800}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if strings.Contains(path, ".git") || strings.Contains(path, ".playwright-cli") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "test.") || strings.Contains(path, "_test.") {
			return nil
		}
		limit, ok := limits[filepath.Ext(path)]
		if !ok {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lines := strings.Count(string(data), "\n") + 1
		if lines > limit {
			t.Errorf("%s has %d lines; modular limit is %d", path, lines, limit)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCorePackagesHaveNoTransportPlatformOrStorageBackEdges(t *testing.T) {
	for _, dir := range []string{"domain", "application"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"net/http", "duckdb", "platform", "transport", "internal/licensing"} {
				if strings.Contains(string(data), forbidden) {
					t.Errorf("%s depends on forbidden outer layer %q", filepath.Join(dir, entry.Name()), forbidden)
				}
			}
		}
	}
}

func TestWebSourcesUseOnlyVersionedAPI(t *testing.T) {
	err := filepath.WalkDir("web", func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || (filepath.Ext(path) != ".js" && filepath.Ext(path) != ".html") {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, token := range strings.FieldsFunc(string(data), func(r rune) bool { return r == '\'' || r == '"' || r == '`' || r == ' ' || r == '\n' }) {
			if strings.HasPrefix(token, "/api/") && !strings.HasPrefix(token, "/api/v1/") {
				t.Errorf("%s contains legacy API token %q", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
