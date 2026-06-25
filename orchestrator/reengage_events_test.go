package orchestrator

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-playbook/agentstream"
	"ai-playbook/cache"
	"ai-playbook/kb"
)

// fakeEvents builds an EventsFunc that emits a canned normalized event stream:
// a text delta (→ playbook), a reasoning line + a tool line (→ activity), and a
// Final (→ authoritative cache/artifact body). It records the kind + failedOutput
// it was called with so tests can assert the right prompt path was selected.
type fakeEvents struct {
	gotKind   ReengageKind
	gotFailed string
	calls     int
	delta     string
	final     string
}

func (f *fakeEvents) fn(kind ReengageKind, failedOutput string) (<-chan agentstream.Event, func() error, error) {
	f.calls++
	f.gotKind = kind
	f.gotFailed = failedOutput
	ch := make(chan agentstream.Event)
	go func() {
		ch <- agentstream.Event{Kind: agentstream.TextDelta, Text: f.delta}
		ch <- agentstream.Event{Kind: agentstream.Reasoning, Text: "diagnosing the failure"}
		ch <- agentstream.Event{Kind: agentstream.ToolActivity, Text: "run: make test"}
		ch <- agentstream.Event{Kind: agentstream.Final, Text: f.final}
		close(ch)
	}()
	closeFn := func() error { return nil }
	return ch, closeFn, nil
}

// drainAct reads the activity channel to close (bounded), returning all summaries.
func drainAct(ch <-chan string) []string {
	var got []string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case s, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, s)
		case <-timeout:
			return got
		}
	}
}

func wantActivity(t *testing.T, ch <-chan string) {
	t.Helper()
	got := drainAct(ch)
	var sawReason, sawTool bool
	for _, s := range got {
		if s == "diagnosing the failure" {
			sawReason = true
		}
		if s == "run: make test" {
			sawTool = true
		}
	}
	if !sawReason || !sawTool {
		t.Errorf("activity feed = %v, want both the reasoning and tool lines", got)
	}
}

// Regenerate via the EVENT path: the playbook reader carries the streamed delta,
// the activity feed carries reasoning + tool lines, the StreamMode is Replace, and
// the body (Final-authoritative) drives the cache re-store on close.
func TestRegenerate_EventPath_StreamsActivityAndReStores(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_ASSIST_DATA_DIR", root)
	fe := &fakeEvents{delta: "# Fresh\n", final: "# Fresh regenerated body\n"}
	c := cache.Open()
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:     sampleReq(),
		Events:  fe.fn,
		Cache:   c,
		CtxHash: "ctxhash",
		ReqHash: "reqhash",
	})

	stream, activity, mode, err := o.Regenerate()
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	if activity == nil {
		t.Fatal("event path must return a non-nil activity channel")
	}
	actCh := make(chan []string, 1)
	go func() {
		var sawR, sawT bool
		for _, s := range drainAct(activity) {
			sawR = sawR || s == "diagnosing the failure"
			sawT = sawT || s == "run: make test"
		}
		if !sawR || !sawT {
			actCh <- nil
		} else {
			actCh <- []string{"ok"}
		}
	}()

	got, _ := io.ReadAll(stream)
	if string(got) != "# Fresh\n" {
		t.Errorf("playbook reader = %q, want the streamed delta", got)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if fe.calls != 1 || fe.gotKind != KindReengageRegenerate {
		t.Errorf("Events called %d times, kind=%v; want 1 regenerate", fe.calls, fe.gotKind)
	}
	if res := <-actCh; res == nil {
		t.Error("activity feed missing reasoning and/or tool line")
	}

	// Re-store fired on close with the Final-authoritative body.
	entry := filepath.Join(root, "cache", "ctxhash", "reqhash.md")
	b, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("fresh playbook was not re-stored: %v", err)
	}
	if !strings.Contains(string(b), "Fresh regenerated body") {
		t.Errorf("re-stored entry missing the authoritative Final body:\n%s", b)
	}
}

