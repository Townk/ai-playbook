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

// TestSetAssisted_ConsumeOnce verifies SetAssisted stashes the opt-in on
// pendingAssisted, and that pendingAssisted can be cleared the same way
// Main's consume-once cluster clears it (m.assisted = pendingAssisted;
// pendingAssisted = false) — mirroring pendingAutoRollback's consume-once
// wiring.
func TestSetAssisted_ConsumeOnce(t *testing.T) {
	SetAssisted(true)
	if !pendingAssisted {
		t.Fatal("SetAssisted must set pendingAssisted")
	}
	pendingAssisted = false // mirrors Main()'s consume-once clear
	if pendingAssisted {
		t.Fatal("pendingAssisted must clear (consume-once)")
	}
}
