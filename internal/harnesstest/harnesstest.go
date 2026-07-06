// Package harnesstest holds the shared test helper for LIVE harness tests —
// tests that invoke a real installed harness CLI (the multi-harness spec's
// conditional-live-test rule): any system that has the required binary runs
// them; any system without it skips them, uniformly for every harness. Fixture
// tests never use this — they are the always-run baseline.
package harnesstest

import (
	"os/exec"
	"testing"
)

// RequireHarness skips t when the harness CLI bin is not installed on PATH.
// Every live harness test starts with it, naming the exact binary it drives:
//
//	harnesstest.RequireHarness(t, "pi")
func RequireHarness(t testing.TB, bin string) {
	t.Helper()
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("harness CLI %q not installed; skipping live test", bin)
	}
}
