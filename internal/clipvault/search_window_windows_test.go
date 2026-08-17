//go:build windows

package clipvault

import (
	"testing"
	"time"
)

func TestFormatSuggestionUsesChineseKindLabel(t *testing.T) {
	tests := map[string]string{
		"text":    "文本",
		"image":   "图片",
		"video":   "视频",
		"archive": "压缩包",
		"office":  "Office",
		"files":   "其他文件",
	}

	for kind, want := range tests {
		t.Run(kind, func(t *testing.T) {
			item := StoredItem{
				CreatedAt:        time.Date(2026, time.July, 24, 12, 30, 0, 0, time.Local),
				Kind:             kind,
				ContentAvailable: true,
			}
			wantSuggestion := "07-24 12:30  " + want + "  [" + want + "]"
			if got := formatSuggestion(item); got != wantSuggestion {
				t.Errorf("formatSuggestion() = %q, want %q", got, wantSuggestion)
			}
		})
	}
}

func TestFormatSuggestionMarksEventStates(t *testing.T) {
	at := time.Date(2026, time.July, 24, 12, 30, 0, 0, time.Local)
	dup := StoredItem{CreatedAt: at, Kind: "text", Text: "hello", ContentAvailable: true, IsDuplicate: true}
	if got := formatSuggestion(dup); got != "07-24 12:30  文本·重复  hello" {
		t.Errorf("duplicate suggestion = %q", got)
	}
	unavailable := StoredItem{CreatedAt: at, Kind: "other", Format: "CLIPVAULT_EVENT_ONLY", UnavailableReason: "superseded_before_read"}
	if got := formatSuggestion(unavailable); got != "07-24 12:30  other  [内容已被覆盖]" {
		t.Errorf("event-only suggestion = %q", got)
	}
}
