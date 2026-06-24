package orchestrator

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-playbook/author"
	"ai-playbook/cache"
	"ai-playbook/capture"
	"ai-playbook/kb"
)

// fakeAgent records the system prompt it was called with and returns a canned
// stream. It is the injected author.Agent for the re-engagement tests.
type fakeAgent struct {
	gotSystem string
	canned    string
	calls     int
}

func (f *fakeAgent) agent(systemPrompt, userMessage string) (io.ReadCloser, error) {
	f.calls++
	f.gotSystem = systemPrompt
	return io.NopCloser(strings.NewReader(f.canned)), nil
}

func sampleReq() capture.Request {
	return capture.Request{
		Kind:        "error",
		Command:     "make build",
		Exit:        "2",
		Scrollback:  "make: *** Error 2",
		UserRequest: "fix my build",
		ProjectRoot: "/home/me/proj",
		Project:     capture.Project{Name: "proj"},
	}
}

// Regenerate returns the fake agent's fresh stream (ModeReplace) and was called
// with the standard authoring prompt (cache-bypassed re-author).
func TestRegenerate_StreamsAndMode(t *testing.T) {
	t.Setenv("AI_ASSIST_DATA_DIR", t.TempDir()) // no KB folded in
	fa := &fakeAgent{canned: "# Fresh playbook\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:   sampleReq(),
		Agent: fa.agent,
	})

	stream, mode, err := o.Regenerate()
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	got, _ := io.ReadAll(stream)
	stream.Close()
	if string(got) != fa.canned {
		t.Errorf("stream = %q, want canned", got)
	}
	if fa.calls != 1 {
		t.Errorf("agent calls = %d, want 1", fa.calls)
	}
	if fa.gotSystem != author.SystemPrompt(sampleReq(), "") {
		t.Errorf("regenerate did not use the standard authoring prompt")
	}
}

// Regenerate re-stores the fresh playbook under the original keys when the cache +
// keys are present (matching ai-assist-regenerate's re-store).
func TestRegenerate_ReStoresFreshPlaybook(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_ASSIST_DATA_DIR", root)
	fa := &fakeAgent{canned: "# Regenerated body\n"}
	c := cache.Open()
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:     sampleReq(),
		Agent:   fa.agent,
		Cache:   c,
		CtxHash: "ctxhash",
		ReqHash: "reqhash",
	})

	stream, _, err := o.Regenerate()
	if err != nil {
		t.Fatal(err)
	}
	// The re-store fires on Close (after the full body is read).
	_, _ = io.ReadAll(stream)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	entry := filepath.Join(root, "cache", "ctxhash", "reqhash.md")
	b, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("fresh playbook was not re-stored: %v", err)
	}
	if !strings.Contains(string(b), "Regenerated body") {
		t.Errorf("re-stored entry missing the fresh body:\n%s", b)
	}
}

// Followup returns the fake stream (ModeAppend) and was called with a prompt that
// includes the failed output.
func TestFollowup_StreamsWithFailedOutput(t *testing.T) {
	fa := &fakeAgent{canned: "# Revised fix\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:   sampleReq(),
		Agent: fa.agent,
	})

	const failed = "ld: symbol not found"
	stream, mode, err := o.Followup(failed)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeAppend {
		t.Errorf("mode = %v, want ModeAppend", mode)
	}
	got, _ := io.ReadAll(stream)
	stream.Close()
	if string(got) != fa.canned {
		t.Errorf("stream = %q, want canned", got)
	}
	if !strings.Contains(fa.gotSystem, failed) {
		t.Errorf("followup prompt missing the failed output %q", failed)
	}
}

// Wrapup returns the fake stream (ModeAppend), writes a solution artifact, and
// appends a distilled fact to the KB.
func TestWrapup_ArtifactAndKB(t *testing.T) {
	root := t.TempDir()
	req := sampleReq()
	fa := &fakeAgent{canned: "Resolved.\n\n## Solution\nrun make -B\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:      req,
		Agent:    fa.agent,
		CtxHash:  "ctxhash",
		DataRoot: root,
	})

	stream, mode, err := o.Wrapup(`{"id":"verify","exit":0}`)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeAppend {
		t.Errorf("mode = %v, want ModeAppend", mode)
	}
	got, _ := io.ReadAll(stream)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != fa.canned {
		t.Errorf("stream = %q, want canned", got)
	}

	// (1) Solution artifact: solutions/<ctx>-<ts>.md with front matter + body.
	matches, _ := filepath.Glob(filepath.Join(root, "solutions", "ctxhash-*.md"))
	if len(matches) != 1 {
		t.Fatalf("solution artifacts = %d, want 1 (%v)", len(matches), matches)
	}
	art, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(art), "## Solution") {
		t.Errorf("artifact missing the streamed Solution body:\n%s", art)
	}
	if !strings.Contains(string(art), "request: fix my build") {
		t.Errorf("artifact missing the front-matter request:\n%s", art)
	}

	// (2) KB append: the project's knowledge.md gained a distilled bullet.
	kbText := kb.LoadFrom(root, req.ProjectRoot)
	if !strings.Contains(string(kbText), "make build") {
		t.Errorf("KB not appended with a distilled fact:\n%s", kbText)
	}
}

// Without a Reengage wired the re-engagement methods return ErrNotImplemented.
func TestReengageMethods_NoReengage(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	if _, _, err := o.Regenerate(); err == nil {
		t.Error("Regenerate without Reengage should error")
	}
	if _, _, err := o.Followup(""); err == nil {
		t.Error("Followup without Reengage should error")
	}
	if _, _, err := o.Wrapup(""); err == nil {
		t.Error("Wrapup without Reengage should error")
	}
}
