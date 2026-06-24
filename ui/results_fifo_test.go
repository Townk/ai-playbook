package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestResultsFifoFlagAccepted guards the regression where --results-fifo was
// not defined and the binary crashed with exit 2. It builds the real binary,
// creates a FIFO, feeds it EOF immediately (so the drain goroutine unblocks),
// and asserts exit 0 — not exit 2.
func TestResultsFifoFlagAccepted(t *testing.T) {
	// Build the root ai-playbook binary into a temp directory. The pager now
	// lives in package ui and is reached via the `run` subcommand, so we build
	// the module root (..) rather than this package.
	dir := t.TempDir()
	bin := filepath.Join(dir, "ai-playbook")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	// Create the three FIFOs the binary expects.
	inputFifo := filepath.Join(dir, "input")
	actionsFifo := filepath.Join(dir, "actions")
	resultsFifo := filepath.Join(dir, "results")
	for _, p := range []string{inputFifo, actionsFifo, resultsFifo} {
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatalf("mkfifo %s: %v", p, err)
		}
	}

	// Run the binary via the `run` subcommand (no TTY → static-render path,
	// exits after draining input).
	cmd := exec.Command(bin,
		"run",
		"--input-fifo", inputFifo,
		"--actions-fifo", actionsFifo,
		"--results-fifo", resultsFifo,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Write EOF to the results FIFO (open + immediate close) so the drain
	// goroutine unblocks right away.
	go func() {
		rf, err := os.OpenFile(resultsFifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		rf.Close() // EOF
	}()

	// Write a minimal payload to the input FIFO and close it so the binary
	// finishes the static-render path and exits.
	go func() {
		wf, err := os.OpenFile(inputFifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		wf.WriteString("hello\n")
		wf.Close()
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("binary exited with error (want exit 0, got %v) — flag not accepted or drain goroutine blocked", err)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("binary did not exit within 5s — likely blocked on FIFO open")
	}
}
