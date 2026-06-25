package main

import (
	"io"
	"testing"
	"time"

	"ai-playbook/agentstream"
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
// pipe yields the concatenated playbook, the activity feed carries the reasoning +
// tool strings, the body buffer equals the playbook, and closeFn is called.
func TestFanOut_DeltasReasoningToolFinal(t *testing.T) {
	events := make(chan agentstream.Event)
	closed := make(chan struct{})
	closeFn := func() error { close(closed); return nil }

	reader, activity, fo := fanOut(events, closeFn, 16)

	go func() {
		events <- agentstream.Event{Kind: agentstream.TextDelta, Text: "# Step one\n"}
		events <- agentstream.Event{Kind: agentstream.Reasoning, Text: "thinking about the failure"}
		events <- agentstream.Event{Kind: agentstream.TextDelta, Text: "run make test\n"}
		events <- agentstream.Event{Kind: agentstream.ToolActivity, Text: "run: make test"}
		events <- agentstream.Event{Kind: agentstream.Final, Text: "# Step one\nrun make test\n"}
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
	want := "# Step one\nrun make test\n"
	if string(playbook) != want {
		t.Errorf("playbook pipe = %q, want %q", playbook, want)
	}

	got := <-actCh
	wantAct := []string{"thinking about the failure", "run: make test"}
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
	events := make(chan agentstream.Event)
	reader, activity, fo := fanOut(events, func() error { return nil }, 16)

	go func() {
		events <- agentstream.Event{Kind: agentstream.Final, Text: "# Whole playbook\n"}
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

// TestFanOut_DeltasPreferFinalBody: when both deltas and a Final arrive, the pipe
// carries the streamed deltas while the stored body prefers the Final text.
func TestFanOut_DeltasPreferFinalBody(t *testing.T) {
	events := make(chan agentstream.Event)
	reader, activity, fo := fanOut(events, func() error { return nil }, 16)

	go func() {
		events <- agentstream.Event{Kind: agentstream.TextDelta, Text: "partial "}
		events <- agentstream.Event{Kind: agentstream.TextDelta, Text: "stream"}
		events <- agentstream.Event{Kind: agentstream.Final, Text: "FINAL BODY"}
		close(events)
	}()
	go drainActivity(activity)

	playbook, _ := io.ReadAll(reader)
	if string(playbook) != "partial stream" {
		t.Errorf("pipe = %q, want the streamed deltas", playbook)
	}
	if fo.Body() != "FINAL BODY" {
		t.Errorf("body = %q, want the authoritative Final text", fo.Body())
	}
}
