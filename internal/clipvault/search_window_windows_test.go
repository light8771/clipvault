//go:build windows

package clipvault

import (
	"testing"
	"time"
)

// newFormatSuggestionTestEnv 注册 i18n 服务（zh-CN 默认），使搜索窗词条
// 渲染与生产路径一致；测试结束恢复原指针。
func newFormatSuggestionTestEnv(t *testing.T) {
	t.Helper()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc, err := NewI18nService(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prev := templateI18nService.Swap(svc)
	t.Cleanup(func() { templateI18nService.Store(prev) })
}

func TestFormatSuggestionUsesChineseKindLabel(t *testing.T) {
	newFormatSuggestionTestEnv(t)
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
	newFormatSuggestionTestEnv(t)
	at := time.Date(2026, time.July, 24, 12, 30, 0, 0, time.Local)
	dup := StoredItem{CreatedAt: at, Kind: "text", Text: "hello", ContentAvailable: true, IsDuplicate: true}
	if got := formatSuggestion(dup); got != "07-24 12:30  文本·重复  hello" {
		t.Errorf("duplicate suggestion = %q", got)
	}
	unavailable := StoredItem{CreatedAt: at, Kind: "other", Format: "CLIPVAULT_EVENT_ONLY", UnavailableReason: "superseded_before_read"}
	if got := formatSuggestion(unavailable); got != "07-24 12:30  other  [内容已被新复制覆盖]" {
		t.Errorf("event-only suggestion = %q", got)
	}
}
