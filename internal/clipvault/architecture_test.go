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

func TestRequiredArchitecturePackagesExistAndAreConsumed(t *testing.T) {
	required := []string{
		"domain", "application", filepath.Join("storage", "duckdb"), "transfer",
		"backup", "security", filepath.Join("transport", "httpui"),
		filepath.Join("platform", "windows"), "bootstrap",
	}
	for _, dir := range required {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("required package %s: %v", dir, err)
		}
		hasProductionGo := false
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".go" && !strings.HasSuffix(entry.Name(), "_test.go") {
				hasProductionGo = true
				break
			}
		}
		if !hasProductionGo {
			t.Errorf("required package %s has no production Go source", dir)
		}
	}
	for _, importPath := range []string{
		"internal/clipvault/transfer", "internal/clipvault/backup", "internal/clipvault/security",
		"internal/clipvault/transport/httpui", "internal/clipvault/platform/windows",
	} {
		found := false
		entries, _ := os.ReadDir(".")
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			data, _ := os.ReadFile(entry.Name())
			if strings.Contains(string(data), importPath) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("architecture package %s is not consumed by the root adapters", importPath)
		}
	}
}

func TestArchitectureDependencyDirection(t *testing.T) {
	for dir, forbidden := range map[string][]string{
		"domain":                           {"internal/clipvault/application", "internal/clipvault/storage", "internal/clipvault/transport", "internal/clipvault/platform", "internal/clipvault/bootstrap"},
		"application":                      {"internal/clipvault/storage", "internal/clipvault/transport", "internal/clipvault/platform", "internal/clipvault/bootstrap"},
		"transfer":                         {"internal/clipvault/storage", "internal/clipvault/transport", "internal/clipvault/platform", "internal/clipvault/bootstrap"},
		"backup":                           {"internal/clipvault/storage", "internal/clipvault/transport", "internal/clipvault/platform", "internal/clipvault/bootstrap"},
		"security":                         {"internal/clipvault/storage", "internal/clipvault/transport", "internal/clipvault/platform", "internal/clipvault/bootstrap"},
		filepath.Join("storage", "duckdb"): {"internal/clipvault/transport", "internal/clipvault/platform", "internal/clipvault/bootstrap"},
	} {
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return err
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, dependency := range forbidden {
				if strings.Contains(string(data), dependency) {
					t.Errorf("%s has forbidden outward dependency %s", path, dependency)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
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
