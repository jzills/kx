package cli

import (
	"os"
	"testing"
)

// KX_STATE and KX_CONFIG redirect the two files kx keeps on disk, and this
// package's tests assert on where those files land by default: --version names
// both paths, and the root help screen lists them. Exported in the developer's
// own shell — which is the documented way to give a second terminal its own
// history — they redirect the paths out from under those assertions, and three
// tests fail for a reason that has nothing to do with the change under test.
//
// CI never sees it, since nothing there exports either one. Cleared for the
// whole package rather than per test so a later test asserting a default path
// inherits the isolation instead of rediscovering this.
//
// Unset rather than emptied: the empty string is how both lookups spell
// "absent", so either works today, and unsetting stays right if that ever
// tightens.
func TestMain(m *testing.M) {
	for _, key := range []string{"KX_STATE", "KX_CONFIG"} {
		if err := os.Unsetenv(key); err != nil {
			panic("clearing " + key + ": " + err.Error())
		}
	}
	os.Exit(m.Run())
}
