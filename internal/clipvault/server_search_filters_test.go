package clipvault

import (
	"testing"

	"clipboard_ext/internal/clipvault/domain"
)

func TestSearchHistoryFiltersUseTranslationKeysForEveryClipboardKind(t *testing.T) {
	tests := map[string]string{
		domain.KindText:    "history.kind_text",
		domain.KindImage:   "history.kind_image",
		domain.KindVideo:   "history.kind_video",
		domain.KindArchive: "history.kind_archive",
		domain.KindOffice:  "history.kind_office",
		domain.KindFiles:   "history.kind_files",
		domain.KindOther:   "history.kind_other",
	}
	for kind, want := range tests {
		filters := searchHistoryFilters(kind, false, 0, "", "", nil)
		if got := filters["shlog.filter.kind"]; got != want {
			t.Errorf("kind %q filter label = %q, want %q", kind, got, want)
		}
	}
}
