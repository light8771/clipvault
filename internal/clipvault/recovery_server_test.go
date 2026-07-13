//go:build windows

package clipvault

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryPageIsChineseAndExplainsCredentials(t *testing.T) {
	required := []string{
		`lang="zh-CN"`,
		"恢复剪贴板数据库",
		"这不是软件激活码，也不是锁屏密码",
		"仅在本机处理",
		"创建新数据库",
		`role="status"`,
		`aria-live="polite"`,
	}
	for _, text := range required {
		if !strings.Contains(recoveryHTML, text) {
			t.Fatalf("recovery page is missing %q", text)
		}
	}
	for _, stale := range []string{"Data Recovery", "Recovery Password", "location.reload()"} {
		if strings.Contains(recoveryHTML, stale) {
			t.Fatalf("recovery page still contains stale content %q", stale)
		}
	}
}

func TestRecoveryHandlerRequiresPasswordInChinese(t *testing.T) {
	rs := NewRecoveryServer(t.TempDir())
	req := httptest.NewRequest("POST", "/api/recovery/recover", strings.NewReader(`{"password":""}`))
	recorder := httptest.NewRecorder()
	rs.handleRecover(recorder, req)

	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error != "请输入数据恢复密码" {
		t.Fatalf("unexpected response: %+v", response)
	}
}
