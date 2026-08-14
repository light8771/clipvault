package clipvault

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
)

var (
	elementIDPattern       = regexp.MustCompile(`\sid="([^"]+)"`)
	dataActionPattern      = regexp.MustCompile(`\sdata-action="([^"]+)"`)
	moduleImportPattern    = regexp.MustCompile(`\bfrom\s+['"](\./[^'"]+)['"]`)
	settingsSectionPattern = regexp.MustCompile(`\sdata-section="([^"]+)"`)
)

func renderApplicationPage(t *testing.T, route string) string {
	t.Helper()
	result := httptest.NewRecorder()
	(&SearchServer{}).handleIndex(result, httptest.NewRequest(http.MethodGet, "http://clipvault.local"+route, nil))
	if result.Code != http.StatusOK {
		t.Fatalf("render %s: status=%d body=%s", route, result.Code, result.Body.String())
	}
	return result.Body.String()
}

func TestApplicationPagesHaveUniqueElementIDs(t *testing.T) {
	for route := range applicationPages {
		t.Run(route, func(t *testing.T) {
			seen := make(map[string]struct{})
			for _, match := range elementIDPattern.FindAllStringSubmatch(renderApplicationPage(t, route), -1) {
				id := match[1]
				if _, duplicate := seen[id]; duplicate {
					t.Errorf("duplicate element id %q", id)
				}
				seen[id] = struct{}{}
			}
		})
	}
}

func TestApplicationPageRoutesAreExact(t *testing.T) {
	want := map[string]string{"/": "history", "/settings": "settings"}
	if len(applicationPages) != len(want) {
		t.Fatalf("application page count=%d, want %d", len(applicationPages), len(want))
	}
	for route, name := range want {
		page, ok := applicationPages[route]
		if !ok || page.Name != name {
			t.Errorf("route %s page=%q found=%v, want %q", route, page.Name, ok, name)
		}
	}
	for _, route := range []string{"/history", "/settings/", "/index.html", "/app"} {
		result := httptest.NewRecorder()
		(&SearchServer{}).handleIndex(result, httptest.NewRequest(http.MethodGet, "http://clipvault.local"+route, nil))
		if result.Code != http.StatusNotFound {
			t.Errorf("obsolete or unknown route %s status=%d, want 404", route, result.Code)
		}
	}
}

func TestApplicationPageActionsAreRegisteredByItsEntryPoint(t *testing.T) {
	shared, err := fs.ReadFile(webFS, "web/app/actions.js")
	if err != nil {
		t.Fatal(err)
	}
	for route, page := range applicationPages {
		t.Run(route, func(t *testing.T) {
			entry, err := fs.ReadFile(webFS, "web"+page.Bootstrap)
			if err != nil {
				t.Fatal(err)
			}
			registrations := string(shared) + string(entry)
			for _, match := range dataActionPattern.FindAllStringSubmatch(renderApplicationPage(t, route), -1) {
				action := match[1]
				registered := regexp.MustCompile(`['"]` + regexp.QuoteMeta(action) + `['"]\s*:`).MatchString(registrations)
				if !registered {
					t.Errorf("data action %q is not registered by %s", action, page.Bootstrap)
				}
			}
		})
	}
}

func TestSettingsNavigationTargetsExistingSections(t *testing.T) {
	body := renderApplicationPage(t, "/settings")
	for _, match := range settingsSectionPattern.FindAllStringSubmatch(body, -1) {
		sectionID := match[1]
		if !strings.Contains(body, `id="`+sectionID+`"`) {
			t.Errorf("settings navigation target %q does not exist", sectionID)
		}
		navigationID := "snav-" + strings.TrimPrefix(sectionID, "s-")
		if !strings.Contains(body, `id="`+navigationID+`"`) {
			t.Errorf("settings navigation control %q does not exist", navigationID)
		}
	}
}

func TestApplicationEntryPointsHaveDisjointPageModules(t *testing.T) {
	tests := []struct {
		entry     string
		forbidden []string
	}{
		{
			entry: "/app/history-bootstrap.js",
			forbidden: []string{
				"appearance-form.js", "appearance-media.js", "config-events.js", "config-save.js", "recovery.js", "settings-lifecycle.js",
				"settings-navigation.js", "settings-sources.js", "settings-storage.js", "transfer.js",
			},
		},
		{
			entry: "/app/settings-bootstrap.js",
			forbidden: []string{
				"history-actions.js", "history-lifecycle.js", "history-list.js", "history-navigation.js",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.entry, func(t *testing.T) {
			modules := applicationModuleGraph(t, strings.TrimPrefix(tt.entry, "/"))
			for _, forbidden := range tt.forbidden {
				if _, loaded := modules["app/"+forbidden]; loaded {
					t.Errorf("%s loads page-incompatible module %s", tt.entry, forbidden)
				}
			}
		})
	}
}

func TestApplicationModuleRoutesExactlyMatchReachableGraph(t *testing.T) {
	reachable := make(map[string]struct{})
	for _, page := range applicationPages {
		for module := range applicationModuleGraph(t, strings.TrimPrefix(page.Bootstrap, "/")) {
			reachable["/"+module] = struct{}{}
		}
	}
	registered := make(map[string]struct{}, len(applicationModuleAssets))
	for _, asset := range applicationModuleAssets {
		if _, duplicate := registered[asset]; duplicate {
			t.Errorf("duplicate module route %s", asset)
		}
		registered[asset] = struct{}{}
	}
	for module := range reachable {
		if _, ok := registered[module]; !ok {
			t.Errorf("reachable module %s has no HTTP route", module)
		}
	}
	for module := range registered {
		if _, ok := reachable[module]; !ok {
			t.Errorf("registered module %s is unreachable from an application entry point", module)
		}
	}
}

func applicationModuleGraph(t *testing.T, entry string) map[string]struct{} {
	t.Helper()
	seen := make(map[string]struct{})
	var visit func(string)
	visit = func(module string) {
		if _, exists := seen[module]; exists {
			return
		}
		seen[module] = struct{}{}
		data, err := fs.ReadFile(webFS, "web/"+module)
		if err != nil {
			t.Fatalf("read module %s: %v", module, err)
		}
		for _, match := range moduleImportPattern.FindAllStringSubmatch(string(data), -1) {
			visit(path.Clean(path.Join(path.Dir(module), match[1])))
		}
	}
	visit(entry)
	return seen
}
