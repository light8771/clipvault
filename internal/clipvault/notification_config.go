package clipvault

import "encoding/json"

// pauseResumeNotificationProvided reports whether an API config payload
// explicitly sets the pause/resume notification switch.
func pauseResumeNotificationProvided(body []byte) bool {
	var payload struct {
		Notifications *struct {
			PauseResume *bool `json:"pauseResume"`
		} `json:"notifications"`
	}
	return json.Unmarshal(body, &payload) == nil && payload.Notifications != nil && payload.Notifications.PauseResume != nil
}
