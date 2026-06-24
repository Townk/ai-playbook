package ui

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// syscallMkfifo wraps syscall.Mkfifo so tests can skip when unavailable.
func syscallMkfifo(path string) error {
	return syscall.Mkfifo(path, 0o600)
}

func TestEmitActionWritesFifoLine(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "act")
	m := model{fifoPath: fifo}
	m.emitAction(Button{Kind: "copy", BlockID: "b1", Payload: "echo hi\nls"})
	f, err := os.Open(fifo)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rec, _ := bufio.NewReader(f).ReadString('\x1e')
	rec = strings.TrimSuffix(rec, "\x1e")
	// 3-field: kind \x1f blockID \x1f payload
	kind, rest, ok := strings.Cut(rec, "\x1f")
	if !ok || kind != "copy" {
		t.Fatalf("kind = %q ok = %v", kind, ok)
	}
	blockID, payload, ok2 := strings.Cut(rest, "\x1f")
	if !ok2 {
		t.Fatalf("missing second \\x1f in record %q", rec)
	}
	if blockID != "b1" {
		t.Fatalf("blockID = %q, want %q", blockID, "b1")
	}
	if payload != "echo hi\nls" {
		t.Fatalf("payload = %q, want %q", payload, "echo hi\nls")
	}
}

func TestEmitActionCarriesBlockID(t *testing.T) {
	dir := t.TempDir()
	fifo := dir + "/act"
	if err := syscallMkfifo(fifo); err != nil {
		t.Skip("mkfifo unavailable")
	}
	m := model{fifoPath: fifo}
	got := make(chan string, 1)
	// emitAction opens the write end with O_NONBLOCK, which succeeds only when a
	// reader is already open. Open the read end first (blocking open waits until a
	// writer appears), so we run it concurrently with emitAction: the two opens
	// unblock each other once both are in-flight.
	go func() {
		// Opening O_RDONLY (blocking) waits until a writer appears; emitAction's
		// O_WRONLY|O_NONBLOCK open will see this reader and succeed.
		rf, err := os.Open(fifo)
		if err != nil {
			got <- ""
			return
		}
		b, _ := io.ReadAll(rf)
		rf.Close()
		got <- string(b)
	}()
	// Give the goroutine a moment to reach the blocking open before we try the
	// nonblocking write (which would get ENXIO if no reader is present yet).
	// A brief sleep is sufficient — the goroutine only needs to enter open(2).
	time.Sleep(5 * time.Millisecond)
	m.emitAction(Button{Kind: "run", BlockID: "fix", Payload: "ls"})
	select {
	case s := <-got:
		if s != "run\x1ffix\x1fls\x1e" {
			t.Fatalf("record = %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("no record written")
	}
}

func TestEmitActionNoFifoIsNoop(t *testing.T) {
	m := model{fifoPath: ""}
	m.emitAction(Button{Kind: "copy", Payload: "x"}) // must not panic
}
