package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ai-playbook/driver"
)

// newTestDriver spawns a controlled-rc zsh (a minimal .zshrc — no p10k/mise), the
// same fixture approach as driver_test.go, so the orchestrator tests drive a real
// shell deterministically.
func newTestDriver(t *testing.T) *driver.Driver {
	t.Helper()
	zdot := t.TempDir()
	rc := "tfn() { print -r -- FN_OK }\n"
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte(rc), 0644); err != nil {
		t.Fatal(err)
	}
	d, err := driver.Open(driver.Options{Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// recMux records the payloads handed to Copy/Play.
type recMux struct {
	copied []string
	played []string
}

func (m *recMux) Copy(text string) error { m.copied = append(m.copied, text); return nil }
func (m *recMux) Play(cmd string) error  { m.played = append(m.played, cmd); return nil }

// The load-bearing one: value-passing persists across separate Do(run) calls.
func TestValuePassingAcrossBlocks(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})

	if r, err := o.Do(Action{Kind: KindRun, ID: "a", Payload: "print -r -- HELLO"}); err != nil || r.Out != "HELLO" || r.Exit != 0 {
		t.Fatalf("block a → %+v err=%v", r, err)
	}
	// A later block (no id) reads block a's exported output.
	r, err := o.Do(Action{Kind: KindRun, Payload: "print -r -- got:$AAS_OUT_a"})
	if err != nil {
		t.Fatalf("block b err=%v", err)
	}
	if r.Out != "got:HELLO" {
		t.Errorf("AAS_OUT_a did not propagate → %q (want got:HELLO)", r.Out)
	}
	// LAST_* and AAS_EXIT_<id> propagate too.
	if r, err := o.Do(Action{Kind: KindRun, Payload: "print -r -- last:$LAST_EXCODE exit:$AAS_EXIT_a"}); err != nil || r.Out != "last:0 exit:0" {
		t.Errorf("LAST_EXCODE/AAS_EXIT_a → %q err=%v", r.Out, err)
	}
}

// sanitized key: a non-[A-Za-z0-9_] char in the id maps to _.
func TestValuePassingKeySanitized(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	if _, err := o.Do(Action{Kind: KindRun, ID: "step-1", Payload: "print -r -- X"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := o.Do(Action{Kind: KindRun, Payload: "print -r -- $AAS_OUT_step_1"}); r.Out != "X" {
		t.Errorf("sanitized key AAS_OUT_step_1 → %q (want X)", r.Out)
	}
}

// stop: a long run is interrupted by a concurrent Do(stop) and returns promptly.
func TestStopInterruptsRun(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	done := make(chan driver.Result, 1)
	go func() {
		r, _ := o.Do(Action{Kind: KindRun, Payload: "sleep 30"})
		done <- r
	}()
	// Wait until the command is actually running (a foreground pgrp appears).
	for i := 0; i < 150; i++ {
		time.Sleep(40 * time.Millisecond)
		if o.Drv.Pgrp() > 0 {
			break
		}
	}
	if _, err := o.Do(Action{Kind: KindStop}); err != nil {
		t.Fatalf("stop err=%v", err)
	}
	select {
	case r := <-done:
		if r.Exit == 0 && !r.TimedOut {
			t.Errorf("stopped run should not report clean success → %+v", r)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("stop did not end the run")
	}
}

func TestCopyPlayRecorded(t *testing.T) {
	m := &recMux{}
	o := New(newTestDriver(t), m)
	if _, err := o.Do(Action{Kind: KindCopy, Payload: "to-clip"}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Do(Action{Kind: KindPlay, Payload: "to-pane"}); err != nil {
		t.Fatal(err)
	}
	if len(m.copied) != 1 || m.copied[0] != "to-clip" {
		t.Errorf("copy not recorded → %v", m.copied)
	}
	if len(m.played) != 1 || m.played[0] != "to-pane" {
		t.Errorf("play not recorded → %v", m.played)
	}
}

func TestDeferredKindsNotImplemented(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	for _, k := range []Kind{KindViewDiff, KindApplyDiff, KindUndoDiff, KindRegenerate, KindFollowup, KindWrapup} {
		if _, err := o.Do(Action{Kind: k}); !errors.Is(err, ErrNotImplemented) {
			t.Errorf("%s → err=%v (want ErrNotImplemented)", k, err)
		}
	}
}

func TestKindString(t *testing.T) {
	cases := map[Kind]string{
		KindCopy: "copy", KindPlay: "play", KindRun: "run", KindStop: "stop",
		KindViewDiff: "view-diff", KindApplyDiff: "apply-diff", KindUndoDiff: "undo-diff",
		KindRegenerate: "regenerate", KindFollowup: "followup", KindWrapup: "wrapup",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}
