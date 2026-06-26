package ui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEffectiveTitle asserts the --title flag OVERRIDES the H1/front-matter title
// when set, and falls back to the derived title (incl. empty) when absent.
func TestEffectiveTitle(t *testing.T) {
	cases := []struct {
		flag, derived, want string
	}{
		{"", "", ""},
		{"", "Derived", "Derived"},
		{"Flag Wins", "Derived", "Flag Wins"},
		{"   ", "Derived", "Derived"}, // blank flag → derived
		{"Flag Only", "", "Flag Only"},
	}
	for _, c := range cases {
		if got := effectiveTitle(c.flag, c.derived); got != c.want {
			t.Errorf("effectiveTitle(%q, %q) = %q, want %q", c.flag, c.derived, got, c.want)
		}
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// wrote (the no-TTY render path prints staticRender to stdout).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

// TestRunTitleFlagSetsHeader asserts `run --title X` makes the pager header show X,
// overriding the playbook's H1 (the no-TTY render path → staticRender → header()).
func TestRunTitleFlagSetsHeader(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte("# Derived H1 Title\n\nsome body line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldArgs := os.Args
	os.Args = []string{"ai-playbook", "run", "--title", "Override Header", file}
	defer func() { os.Args = oldArgs }()

	out := captureStdout(t, func() { Main() })
	if !strings.Contains(out, "Override Header") {
		t.Errorf("output missing the --title header %q\n%s", "Override Header", out)
	}
	if strings.Contains(out, "Derived H1 Title") {
		t.Errorf("--title must OVERRIDE the H1; output still shows the H1:\n%s", out)
	}
	if !strings.Contains(out, "▓▓▓") {
		t.Errorf("header must keep the ▓▓▓ styling:\n%s", out)
	}
}

// TestRunNoTitleUsesH1 asserts that without --title the derived H1 still drives the
// header (the override is opt-in).
func TestRunNoTitleUsesH1(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte("# Derived H1 Title\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldArgs := os.Args
	os.Args = []string{"ai-playbook", "run", file}
	defer func() { os.Args = oldArgs }()

	out := captureStdout(t, func() { Main() })
	if !strings.Contains(out, "Derived H1 Title") {
		t.Errorf("no --title must keep the H1 header:\n%s", out)
	}
}

// TestRunStreamSeedsTitle asserts StreamOptions.Title seeds the session pager's
// working header (the escalate path's classify label), via the no-TTY render path.
func TestRunStreamSeedsTitle(t *testing.T) {
	out := captureStdout(t, func() {
		RunStream(strings.NewReader("authoring in progress\n"), StreamOptions{Title: "Fix Gradle Build"})
	})
	if !strings.Contains(out, "Fix Gradle Build") {
		t.Errorf("RunStream did not seed the header from StreamOptions.Title:\n%s", out)
	}
}
