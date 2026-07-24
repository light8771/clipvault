package clipvault

import (
	"encoding/json"
	"testing"
)

func TestNotificationsDefaultAndLegacyConfig(t *testing.T) {
	if !DefaultConfig().Notifications.PauseResume {
		t.Fatal("pause/resume notifications must be enabled by default")
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(`{"maxTextChars": 100}`), &cfg); err != nil {
		t.Fatalf("unmarshal legacy config: %v", err)
	}
	if !cfg.Notifications.PauseResume {
		t.Fatal("legacy config must retain the default notification setting")
	}
}

func TestNotificationsCanBeDisabledAndPersisted(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notifications.PauseResume = false

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var persisted Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshal persisted config: %v", err)
	}
	if persisted.Notifications.PauseResume {
		t.Fatal("persisted notification setting must remain disabled")
	}
}

func TestPauseResumeNotificationProvided(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "enabled", body: `{"notifications":{"pauseResume":true}}`, want: true},
		{name: "disabled", body: `{"notifications":{"pauseResume":false}}`, want: true},
		{name: "missing notifications", body: `{}`, want: false},
		{name: "missing switch", body: `{"notifications":{}}`, want: false},
		{name: "invalid json", body: `{`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pauseResumeNotificationProvided([]byte(tt.body)); got != tt.want {
				t.Fatalf("pauseResumeNotificationProvided() = %v, want %v", got, tt.want)
			}
		})
	}
}
