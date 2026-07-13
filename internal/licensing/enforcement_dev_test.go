//go:build !release

package licensing

import "testing"

func TestDevelopmentBuildDoesNotEnforceLicense(t *testing.T) {
	if EnforcementEnabled() {
		t.Fatal("development build unexpectedly enforces licensing")
	}
}
