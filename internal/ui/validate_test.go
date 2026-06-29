package ui

import "testing"

// ---- ValidatePlaybook ----

func TestValidatePlaybook_RealPlaybook(t *testing.T) {
	md := "# Build the app\n\nDo this:\n\n```bash {id=x}\nmake build\n```\n"
	if !ValidatePlaybook(md) {
		t.Fatalf("ValidatePlaybook: a real playbook (H1 + runnable block) must be valid")
	}
}

func TestValidatePlaybook_Narration(t *testing.T) {
	// No H1 and no runnable block — a narration, not a playbook.
	if ValidatePlaybook("I ran the build for you and it worked great.") {
		t.Fatalf("ValidatePlaybook: a narration (no H1 / no runnable block) must be invalid")
	}
	// An H1 but no runnable block is still not a playbook.
	if ValidatePlaybook("# A title\n\njust prose, nothing to run\n") {
		t.Fatalf("ValidatePlaybook: an H1 with no runnable block must be invalid")
	}
}
