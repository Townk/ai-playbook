package author

import (
	"io"
	"strings"
	"testing"
)

func TestFollowupPrompt_CarriesFailedOutputAndTask(t *testing.T) {
	req := sampleFailure() // Command "make build", exit "2"
	const failed = "ld: symbol(s) not found for architecture arm64"
	sys := FollowupPrompt(req, failed)

	wants := []string{
		"did not work",          // the "fix didn't work" framing
		"fix my broken build",   // original request
		"make build",            // failed command (also the verify re-run target)
		failed,                  // the captured failed output IS present
		"DIFFERENT",             // "propose a DIFFERENT, corrected fix"
		"{id=fix}",              // the fix block must be tagged exactly
		"{id=verify needs=fix}", // separate verify block, exact id (runner keys on it)
		"MANDATORY",             // the ids are stressed as required
		"another follow-up",     // offer another follow-up on failure
	}
	for _, w := range wants {
		if !strings.Contains(sys, w) {
			t.Errorf("followup prompt missing %q\n--- prompt ---\n%s", w, sys)
		}
	}
}

func TestFollowupPrompt_NoOutputCaptured(t *testing.T) {
	sys := FollowupPrompt(sampleFailure(), "")
	if !strings.Contains(sys, "(no output captured)") {
		t.Errorf("empty failed output must render the no-output placeholder:\n%s", sys)
	}
}

func TestFollowup_CallsAgentWithPromptAndMessage(t *testing.T) {
	req := sampleFailure()
	const failed = "boom: it failed"
	fa := &fakeAgent{canned: "# Revised fix\n\n```bash {id=fix2}\nmake -B\n```\n"}

	r, err := Followup(req, failed, fa.agent)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if fa.calls != 1 {
		t.Fatalf("agent calls = %d, want 1", fa.calls)
	}
	if got, _ := io.ReadAll(r); string(got) != fa.canned {
		t.Errorf("stream = %q, want canned", got)
	}
	if fa.gotSystem != FollowupPrompt(req, failed) {
		t.Errorf("agent system prompt != FollowupPrompt\n--- got ---\n%s", fa.gotSystem)
	}
	if fa.gotUser != BuildUserMessage(req) {
		t.Errorf("agent user message != BuildUserMessage\n--- got ---\n%s", fa.gotUser)
	}
}
