package author

import (
	"io"
	"strings"
	"testing"

	"ai-playbook/capture"
)

func TestFollowupPrompt_CarriesFailedOutputAndTask(t *testing.T) {
	req := sampleFailure() // Command "make build", exit "2"
	const failed = "ld: symbol(s) not found for architecture arm64"
	sys := FollowupPrompt(req, failed)

	wants := []string{
		"did not work",               // the "fix didn't work" framing
		"fix my broken build",        // original request
		"make build",                 // failed command (also the verify re-run target)
		failed,                       // the captured failed output IS present
		"DIFFERENT",                  // "propose a DIFFERENT, corrected fix"
		"{id=verify needs=<fix-id>}", // separate verify block
		"another follow-up",          // offer another follow-up on failure
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

func TestWrapupPrompt_VerifyAndSolutionFraming(t *testing.T) {
	req := sampleFailure()
	const runlog = `{"id":"fix","exit":0}` + "\n" + `{"id":"verify","exit":0}`
	sys := WrapupPrompt(req, runlog)

	wants := []string{
		"wrapping up",           // wrap-up framing
		"RESOLVED",              // (1) state whether resolved — the verify framing
		"## Solution",           // (2) the Solution section header
		"ai-assist-remember",    // (3) remember-once distillation
		"fix my broken build",   // original request
		runlog,                  // the run log is interpolated
		"Exit code 0 = success", // exit-code legend (present when a run log exists)
	}
	for _, w := range wants {
		if !strings.Contains(sys, w) {
			t.Errorf("wrapup prompt missing %q\n--- prompt ---\n%s", w, sys)
		}
	}
}

func TestWrapupPrompt_NoRunLog(t *testing.T) {
	sys := WrapupPrompt(sampleFailure(), "")
	if !strings.Contains(sys, "No blocks were run in this session.") {
		t.Errorf("empty run log must render the no-blocks placeholder:\n%s", sys)
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

func TestWrapup_CallsAgentWithPromptAndMessage(t *testing.T) {
	req := sampleFailure()
	const runlog = `{"id":"fix","exit":0}`
	fa := &fakeAgent{canned: "Resolved.\n\n## Solution\ndo X\n"}

	r, err := Wrapup(req, runlog, fa.agent)
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
	if fa.gotSystem != WrapupPrompt(req, runlog) {
		t.Errorf("agent system prompt != WrapupPrompt\n--- got ---\n%s", fa.gotSystem)
	}
}

// A request with no command still produces a usable wrap-up prompt (no panic, the
// placeholder rendering).
func TestWrapupPrompt_EmptyRequest(t *testing.T) {
	sys := WrapupPrompt(capture.Request{}, "")
	if !strings.Contains(sys, "<unknown>") {
		t.Errorf("empty request should fall back to <unknown>:\n%s", sys)
	}
}
