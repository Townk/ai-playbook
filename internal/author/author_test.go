package author

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/kb"
	"github.com/Townk/ai-playbook/pkg/driver"
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
	// Point the KB data dir at an empty temp dir so the recall load (both sets,
	// via LoadRecall inside Author) is empty and the system prompt deterministic.
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	req := sampleFailure()
	fa := &fakeAgent{canned: "# Fix your build\n\n```bash {id=fix}\nmake clean\n```\n"}

	r, err := Author(req, nil, fa.agent)
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
	// With no KB file present, the folded-in KB is empty.
	// Author auto-resolves the shell from $SHELL; mirror that here.
	wantSys := SystemPrompt(req, "", "", driver.ResolveShellName(""))
	if fa.gotSystem != wantSys {
		t.Errorf("agent system prompt did not match SystemPrompt(req)\n--- got ---\n%s", fa.gotSystem)
	}
	wantUser := BuildUserMessage(req)
	if fa.gotUser != wantUser {
		t.Errorf("agent user message did not match BuildUserMessage(req)\n--- got ---\n%s", fa.gotUser)
	}
}

// TestAuthor_FoldsInOnDiskKB exercises the recall wiring end-to-end: a project KB
// file written under the data dir for the request's project root is loaded (via
// LoadRecall inside Author) and folded into the system prompt Author hands the
// agent (the "## What we already know" section).
func TestAuthor_FoldsInOnDiskKB(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", root)
	req := sampleFailure()
	const facts = "deploys via fly.io, not docker"
	p := kb.Path(root, req.ProjectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(facts), 0o644); err != nil {
		t.Fatal(err)
	}

	fa := &fakeAgent{canned: "ok\n"}
	r, err := Author(req, nil, fa.agent)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if !strings.Contains(fa.gotSystem, "## What we already know about this project") {
		t.Errorf("Author system prompt missing KB header\n%s", fa.gotSystem)
	}
	if !strings.Contains(fa.gotSystem, facts) {
		t.Errorf("Author system prompt missing on-disk KB content %q", facts)
	}
}

// TestSystemPrompt_LoadBearingSections is the bad-port smoke: the embedded prompt
// must contain the load-bearing instructions (block schema, value-passing refs,
// the verify-fold-in rule) so a botched port is caught.
func TestSystemPrompt_LoadBearingSections(t *testing.T) {
	sys := SystemPrompt(sampleFailure(), "", "", "zsh")
	wants := []string{
		"LITERATE TROUBLESHOOTING PLAYBOOK",         // failure structure
		"{id=fix}",                                  // block-schema id marker
		"{id=verify needs=<fix-id>}",                // separate verify block
		"re-run the original failed",                // verify re-runs original command
		"Do NOT fold the re-run into the fix block", // C3a no-fold rule
		"needs=",       // dependency-ordering guidance (now via the rubric)
		"from=",        // data-dependency guidance (now via the rubric)
		"static",       // static (non-runnable) blocks (now via the rubric)
		"unified diff", // diff block schema (now via the rubric)
		"set -e",       // shell block semantics
	}
	for _, w := range wants {
		if !strings.Contains(sys, w) {
			t.Errorf("system prompt missing load-bearing %q", w)
		}
	}
}

