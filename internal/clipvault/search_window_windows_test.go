//go:build windows

package clipvault

import (
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
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
				CreatedAt:  time.Date(2026, time.July, 24, 12, 30, 0, 0, time.Local),
				RecordType: domain.HistoryRecordContent,
				Kind:       kind,
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
	unavailable := StoredItem{CreatedAt: at, RecordType: domain.HistoryRecordEvent, EventCode: domain.EventUnsupportedFormat}
	if got := formatSuggestion(unavailable); got != "07-24 12:30  采集事件  [暂不支持此类剪贴板内容]" {
		t.Errorf("capture event suggestion = %q", got)
	}
	empty := StoredItem{CreatedAt: at, RecordType: domain.HistoryRecordEvent, EventCode: domain.EventClipboardEmpty}
	if got := formatSuggestion(empty); got != "07-24 12:30  采集事件  [剪贴板已清空]" {
		t.Errorf("clipboard-empty suggestion = %q", got)
	}
	imageLimit := StoredItem{CreatedAt: at, RecordType: domain.HistoryRecordEvent, EventCode: domain.EventImageLimitExceeded}
	if got := formatSuggestion(imageLimit); got != "07-24 12:30  采集事件  [图片超过保存上限]" {
		t.Errorf("image-limit suggestion = %q", got)
	}
}

func TestFormatSuggestionMarksConsecutiveContentDuplicate(t *testing.T) {
	newFormatSuggestionTestEnv(t)
	item := StoredItem{
		CreatedAt:            time.Date(2026, time.July, 24, 12, 30, 0, 0, time.Local),
		RecordType:           domain.HistoryRecordContent,
		Kind:                 domain.KindText,
		Text:                 "same",
		ConsecutiveDuplicate: true,
	}
	if got := formatSuggestion(item); got != "07-24 12:30  文本·连续重复  same" {
		t.Fatalf("duplicate suggestion = %q", got)
	}
}
