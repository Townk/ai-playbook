package skillcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/skills"
)

// runCmd drives run() with captured stdout/stderr, returning the exit code
// and both streams.
func runCmd(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestSkillShow_PrintsEmbeddedSkill(t *testing.T) {
	code, stdout, stderr := runCmd("show")
	if code != 0 {
		t.Fatalf("skill show exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != string(skills.PlaybookAuthoring) {
		t.Errorf("skill show output differs from the embedded SKILL (%d vs %d bytes)", len(stdout), len(skills.PlaybookAuthoring))
	}
}

func TestSkillShow_RejectsExtraArgs(t *testing.T) {
	code, _, stderr := runCmd("show", "extra")
	if code != 2 {
		t.Errorf("skill show extra exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unexpected argument") {
		t.Errorf("stderr = %q, want an unexpected-argument message", stderr)
	}
}

func TestSkillInstall_ToDirCreatesDirsAndFile(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := runCmd("install", "--to", dir)
	if code != 0 {
		t.Fatalf("skill install exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	target := filepath.Join(dir, "playbook-authoring", "SKILL.md")
	if strings.TrimSpace(stdout) != target {
		t.Errorf("stdout = %q, want the installed path %q", stdout, target)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read installed file: %v", err)
	}
	if !bytes.Equal(got, skills.PlaybookAuthoring) {
		t.Errorf("installed file differs from the embedded SKILL (%d vs %d bytes)", len(got), len(skills.PlaybookAuthoring))
	}
}

func TestSkillInstall_DefaultTargetUnderHome(t *testing.T) {
	home := t.TempDir()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = orig }()

	code, stdout, stderr := runCmd("install")
	if code != 0 {
		t.Fatalf("skill install exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	target := filepath.Join(home, ".claude", "skills", "playbook-authoring", "SKILL.md")
	if strings.TrimSpace(stdout) != target {
		t.Errorf("stdout = %q, want the default path %q", stdout, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("default target not installed: %v", err)
	}
}

func TestSkillInstall_RefusesExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "playbook-authoring", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("pre-existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCmd("install", "--to", dir)
	if code == 0 {
		t.Fatal("skill install over an existing file exit = 0, want non-zero")
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("stderr = %q, want a message pointing at --force", stderr)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "pre-existing" {
		t.Errorf("existing file was modified without --force: %q", got)
	}
}

func TestSkillInstall_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "playbook-authoring", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCmd("install", "--to", dir, "--force")
	if code != 0 {
		t.Fatalf("skill install --force exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.TrimSpace(stdout) != target {
		t.Errorf("stdout = %q, want the installed path %q", stdout, target)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, skills.PlaybookAuthoring) {
		t.Errorf("--force did not replace the stale file with the embedded SKILL")
	}
}

func TestSkill_UnknownAndMissingSubcommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing", nil},
		{"unknown", []string{"bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runCmd(tc.args...)
			if code != 2 {
				t.Errorf("skill %v exit = %d, want 2", tc.args, code)
			}
			if !strings.Contains(stderr, "show|install") {
				t.Errorf("stderr = %q, want usage naming show|install", stderr)
			}
		})
	}
}

// TestMain_DispatchesViaOsArgs covers the thin Main → run bridge (os.Args
// slicing): `<prog> skill show` exits 0 through the real entrypoint.
func TestMain_DispatchesViaOsArgs(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()
	os.Args = []string{"ai-playbook", "skill", "show"}
	// Silence the embedded SKILL dump: Main writes to os.Stdout by design.
	origOut := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devnull
	defer func() { os.Stdout = origOut; devnull.Close() }()
	if code := Main(); code != 0 {
		t.Fatalf("Main() = %d, want 0", code)
	}
}
