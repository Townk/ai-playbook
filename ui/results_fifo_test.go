package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestResultsFifoFlagAccepted guards the regression where --results-fifo was
// not defined and the binary crashed with exit 2. It builds the real binary,
// creates the results FIFO, feeds it EOF immediately (so the drain goroutine
// unblocks), feeds the input stream over stdin, and asserts exit 0 — not exit 2.
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

	// Create the results FIFO the binary drains.
	resultsFifo := filepath.Join(dir, "results")
	if err := syscall.Mkfifo(resultsFifo, 0o600); err != nil {
		t.Fatalf("mkfifo %s: %v", resultsFifo, err)
	}

	// Run the binary via the `run` subcommand (no TTY → static-render path,
	// exits after draining input). Input comes over stdin.
	cmd := exec.Command(bin,
		"run",
		"--results-fifo", resultsFifo,
	)
	cmd.Stdin = strings.NewReader("hello\n")
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
