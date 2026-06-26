package input

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestProcessingShowsSpinnerAndStatus(t *testing.T) {
	m := newProcessingModel(defaultTheme(), "ai-playbook", 50, 12)
	m2, _ := m.Update(statusMsg("Looking up docs"))
	out := strip(m2.(processingModel).View().Content) // rendered frame
	if !strings.Contains(out, "Looking up docs") {
		t.Fatalf("status label not shown: %q", out)
	}
	if !strings.Contains(out, "▓▓▓ ai-playbook") {
		t.Fatalf("same framed title expected: %q", out)
	}
}

func TestProcessingQuitsOnClose(t *testing.T) {
	m := newProcessingModel(defaultTheme(), "ai-playbook", 50, 12)
	_, cmd := m.Update(closeMsg{})
	if cmd == nil {
		t.Fatal("close must return a quit cmd")
	}
	// Verify the cmd is actually a quit command by executing it
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("close cmd must produce tea.QuitMsg, got %T", msg)
	}
}

// drain collects messages from ch until it is closed or the timeout fires.
func drain(t *testing.T, ch <-chan tea.Msg) []tea.Msg {
	t.Helper()
	var msgs []tea.Msg
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return msgs
			}
			msgs = append(msgs, msg)
		case <-timeout:
			t.Fatalf("scanRecords did not close channel; got %d msgs so far", len(msgs))
			return msgs
		}
	}
}

// A batched status+close delivered in a single read must yield BOTH a
// statusMsg AND a closeMsg — the close is never dropped.
func TestScanRecordsBatchedStatusThenCloseNotDropped(t *testing.T) {
	in := strings.NewReader("status" + recUS + "working" + recRS + "close" + recRS)
	ch := make(chan tea.Msg, 8)
	go scanRecords(in, ch)

	msgs := drain(t, ch)

	var sawStatus, sawClose bool
	for _, m := range msgs {
		switch v := m.(type) {
		case statusMsg:
			if string(v) == "working" {
				sawStatus = true
			}
		case closeMsg:
			sawClose = true
		}
	}
	if !sawStatus {
		t.Fatalf("expected statusMsg(\"working\"); got %#v", msgs)
	}
	if !sawClose {
		t.Fatalf("batched close was dropped; got %#v", msgs)
	}
}

// EOF after a status (writer gone, no explicit close) must still yield exactly
// one closeMsg so the float quits.
func TestScanRecordsEOFAfterStatusYieldsClose(t *testing.T) {
	in := strings.NewReader("status" + recUS + "thinking" + recRS)
	ch := make(chan tea.Msg, 8)
	go scanRecords(in, ch)

	msgs := drain(t, ch)

	var sawStatus, closeCount int
	for _, m := range msgs {
		switch m.(type) {
		case statusMsg:
			sawStatus++
		case closeMsg:
			closeCount++
		}
	}
	if sawStatus != 1 {
		t.Fatalf("expected one statusMsg before EOF; got %#v", msgs)
	}
	if closeCount != 1 {
		t.Fatalf("EOF must yield exactly one closeMsg; got %d: %#v", closeCount, msgs)
	}
}

func TestProcessingModelTracksWindowResize(t *testing.T) {
	m := newProcessingModel(defaultTheme(), "ai-playbook", 50, 12)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	pm := m2.(processingModel)
	if pm.width != 80 {
		t.Fatalf("expected width 80 after WindowSizeMsg, got %d", pm.width)
	}
	if pm.height != 24 {
		t.Fatalf("expected height 24 after WindowSizeMsg, got %d", pm.height)
	}
}

// The processing frame must fill the SAME height as the input dialog it replaced
// (so the float shows no black gap), with the spinner centered and no hint.
func TestProcessingFillsPaneHeight(t *testing.T) {
	m := newProcessingModel(defaultTheme(), "ai-playbook", 56, 14)
	out := m.View().Content
	if n := len(strings.Split(out, "\n")); n != 14 {
		t.Fatalf("processing frame should fill the pane height (14), got %d lines:\n%s", n, strip(out))
	}
	plain := strip(out)
	if !strings.Contains(plain, "▓▓▓ ai-playbook") {
		t.Fatalf("title row expected: %q", plain)
	}
	if !strings.Contains(plain, "Processing…") {
		t.Fatalf("centered label expected: %q", plain)
	}
}
