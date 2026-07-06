package harnesstest

import "testing"

// TestRequireHarness pins the conditional-live-test contract: a missing harness
// CLI skips the test (naming the binary); a present one lets it run.
func TestRequireHarness(t *testing.T) {
	reached := false
	t.Run("missing bin skips", func(t *testing.T) {
		RequireHarness(t, "definitely-not-an-installed-harness-cli")
		reached = true // unreachable: Skipf stops the subtest above
	})
	if reached {
		t.Error("RequireHarness must skip when the harness CLI is absent")
	}

	t.Run("present bin runs", func(t *testing.T) {
		RequireHarness(t, "sh") // present on every POSIX system the suite runs on
		// Reaching this line IS the assertion.
	})
}
