package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

// A KindRun Action with StdinPath set feeds that file into the block's stdin, so
// a `cat`-style consumer emits the piped bytes exactly. Empty StdinPath keeps the
// pre-existing </dev/null behavior (no stdin data).
func TestActionStdinPathThreadsToRun(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})

	stdin := filepath.Join(t.TempDir(), "piped")
	const data = "PIPED_STDIN_DATA"
	if err := os.WriteFile(stdin, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	// With StdinPath set, `cat` reads the file's bytes off stdin.
	r, err := o.Do(Action{Kind: KindRun, ID: "cons", Payload: "cat", StdinPath: stdin})
	if err != nil {
		t.Fatalf("run with StdinPath: %v", err)
	}
	if r.Out != data {
		t.Errorf("StdinPath not wired to stdin: Out=%q want %q", r.Out, data)
	}

	// Without StdinPath, stdin is </dev/null → cat reads nothing.
	r2, err := o.Do(Action{Kind: KindRun, ID: "cons2", Payload: "cat"})
	if err != nil {
		t.Fatalf("run without StdinPath: %v", err)
	}
	if r2.Out != "" {
		t.Errorf("empty StdinPath should keep </dev/null: Out=%q want empty", r2.Out)
	}
}

// The driver's CapturePath resolves a producer's retained-stdout path, which a
// consumer Action can pipe in via StdinPath — the whole from= round-trip through
// the orchestrator seam.
func TestCapturePathPipesProducerToConsumer(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})

	if r, err := o.Do(Action{Kind: KindRun, ID: "prod", Payload: "printf PRODUCED"}); err != nil || r.Exit != 0 {
		t.Fatalf("producer → %+v err=%v", r, err)
	}
	cap := o.Drv.CapturePath("prod")
	if cap == "" {
		t.Fatal("CapturePath(prod) empty — retention path not exposed")
	}
	if _, err := os.Stat(cap); err != nil {
		t.Fatalf("producer capture missing at %s: %v", cap, err)
	}
	r, err := o.Do(Action{Kind: KindRun, ID: "cons", Payload: "cat", StdinPath: cap})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	if r.Out != "PRODUCED" {
		t.Errorf("consumer stdin from producer capture = %q, want PRODUCED", r.Out)
	}
}
