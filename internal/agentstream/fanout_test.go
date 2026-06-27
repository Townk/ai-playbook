package agentstream

import (
	"io"
	"testing"
	"time"
)

// drainActivity reads the activity channel to close, returning everything it saw.
// It is safe to call from a goroutine (no t.Fatal): on timeout it returns what it
// has so the caller's assertion surfaces the failure on the test goroutine.
func drainActivity(ch <-chan string) []string {
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

// TestFanOut_DeltasReasoningToolFinal feeds the full event mix and asserts the
// NEW contract: the pipe (the doc) yields the authoritative Final text, the
// activity feed carries the streamed TextDelta texts in order ALONGSIDE the
// reasoning + tool strings (all transient narration), the body buffer equals the
// Final, and closeFn is called.
func TestFanOut_DeltasReasoningToolFinal(t *testing.T) {
	events := make(chan Event)
	closed := make(chan struct{})
	closeFn := func() error { close(closed); return nil }

	reader, activity, fo := FanOut(events, closeFn, 16)

	go func() {
		events <- Event{Kind: TextDelta, Text: "# Step one\n"}
		events <- Event{Kind: Reasoning, Text: "thinking about the failure"}
		events <- Event{Kind: TextDelta, Text: "run make test\n"}
		events <- Event{Kind: ToolActivity, Text: "run: make test"}
		events <- Event{Kind: Final, Text: "# Step one\nrun make test\n"}
		close(events)
	}()

	// Drain the activity channel concurrently so the pump never blocks on a full
	// channel while we read the pipe.
	actCh := make(chan []string, 1)
	go func() { actCh <- drainActivity(activity) }()

	playbook, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read playbook pipe: %v", err)
	}
	// The doc pipe carries the authoritative Final text (the playbook), NOT the
	// interim streamed narration.
	want := "# Step one\nrun make test\n"
	if string(playbook) != want {
		t.Errorf("playbook pipe = %q, want the Final text %q", playbook, want)
	}

	// The activity feed carries the streamed TextDelta texts (transient narration)
	// in order, interleaved with the reasoning + tool lines.
	got := <-actCh
	wantAct := []string{"# Step one\n", "thinking about the failure", "run make test\n", "run: make test"}
	if len(got) != len(wantAct) {
		t.Fatalf("activity = %v, want %v", got, wantAct)
	}
	for i := range wantAct {
		if got[i] != wantAct[i] {
			t.Errorf("activity[%d] = %q, want %q", i, got[i], wantAct[i])
		}
	}

	if fo.Body() != want {
		t.Errorf("body = %q, want %q (Final authoritative)", fo.Body(), want)
	}

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Error("closeFn was not called on event-channel close")
	}
}

// TestFanOut_FinalOnly: a harness that emits only Final (no TextDelta) must still
// write the Final text to the pipe and set it as the body.
func TestFanOut_FinalOnly(t *testing.T) {
	events := make(chan Event)
	reader, activity, fo := FanOut(events, func() error { return nil }, 16)

	go func() {
		events <- Event{Kind: Final, Text: "# Whole playbook\n"}
		close(events)
	}()
	go drainActivity(activity)

	playbook, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read playbook pipe: %v", err)
	}
	if string(playbook) != "# Whole playbook\n" {
		t.Errorf("Final-only pipe = %q, want the Final text", playbook)
	}
	if fo.Body() != "# Whole playbook\n" {
		t.Errorf("Final-only body = %q, want the Final text", fo.Body())
	}
}

// TestFanOut_FinalIsTheDoc: when both deltas and a Final arrive, the doc pipe
// carries the authoritative Final text (the streamed deltas are transient
// narration on the activity feed, NOT the doc), and the stored body is the Final.
func TestFanOut_FinalIsTheDoc(t *testing.T) {
	events := make(chan Event)
	reader, activity, fo := FanOut(events, func() error { return nil }, 16)

	go func() {
		events <- Event{Kind: TextDelta, Text: "partial "}
		events <- Event{Kind: TextDelta, Text: "stream"}
		events <- Event{Kind: Final, Text: "FINAL BODY"}
		close(events)
	}()

	actCh := make(chan []string, 1)
	go func() { actCh <- drainActivity(activity) }()

	playbook, _ := io.ReadAll(reader)
	if string(playbook) != "FINAL BODY" {
		t.Errorf("pipe = %q, want the authoritative Final text", playbook)
	}
	if fo.Body() != "FINAL BODY" {
		t.Errorf("body = %q, want the authoritative Final text", fo.Body())
	}
	// The streamed deltas became transient narration on the activity feed.
	got := <-actCh
	wantAct := []string{"partial ", "stream"}
	if len(got) != len(wantAct) {
		t.Fatalf("activity = %v, want the streamed deltas %v", got, wantAct)
	}
	for i := range wantAct {
		if got[i] != wantAct[i] {
			t.Errorf("activity[%d] = %q, want %q", i, got[i], wantAct[i])
		}
	}
}

// TestFanOut_NoFinalFallback: when a harness streams TextDeltas but never emits a
// Final, the accumulated deltas are the fallback doc (pipe) AND body.
func TestFanOut_NoFinalFallback(t *testing.T) {
	events := make(chan Event)
	reader, activity, fo := FanOut(events, func() error { return nil }, 16)

	go func() {
		events <- Event{Kind: TextDelta, Text: "partial "}
		events <- Event{Kind: TextDelta, Text: "stream"}
		// No Final emitted.
		close(events)
	}()
	go drainActivity(activity)

	playbook, _ := io.ReadAll(reader)
	if string(playbook) != "partial stream" {
		t.Errorf("pipe = %q, want the accumulated deltas (no-Final fallback)", playbook)
	}
	if fo.Body() != "partial stream" {
		t.Errorf("body = %q, want the accumulated deltas (no-Final fallback)", fo.Body())
	}
}
