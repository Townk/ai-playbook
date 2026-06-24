package author

import (
	"io"
	"strings"
	"testing"

	"ai-playbook/capture"
)

// sampleFailure is a representative failed-command request.
func sampleFailure() capture.Request {
	return capture.Request{
		Kind:        "error",
		Command:     "make build",
		Exit:        "2",
		Scrollback:  "make: *** [build] Error 2\ngcc: fatal error: no input files",
		UserRequest: "fix my broken build",
		ProjectRoot: "/home/me/proj",
		CWD:         "/home/me/proj/sub",
		Project:     capture.Project{Name: "proj", Branch: "main"},
	}
}

func TestBuildUserMessage_Failure(t *testing.T) {
	msg := BuildUserMessage(sampleFailure())

	wants := []string{
		"make build",               // command
		"exit 2",                   // exit
		"fix my broken build",      // user_request
		"no input files",           // scrollback content
		"Relevant terminal output", // failure-output block header
		"proj",                     // project name
		"on branch main",           // branch
	}
	for _, w := range wants {
		if !strings.Contains(msg, w) {
			t.Errorf("user message missing %q\n--- message ---\n%s", w, msg)
		}
	}
}

func TestBuildUserMessage_GeneralOmitsFailureFraming(t *testing.T) {
	req := capture.Request{
		Kind:        "question",
		Command:     "ls",
		Exit:        "0",
		UserRequest: "how do I list hidden files",
		ProjectRoot: "/p",
		Project:     capture.Project{Name: "p"},
	}
	msg := BuildUserMessage(req)
	if strings.Contains(msg, "Failed command") {
		t.Errorf("general request must not carry failure framing:\n%s", msg)
	}
	if strings.Contains(msg, "Relevant terminal output") {
		t.Errorf("general request must not carry the failure-output block:\n%s", msg)
	}
	if !strings.Contains(msg, "how do I list hidden files") {
		t.Errorf("general request missing user_request:\n%s", msg)
	}
}

// fakeAgent records the args it was called with and returns a canned reader.
type fakeAgent struct {
	gotSystem string
	gotUser   string
	canned    string
	calls     int
}

func (f *fakeAgent) agent(systemPrompt, userMessage string) (io.ReadCloser, error) {
	f.calls++
	f.gotSystem = systemPrompt
	f.gotUser = userMessage
	return io.NopCloser(strings.NewReader(f.canned)), nil
}

func TestAuthor_UsesEmbeddedPromptAndAssembledMessage(t *testing.T) {
	req := sampleFailure()
	fa := &fakeAgent{canned: "# Fix your build\n\n```bash {id=fix}\nmake clean\n```\n"}

	r, err := Author(req, fa.agent)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if fa.calls != 1 {
		t.Fatalf("agent calls = %d, want 1", fa.calls)
	}

	// The returned stream is the fake's canned playbook.
	got, _ := io.ReadAll(r)
	if string(got) != fa.canned {
		t.Errorf("stream = %q, want canned %q", got, fa.canned)
	}

	// It was called with the embedded system prompt + the assembled user message.
	wantSys := SystemPrompt(req, KB)
	if fa.gotSystem != wantSys {
		t.Errorf("agent system prompt did not match SystemPrompt(req)\n--- got ---\n%s", fa.gotSystem)
	}
	wantUser := BuildUserMessage(req)
	if fa.gotUser != wantUser {
		t.Errorf("agent user message did not match BuildUserMessage(req)\n--- got ---\n%s", fa.gotUser)
	}
}

// TestSystemPrompt_LoadBearingSections is the bad-port smoke: the embedded prompt
// must contain the load-bearing instructions (block schema, value-passing refs,
// the verify-fold-in rule) so a botched port is caught.
func TestSystemPrompt_LoadBearingSections(t *testing.T) {
	sys := SystemPrompt(sampleFailure(), "")
	wants := []string{
		"LITERATE TROUBLESHOOTING PLAYBOOK",           // failure structure
		"{id=fix}",                                    // block-schema id marker
		"{id=next needs=fix}",                         // needs-gating marker
		"$AAS_OUT_fix / $AAS_ERR_fix / $AAS_EXIT_fix", // value-passing refs
		"{id=verify needs=<fix-id>}",                  // separate verify block
		"re-run the original failed",                  // verify re-runs original command
		"Do NOT fold the re-run into the fix block",   // C3a no-fold rule
		"{static}",      // static (non-runnable) tag
		"unified diff",  // diff block schema
		"set -e",        // shell block semantics
		"ai-assist-run", // helper tool
	}
	for _, w := range wants {
		if !strings.Contains(sys, w) {
			t.Errorf("system prompt missing load-bearing %q", w)
		}
	}
}

func TestSystemPrompt_GeneralBranch(t *testing.T) {
	req := capture.Request{Kind: "question", Exit: "0", UserRequest: "q", ProjectRoot: "/p", Project: capture.Project{Name: "p"}}
	sys := SystemPrompt(req, "")
	if !strings.Contains(sys, "LITERATE HOW-TO PLAYBOOK") {
		t.Errorf("general branch must use the HOW-TO structure:\n%s", sys)
	}
	if strings.Contains(sys, "LITERATE TROUBLESHOOTING PLAYBOOK") {
		t.Errorf("general branch must NOT use the troubleshooting structure")
	}
	if strings.Contains(sys, "Failed command:") {
		t.Errorf("general branch must not frame a failed command")
	}
}

func TestSystemPrompt_KBFoldedIn(t *testing.T) {
	sys := SystemPrompt(sampleFailure(), "uses bazel, not make")
	if !strings.Contains(sys, "## What we already know about this project") {
		t.Errorf("KB header missing when KB non-empty")
	}
	if !strings.Contains(sys, "uses bazel, not make") {
		t.Errorf("KB content missing")
	}
}
