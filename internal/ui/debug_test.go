package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestDbgGatedByEnv verifies that with AI_PLAYBOOK_DEBUG_LOG set, driving an
// Update code path that calls dbg (quitEvent) appends the expected line to the
// file; and that with the var unset, dbg is a silent no-op that never panics.
func TestDbgGatedByEnv(t *testing.T) {
	// Restore env + resolved handle after the test so we stay hermetic.
	orig, had := os.LookupEnv("AI_PLAYBOOK_DEBUG_LOG")
	t.Cleanup(func() {
		if had {
			os.Setenv("AI_PLAYBOOK_DEBUG_LOG", orig)
		} else {
			os.Unsetenv("AI_PLAYBOOK_DEBUG_LOG")
		}
		resolveDbg()
	})

	logPath := filepath.Join(t.TempDir(), "pager-debug.log")

	// --- Set: dbg must write a line containing the expected text. ---
	os.Setenv("AI_PLAYBOOK_DEBUG_LOG", logPath)
	resolveDbg()

	m := newModel("T", "content")
	m.width, m.height = 80, 24
	m.reader = strings.NewReader("")
	m.parser = &streamParser{}

	_, cmd := m.Update(streamEventsMsg{events: []streamEvent{quitEvent{}}})
	if cmd == nil {
		t.Fatal("quitEvent must return a non-nil cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg from quitEvent")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading debug log: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "[pager] quitEvent received -> tea.Quit") {
		t.Fatalf("debug log missing expected quitEvent line; got:\n%s", got)
	}

	// --- Unset: dbg must be a no-op (no panic, nothing written). ---
	os.Unsetenv("AI_PLAYBOOK_DEBUG_LOG")
	resolveDbg()

	noWritePath := filepath.Join(t.TempDir(), "should-not-exist.log")
	os.Setenv("AI_PLAYBOOK_DEBUG_LOG", noWritePath) // env present but handle not re-resolved
	defer os.Unsetenv("AI_PLAYBOOK_DEBUG_LOG")
	// dbgFile is nil here (we resolved with the var unset), so dbg is a no-op.

	m2 := newModel("T", "content")
	m2.width, m2.height = 80, 24
	m2.reader = strings.NewReader("")
	m2.parser = &streamParser{}
	if _, c := m2.Update(streamEventsMsg{events: []streamEvent{quitEvent{}}}); c == nil {
		t.Fatal("quitEvent must still return a cmd when logging is off")
	}

	if _, err := os.Stat(noWritePath); !os.IsNotExist(err) {
		t.Fatalf("dbg wrote a file while logging was off (stat err=%v)", err)
	}
}
