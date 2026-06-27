package orchestrator

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-playbook/internal/agentstream"
	"ai-playbook/internal/cache"
)

// fakeEvents builds an EventsFunc that emits a canned normalized event stream:
// a text delta (→ playbook), a reasoning line + a tool line (→ activity), and a
// Final (→ authoritative cache/artifact body). It records the kind + base + change
// it was called with so tests can assert the right prompt path was selected.
type fakeEvents struct {
	gotKind   ReengageKind
	gotBase   string
	gotFailed string
	calls     int
	delta     string
	final     string
}

func (f *fakeEvents) fn(kind ReengageKind, base, change string) (<-chan agentstream.Event, func() error, error) {
	f.calls++
	f.gotKind = kind
	f.gotBase = base
	f.gotFailed = change
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

func wantActivity(t *testing.T, ch <-chan string, wantDelta string) {
	t.Helper()
	got := drainAct(ch)
	var sawReason, sawTool, sawDelta bool
	for _, s := range got {
		if s == "diagnosing the failure" {
			sawReason = true
		}
		if s == "run: make test" {
			sawTool = true
		}
		if s == wantDelta {
			sawDelta = true
		}
	}
	if !sawReason || !sawTool {
		t.Errorf("activity feed = %v, want both the reasoning and tool lines", got)
	}
	if !sawDelta {
		t.Errorf("activity feed = %v, want the streamed delta %q (transient narration)", got, wantDelta)
	}
}

// Regenerate via the EVENT path: the playbook reader carries the streamed delta,
// the activity feed carries reasoning + tool lines, the StreamMode is Replace, and
// the body (Final-authoritative) drives the cache re-store on close.
func TestRegenerate_EventPath_StreamsActivityAndReStores(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", root)
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
		var sawR, sawT, sawD bool
		for _, s := range drainAct(activity) {
			sawR = sawR || s == "diagnosing the failure"
			sawT = sawT || s == "run: make test"
			sawD = sawD || s == "# Fresh\n" // streamed delta → activity (transient)
		}
		if !sawR || !sawT || !sawD {
			actCh <- nil
		} else {
			actCh <- []string{"ok"}
		}
	}()

	got, _ := io.ReadAll(stream)
	if string(got) != "# Fresh regenerated body\n" {
		t.Errorf("playbook reader = %q, want the authoritative Final text", got)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if fe.calls != 1 || fe.gotKind != KindReengageRegenerate {
		t.Errorf("Events called %d times, kind=%v; want 1 regenerate", fe.calls, fe.gotKind)
	}
	if res := <-actCh; res == nil {
		t.Error("activity feed missing reasoning, tool, and/or streamed-delta line")
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
	go wantActivity(t, activity, "# Revised\n")

	got, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(got) != "# Revised fix\n" {
		t.Errorf("playbook reader = %q, want the authoritative Final text", got)
	}
	if fe.gotKind != KindReengageFollowup {
		t.Errorf("kind = %v, want followup", fe.gotKind)
	}
	if fe.gotFailed != failed {
		t.Errorf("failedOutput = %q, want %q", fe.gotFailed, failed)
	}
}

// FinalPlaybook via the EVENT path (stage 2): the prompt kind is finalplaybook, the
// base + change are threaded through, the StreamMode is Replace, the playbook
// streams, and (stage 2) NOTHING is persisted — no cache entry is written.
func TestFinalPlaybook_EventPath_ReplaceNoPersist(t *testing.T) {
	root := t.TempDir()
	fe := &fakeEvents{delta: "# Playbook — fix\n", final: "# Playbook — fix\nclean setup\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:      sampleReq(),
		Events:   fe.fn,
		CtxHash:  "ctxhash",
		ReqHash:  "reqhash",
		DataRoot: root,
	})

	const change = "# Troubleshoot\nthe fixes that worked\n"
	stream, activity, mode, err := o.FinalPlaybook("", change)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	if activity == nil {
		t.Fatal("event path must return a non-nil activity channel")
	}
	go wantActivity(t, activity, "# Playbook — fix\n")

	got, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(got) != "# Playbook — fix\nclean setup\n" {
		t.Errorf("playbook reader = %q, want the authoritative Final text", got)
	}
	if fe.gotKind != KindReengageFinalPlaybook {
		t.Errorf("kind = %v, want finalplaybook", fe.gotKind)
	}
	if fe.gotBase != "" {
		t.Errorf("fresh: base = %q, want empty", fe.gotBase)
	}
	if fe.gotFailed != change {
		t.Errorf("change = %q, want %q", fe.gotFailed, change)
	}

	// Stage 2 is GENERATE-ONLY: no cache entry must be written (persistence is stage 3).
	if matches, _ := filepath.Glob(filepath.Join(root, "cache", "ctxhash", "*")); len(matches) != 0 {
		t.Errorf("stage 2 FinalPlaybook must NOT persist a cache entry, found %v", matches)
	}
}

// FinalPlaybook in AMEND mode threads the base playbook through to the producer.
func TestFinalPlaybook_AmendThreadsBase(t *testing.T) {
	fe := &fakeEvents{delta: "# Playbook\n", final: "# Playbook\nupdated\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:    sampleReq(),
		Events: fe.fn,
	})

	const base = "# Playbook — existing\nstep one\n"
	const change = "also configure the NDK"
	stream, _, mode, err := o.FinalPlaybook(base, change)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	_, _ = io.ReadAll(stream)
	_ = stream.Close()
	if fe.gotKind != KindReengageFinalPlaybook {
		t.Errorf("kind = %v, want finalplaybook", fe.gotKind)
	}
	if fe.gotBase != base {
		t.Errorf("amend: base = %q, want %q", fe.gotBase, base)
	}
	if fe.gotFailed != change {
		t.Errorf("amend: change = %q, want %q", fe.gotFailed, change)
	}
}

// When Events is nil the methods fall back to the text Agent path unchanged: a nil
// activity channel and the Agent's canned stream.
func TestReengage_FallsBackToTextWhenNoEvents(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
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
