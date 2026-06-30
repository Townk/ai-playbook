package ui

import "testing"

// TestSetSourcePath_ConsumeOnce verifies that takeSourcePath clears
// pendingSourcePath so a second call returns "" (consume-once semantics).
func TestSetSourcePath_ConsumeOnce(t *testing.T) {
	SetSourcePath("/store/x.md")
	if got := takeSourcePath(); got != "/store/x.md" {
		t.Fatalf("takeSourcePath = %q, want /store/x.md", got)
	}
	if got := takeSourcePath(); got != "" {
		t.Fatalf("second take must be empty (consume-once), got %q", got)
	}
}