func TestSystemPrompt_GeneralBranch(t *testing.T) {
	req := capture.Request{Kind: "question", Exit: "0", UserRequest: "q", ProjectRoot: "/p", Project: capture.Project{Name: "p"}}
	sys := SystemPrompt(req, "", "", "zsh")
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

// TestSystemPrompt_OmitsPerRequestContext is the B8 RED/GREEN case: the failed
// command, the scrollback, and the user's request text must live ONLY in
// BuildUserMessage's output — SystemPrompt must not duplicate them (every
// authoring/followup/final call sends both, so duplicating this content paid
// its token cost twice for no benefit).
func TestSystemPrompt_OmitsPerRequestContext(t *testing.T) {
	req := sampleFailure()
	sys := SystemPrompt(req, "", "", "zsh")
	for _, dup := range []string{req.Command, req.UserRequest, "no input files"} {
		if strings.Contains(sys, dup) {
			t.Errorf("SystemPrompt must not duplicate per-request context %q\n--- prompt ---\n%s", dup, sys)
		}
	}
	// BuildUserMessage must still carry all of it — no information may be lost.
	user := BuildUserMessage(req)
	for _, want := range []string{req.Command, req.UserRequest, "no input files"} {
		if !strings.Contains(user, want) {
			t.Errorf("BuildUserMessage missing per-request context %q\n--- message ---\n%s", want, user)
		}
	}
}

func TestSystemPrompt_KBFoldedIn(t *testing.T) {
	sys := SystemPrompt(sampleFailure(), "", "uses bazel, not make", "zsh")
	if !strings.Contains(sys, "## What we already know about this project") {
		t.Errorf("KB header missing when KB non-empty")
	}
	if !strings.Contains(sys, "uses bazel, not make") {
		t.Errorf("KB content missing")
	}
}

// TestSystemPrompt_ShellAwareness_SH verifies that a `sh`-targeted prompt names
// sh explicitly and includes POSIX-only restrictions (mentioning [[ as forbidden).
func TestSystemPrompt_ShellAwareness_SH(t *testing.T) {
	sys := SystemPrompt(sampleFailure(), "", "", "sh")

	// Must identify the shell explicitly.
	if !strings.Contains(sys, "execute under `sh` (POSIX shell)") {
		t.Errorf("sh prompt missing POSIX shell identification\n--- prompt ---\n%s", sys)
	}
	// Must instruct avoiding [[.
	if !strings.Contains(sys, "NOT `[[ ]]`") {
		t.Errorf("sh prompt must warn against [[ ]] (bash/zsh extension)\n--- prompt ---\n%s", sys)
	}
	// Must not claim zsh or bash as the target.
	if strings.Contains(sys, "execute under `zsh`") {
		t.Errorf("sh prompt must not identify the shell as zsh")
	}
	if strings.Contains(sys, "execute under `bash`") {
		t.Errorf("sh prompt must not identify the shell as bash")
	}
	// Portable core must still be present.
	if !strings.Contains(sys, "set -e") {
		t.Errorf("sh prompt must still contain set -e guidance")
	}
}

// TestSystemPrompt_ShellAwareness_Zsh verifies that a zsh-targeted prompt names
// zsh and does NOT impose the POSIX-only restriction.
func TestSystemPrompt_ShellAwareness_Zsh(t *testing.T) {
	sys := SystemPrompt(sampleFailure(), "", "", "zsh")

	if !strings.Contains(sys, "execute under `zsh`") {
		t.Errorf("zsh prompt missing shell identification\n--- prompt ---\n%s", sys)
	}
	// POSIX-only restriction must be absent for zsh.
	if strings.Contains(sys, "NOT `[[ ]]`") {
		t.Errorf("zsh prompt must not impose POSIX-only restriction (NOT `[[ ]]` must not appear)")
	}
}

// TestSystemPrompt_ShellAwareness_Bash verifies that a bash-targeted prompt names
// bash and does NOT impose the POSIX-only restriction.
func TestSystemPrompt_ShellAwareness_Bash(t *testing.T) {
	sys := SystemPrompt(sampleFailure(), "", "", "bash")

	if !strings.Contains(sys, "execute under `bash`") {
		t.Errorf("bash prompt missing shell identification\n--- prompt ---\n%s", sys)
	}
	// POSIX-only restriction must be absent for bash.
	if strings.Contains(sys, "NOT `[[ ]]`") {
		t.Errorf("bash prompt must not impose POSIX-only restriction (NOT `[[ ]]` must not appear)")
	}
}
