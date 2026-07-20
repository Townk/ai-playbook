package docscmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/climeta"
)

// runC / runM drive the dispatchers with captured output.
func runC(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runCompletion(args, &out, &errb)
	return code, out.String(), errb.String()
}

func runM(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runMan(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestCompletionShow_PrintsRegistryRender(t *testing.T) {
	code, out, _ := runC(t, "show")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if out != climeta.Zsh() {
		t.Error("show must print the runtime-rendered _ai-playbook verbatim")
	}
	if !strings.Contains(out, "#compdef") {
		t.Errorf("rendered completion looks wrong: %q", out[:60])
	}
}

func TestCompletionInstall_WritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	code, out, errb := runC(t, "install", "--to", dir)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, errb)
	}
	for _, f := range []string{"_ai-playbook", "_ask"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || len(b) == 0 {
			t.Errorf("%s not installed: %v", f, err)
		}
	}
	if !strings.Contains(out, "installed 2 completion file(s)") {
		t.Errorf("summary = %q", out)
	}
}

func TestCompletionInstall_RefusesExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "_ask"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errb := runC(t, "install", "--to", dir)
	if code != 1 || !strings.Contains(errb, "already exists") {
		t.Fatalf("exit=%d stderr=%q, want refusal", code, errb)
	}
	// The refusal must be all-or-nothing: the OTHER file must not be written.
	if _, err := os.Stat(filepath.Join(dir, "_ai-playbook")); err == nil {
		t.Error("refusal must not half-install")
	}
	// --force overwrites.
	if code, _, _ := runC(t, "install", "--to", dir, "--force"); code != 0 {
		t.Fatalf("--force exit = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "_ask")); string(b) == "mine" {
		t.Error("--force must overwrite the existing file")
	}
}

func TestCompletionUninstall_RemovesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if code, _, _ := runC(t, "install", "--to", dir); code != 0 {
		t.Fatal("setup install failed")
	}
	code, out, _ := runC(t, "uninstall", "--to", dir)
	if code != 0 || !strings.Contains(out, "removed 2") {
		t.Fatalf("exit=%d out=%q", code, out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("files left behind: %v", entries)
	}
	// Second uninstall: idempotent success (a repeated post-uninstall hook).
	code, out, _ = runC(t, "uninstall", "--to", dir)
	if code != 0 || !strings.Contains(out, "removed 0") {
		t.Fatalf("repeat exit=%d out=%q", code, out)
	}
}

func TestManInstall_WritesEveryPage(t *testing.T) {
	dir := t.TempDir()
	code, out, errb := runM(t, "install", "--to", dir)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, errb)
	}
	// The overview, ask's page, and one page per documented command.
	want := 2 + len(climeta.DocumentedCommands())
	entries, _ := os.ReadDir(dir)
	if len(entries) != want {
		t.Fatalf("installed %d pages, want %d", len(entries), want)
	}
	for _, f := range []string{"ai-playbook.1", "ask.1", "ai-playbook-run.1"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if !strings.Contains(out, "man file(s)") {
		t.Errorf("summary = %q", out)
	}
	// Uninstall removes them all.
	if code, _, _ := runM(t, "uninstall", "--to", dir); code != 0 {
		t.Fatal("uninstall failed")
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("pages left behind: %v", entries)
	}
}

func TestDefaultDirs_XDGDataHome(t *testing.T) {
	x := t.TempDir()
	t.Setenv("XDG_DATA_HOME", x)
	if code, _, _ := runC(t, "install"); code != 0 {
		t.Fatal("default-dir completion install failed")
	}
	if _, err := os.Stat(filepath.Join(x, "zsh", "site-functions", "_ai-playbook")); err != nil {
		t.Error("completion default must be $XDG_DATA_HOME/zsh/site-functions")
	}
	if code, _, _ := runM(t, "install"); code != 0 {
		t.Fatal("default-dir man install failed")
	}
	if _, err := os.Stat(filepath.Join(x, "man", "man1", "ai-playbook.1")); err != nil {
		t.Error("man default must be $XDG_DATA_HOME/man/man1")
	}
}

func TestUsageErrors(t *testing.T) {
	if code, _, _ := runC(t); code != 2 {
		t.Error("completion with no subcommand must be usage error")
	}
	if code, _, _ := runC(t, "definitely-unknown"); code != 2 {
		t.Error("completion unknown subcommand must be usage error")
	}
	if code, _, _ := runC(t, "show", "extra"); code != 2 {
		t.Error("completion show with args must be usage error")
	}
	if code, _, _ := runM(t); code != 2 {
		t.Error("man with no subcommand must be usage error")
	}
	if code, _, _ := runM(t, "show"); code != 2 {
		t.Error("man show is not a subcommand (usage error)")
	}
}
