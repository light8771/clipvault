package clipvault

import "testing"

func TestFilterEngineNoMatchByDefault(t *testing.T) {
	// 默认无自定义规则，所有内容应非敏感
	engine, err := NewFilterEngine(DefaultConfig().Sensitive)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		text string
	}{
		{name: "short number", text: "123456"},
		{name: "password field", text: "password = correct-horse-battery-staple"},
		{name: "jwt token", text: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signaturepart"},
		{name: "normal text", text: "今天的会议纪要：先做剪切板工具，再补搜索页面。"},
		{name: "bank card", text: "4111 1111 1111 1111"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if d := engine.CheckText(tc.text); d.Sensitive {
				t.Fatalf("expected not sensitive, got sensitive: %s", d.Reason)
			}
		})
	}
}

func TestFilterEngineTagsCustomPattern(t *testing.T) {
	cfg := SensitiveConfig{
		CustomPatterns: []string{`(?i)secret`, `^\d{6}$`},
	}
	engine, err := NewFilterEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}

	sensitive := []string{"my_secret_key", "123456"}
	for _, text := range sensitive {
		if d := engine.CheckText(text); !d.Sensitive {
			t.Fatalf("expected sensitive for %q, got not sensitive", text)
		}
	}

	notSensitive := []string{"hello world", "1234567", "normal text"}
	for _, text := range notSensitive {
		if d := engine.CheckText(text); d.Sensitive {
			t.Fatalf("expected not sensitive for %q, got sensitive: %s", text, d.Reason)
		}
	}
}

func TestFilterEngineInvalidPatternErrors(t *testing.T) {
	cfg := SensitiveConfig{
		CustomPatterns: []string{`[invalid`},
	}
	_, err := NewFilterEngine(cfg)
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
}
