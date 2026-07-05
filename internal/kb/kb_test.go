package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRoot_DataDirOverride(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", "/explicit/data")
	if got := DefaultRoot(); got != "/explicit/data" {
		t.Fatalf("DefaultRoot = %q, want /explicit/data", got)
	}
}

func TestDefaultRoot_XDG(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if got := DefaultRoot(); got != filepath.Join("/xdg", "ai-playbook") {
		t.Fatalf("DefaultRoot = %q, want /xdg/ai-playbook", got)
	}
}

// Path matches the shell layout: $root/projects/<sha1(projectRoot)>/knowledge.md.
// The key is the SHA-1 of the literal path string (verified against the known
// shasum of "/p").
func TestPath_ShellLayout(t *testing.T) {
	// printf '%s' /p | shasum -a 1  →  the value below
	const wantKey = "ca85a389d362533706fa2f54ec9af609a5b8a397"
	got := Path("/root", "/p")
	want := filepath.Join("/root", "projects", wantKey, "knowledge.md")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

// TestLoadProject_MissingIsEmpty preserves the retired LoadFrom "missing → empty"
// coverage on the migration-aware reader.
func TestLoadProject_MissingIsEmpty(t *testing.T) {
	if kb := LoadProject(t.TempDir(), "/some/project"); kb != "" {
		t.Fatalf("missing KB should be empty, got %q", kb)
	}
}

// TestLoadGlobal_MissingIsEmpty preserves the same "missing → empty" contract for
// the global reader.
func TestLoadGlobal_MissingIsEmpty(t *testing.T) {
	if kb := LoadGlobal(t.TempDir()); kb != "" {
		t.Fatalf("missing global KB should be empty, got %q", kb)
	}
}

// captureStderr runs f and returns whatever it wrote to os.Stderr.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	f()
	w.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := r.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return b.String()
}

func TestCapped_UnderLimitUnchangedNoNote(t *testing.T) {
	const content KnowledgeBase = "## System\n- a\n- b\n"
	var got KnowledgeBase
	note := captureStderr(t, func() { got = Capped(content, 4096) })
	if got != content {
		t.Fatalf("Capped mutated under-limit content: %q", got)
	}
	if note != "" {
		t.Fatalf("Capped wrote a note for under-limit content: %q", note)
	}
}

func TestCapped_ZeroLimitDisabled(t *testing.T) {
	content := KnowledgeBase(strings.Repeat("- fact\n", 1000))
	note := captureStderr(t, func() {
		if got := Capped(content, 0); got != content {
			t.Fatalf("limit<=0 must disable the cap")
		}
	})
	if note != "" {
		t.Fatalf("disabled cap must not write a note: %q", note)
	}
}

// TestCapped_TruncatesAtBulletBoundary: an oversized file is cut at a line
// boundary (never mid-bullet), keeps the HEAD, ends in a single newline, and
// emits the stderr note.
func TestCapped_TruncatesAtBulletBoundary(t *testing.T) {
	// 20 bullets of a fixed width; each line is "- bullet NN\n".
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&sb, "- bullet %02d\n", i)
	}
	content := KnowledgeBase(sb.String())
	// A limit that lands mid-line: 5 whole lines is 60 bytes ("- bullet 00\n"=12).
	const limit = 65
	var got KnowledgeBase
	note := captureStderr(t, func() { got = Capped(content, limit) })

	if note == "" {
		t.Fatalf("oversized file must emit a stderr note")
	}
	if len(got) > limit {
		t.Fatalf("truncated content %d bytes exceeds limit %d", len(got), limit)
	}
	// Head kept: first bullet present, and no partial line survived.
	if !strings.HasPrefix(string(got), "- bullet 00\n") {
		t.Fatalf("head not kept: %q", got)
	}
	for _, ln := range strings.Split(strings.TrimRight(string(got), "\n"), "\n") {
		if ln != "" && !strings.HasPrefix(ln, "- bullet ") {
			t.Fatalf("a line was split mid-bullet: %q", ln)
		}
	}
	if !strings.HasSuffix(string(got), "\n") {
		t.Fatalf("truncated content must end in a newline: %q", got)
	}
	// The tail (last bullet) was dropped.
	if strings.Contains(string(got), "- bullet 19") {
		t.Fatalf("tail bullet should have been dropped: %q", got)
	}
}
