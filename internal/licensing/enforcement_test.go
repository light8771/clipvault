package licensing

import "testing"

func TestEnforcementEnabled(t *testing.T) {
	if !EnforcementEnabled() {
		t.Fatal("licensing enforcement is disabled; all builds must enforce activation")
	}
}
