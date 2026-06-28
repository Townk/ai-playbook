package ui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDumpDocument_WritesVerbatim(t *testing.T) {
	md := "# Playbook — X\n\nintro\n\n```bash {id=a}\necho hi\n```\n"
	path := dumpDocument(md)
	if path == "" {
		t.Fatal("dumpDocument returned empty path")
	}
	defer os.Remove(path)
	if !strings.HasPrefix(path, "/tmp/apb-doc-dump-") || !strings.HasSuffix(path, ".md") {
		t.Errorf("unexpected dump path %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	if string(got) != md {
		t.Errorf("dump not verbatim:\n got %q\nwant %q", got, md)
	}
}

// ctrl+x in the viewer dumps the current document and reports the path in the
// status line.
func TestCtrlX_DumpsAndReportsPath(t *testing.T) {
	m := newModel("Claude Code", "# Playbook — X\n\nbody paragraph\n")
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	rm := nm.(model)
	if !strings.Contains(rm.status, "dumped document → /tmp/apb-doc-dump-") {
		t.Fatalf("ctrl+x should set a dump status with the path, got %q", rm.status)
	}
	// the reported file exists and holds the document
	path := strings.TrimPrefix(rm.status, "dumped document → ")
	defer os.Remove(path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("dumped file not found: %v", err)
	}
	if !strings.Contains(string(got), "# Playbook — X") {
		t.Errorf("dumped file missing the document: %q", got)
	}
}
