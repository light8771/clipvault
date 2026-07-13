//go:build release

package licensing

import "testing"

func TestReleaseBuildEnforcesLicense(t *testing.T) {
	if !EnforcementEnabled() {
		t.Fatal("release build does not enforce licensing")
	}
}
