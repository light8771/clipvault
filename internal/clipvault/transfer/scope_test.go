package transfer

import "testing"

func TestValidateScope(t *testing.T) {
	valid := []Scope{
		{Mode: "full"},
		{Mode: "range", From: "2026-08-01T00:00:00Z", To: "2026-08-02T00:00:00Z"},
		{Mode: "incremental", SinceID: 7},
	}
	for _, scope := range valid {
		if err := ValidateScope(scope); err != nil {
			t.Errorf("ValidateScope(%+v): %v", scope, err)
		}
	}
	invalid := []Scope{
		{Mode: "full", From: "unexpected"},
		{Mode: "range", From: "2026-08-02T00:00:00Z", To: "2026-08-01T00:00:00Z"},
		{Mode: "incremental"},
		{Mode: "legacy"},
	}
	for _, scope := range invalid {
		if err := ValidateScope(scope); err == nil {
			t.Errorf("ValidateScope(%+v) succeeded", scope)
		}
	}
}
