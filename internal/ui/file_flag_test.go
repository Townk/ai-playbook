package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunFileFlagRendersLikePositional asserts `run --file X` renders the same
// playbook as the bare positional `run X` (the no-TTY render path → staticRender).
// The --file flag is the source the internal callers (serveCachedPlaybook,
// AnswerMain) migrate to; it must be honored identically to the positional arg.
func TestRunFileFlagRendersLikePositional(t *testing.T) {
	file := filepath.Join(t.TempDir(), "pb.md")
	if err := os.WriteFile(file, []byte("# File Flag Title\n\nsome body line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	render := func(args []string) string {
		oldArgs := os.Args
		os.Args = args
		defer func() { os.Args = oldArgs }()
		return captureStdout(t, func() { Main() })
	}

	positional := render([]string{"ai-playbook", "run", file})
	viaFlag := render([]string{"ai-playbook", "run", "--file", file})

	if !strings.Contains(viaFlag, "File Flag Title") {
		t.Errorf("--file output missing the playbook title:\n%s", viaFlag)
	}
	if positional != viaFlag {
		t.Errorf("`run --file X` must render identically to `run X`\npositional:\n%s\n--file:\n%s", positional, viaFlag)
	}
}

// TestRunFileFlagWinsOverPositional asserts that when both --file and a bare
// positional are given, --file wins as the source file.
func TestRunFileFlagWinsOverPositional(t *testing.T) {
	dir := t.TempDir()
	fileSrc := filepath.Join(dir, "from-flag.md")
	posSrc := filepath.Join(dir, "from-positional.md")
	if err := os.WriteFile(fileSrc, []byte("# Flag Source\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(posSrc, []byte("# Positional Source\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"ai-playbook", "run", "--file", fileSrc, posSrc}
	defer func() { os.Args = oldArgs }()

	out := captureStdout(t, func() { Main() })
	if !strings.Contains(out, "Flag Source") {
		t.Errorf("--file must win as the source; output missing 'Flag Source':\n%s", out)
	}
	if strings.Contains(out, "Positional Source") {
		t.Errorf("--file must win over the positional; output still shows the positional source:\n%s", out)
	}
}