// Followup via the EVENT path: the prompt kind is followup, the failed output is
// threaded through, the StreamMode is Append, and the activity feed streams.
func TestFollowup_EventPath_StreamsActivity(t *testing.T) {
	fe := &fakeEvents{delta: "# Revised\n", final: "# Revised fix\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:    sampleReq(),
		Events: fe.fn,
	})

	const failed = "ld: symbol not found"
	stream, activity, mode, err := o.Followup(failed)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeAppend {
		t.Errorf("mode = %v, want ModeAppend", mode)
	}
	if activity == nil {
		t.Fatal("event path must return a non-nil activity channel")
	}
	go wantActivity(t, activity)

	got, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(got) != "# Revised\n" {
		t.Errorf("playbook reader = %q, want the streamed delta", got)
	}
	if fe.gotKind != KindReengageFollowup {
		t.Errorf("kind = %v, want followup", fe.gotKind)
	}
	if fe.gotFailed != failed {
		t.Errorf("failedOutput = %q, want %q", fe.gotFailed, failed)
	}
}

// Wrapup via the EVENT path: StreamMode Append, the artifact captures the
// Final-authoritative `## Solution` body (written on close), and the KB gains the
// distilled fact.
func TestWrapup_EventPath_ArtifactAndKB(t *testing.T) {
	root := t.TempDir()
	req := sampleReq()
	fe := &fakeEvents{delta: "Resolved.\n\n## Solution\n", final: "Resolved.\n\n## Solution\nrun make -B\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:      req,
		Events:   fe.fn,
		CtxHash:  "ctxhash",
		DataRoot: root,
	})

	const runlog = `{"id":"verify","exit":0}`
	stream, activity, mode, err := o.Wrapup(runlog)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeAppend {
		t.Errorf("mode = %v, want ModeAppend", mode)
	}
	if activity == nil {
		t.Fatal("event path must return a non-nil activity channel")
	}
	go wantActivity(t, activity)

	_, _ = io.ReadAll(stream)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if fe.gotKind != KindReengageWrapup {
		t.Errorf("kind = %v, want wrapup", fe.gotKind)
	}
	if fe.gotFailed != runlog {
		t.Errorf("wrapup failedOutput (runlog) = %q, want %q", fe.gotFailed, runlog)
	}

	// (1) Artifact: solutions/<ctx>-*.md with front matter + the Final-authoritative body.
	matches, _ := filepath.Glob(filepath.Join(root, "solutions", "ctxhash-*.md"))
	if len(matches) != 1 {
		t.Fatalf("solution artifacts = %d, want 1 (%v)", len(matches), matches)
	}
	art, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(art), "run make -B") {
		t.Errorf("artifact missing the authoritative Final Solution body:\n%s", art)
	}
	if !strings.Contains(string(art), "request: fix my build") {
		t.Errorf("artifact missing the front-matter request:\n%s", art)
	}

	// (2) KB append: the distilled fact landed.
	kbText := kb.LoadFrom(root, req.ProjectRoot)
	if !strings.Contains(string(kbText), "make build") {
		t.Errorf("KB not appended with a distilled fact:\n%s", kbText)
	}
}

// When Events is nil the methods fall back to the text Agent path unchanged: a nil
// activity channel and the Agent's canned stream.
func TestReengage_FallsBackToTextWhenNoEvents(t *testing.T) {
	t.Setenv("AI_ASSIST_DATA_DIR", t.TempDir())
	fa := &fakeAgent{canned: "# Text fallback\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:   sampleReq(),
		Agent: fa.agent,
		// Events deliberately nil.
	})

	stream, activity, mode, err := o.Regenerate()
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	if activity != nil {
		t.Error("text fallback must return a nil activity channel")
	}
	got, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(got) != fa.canned {
		t.Errorf("stream = %q, want the text Agent's canned output", got)
	}
	if fa.calls != 1 {
		t.Errorf("text Agent calls = %d, want 1", fa.calls)
	}
}
