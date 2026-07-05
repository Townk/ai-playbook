package author

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/config"
)

// fakeExitHarness writes a fake claude that exits with code, emitting no output —
// used to exercise the compaction failure-tolerance path (a harness error leaves
// the file untouched).
func fakeExitHarness(t *testing.T, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-harness shell script requires a POSIX shell")
	}
	script := "#!/bin/sh\nexit " + itoa(code) + "\n"
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-claude-exit")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// setCompactProcess points the compaction call at bin (a fake harness) and restores
// the seam after the test.
func setCompactProcess(t *testing.T, bin string, gotArgs *[]string) {
	t.Helper()
	prev := compactProcess
	compactProcess = func(_ context.Context, _ string, args []string) *exec.Cmd {
		if gotArgs != nil {
			*gotArgs = args
		}
		return exec.Command(bin, args...)
	}
	t.Cleanup(func() { compactProcess = prev })
}

func claudeCfg() *config.Config {
	cfg := config.Default()
	cfg.Agent.Harness = "claude"
	return cfg
}

const compactInput = "## System\n- fact one\n- fact two\n\n## User\n- pref a\n"

// CompactKB is a quick structured call: it carries NO --mcp-config and its system
// prompt is CompactPrompt(content).
func TestCompactKB_QuickCallNoMCP(t *testing.T) {
	const result = "## System\n- facts merged\n\n## User\n- pref a\n"
	bin := fakeMetadataHarness(t, result)
	var args []string
	setCompactProcess(t, bin, &args)

	got, err := CompactKB(claudeCfg(), compactInput)
	if err != nil {
		t.Fatalf("CompactKB: %v", err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(result) {
		t.Errorf("CompactKB result = %q, want %q", got, result)
	}
	if strings.Contains(strings.Join(args, "\x00"), "--mcp-config") {
		t.Errorf("compaction call must NOT attach --mcp-config: %v", args)
	}
	if got := appendSystemPromptArg(args); got != "" {
		t.Errorf("bare compaction call should REPLACE the system prompt, not append: %q", got)
	}
}

// Trigger gating: a file at or under budget triggers ZERO harness invocations and
// leaves the file untouched.
func TestCompactOversized_UnderBudgetNoCall(t *testing.T) {
	root := t.TempDir()
	gpath := writeGlobalKB(t, root, compactInput)
	calls := 0
	prev := compactProcess
	compactProcess = func(_ context.Context, _ string, _ []string) *exec.Cmd {
		calls++
		return exec.Command("true")
	}
	t.Cleanup(func() { compactProcess = prev })

	CompactOversized(claudeCfg(), root, "", len(compactInput)+100) // budget above file size

	if calls != 0 {
		t.Errorf("under-budget file must trigger 0 compaction calls, got %d", calls)
	}
	if b, _ := os.ReadFile(gpath); string(b) != compactInput {
		t.Errorf("under-budget file must be untouched")
	}
	if _, err := os.Stat(gpath + ".bak"); err == nil {
		t.Errorf("under-budget file must not write a .bak")
	}
}

// Over budget with a valid smaller result: the file is replaced and its prior
// content is written to .bak first.
func TestCompactOversized_CompactsOverBudget(t *testing.T) {
	const result = "## System\n- facts merged\n\n## User\n- pref a\n"
	root := t.TempDir()
	gpath := writeGlobalKB(t, root, compactInput)
	bin := fakeMetadataHarness(t, result)
	setCompactProcess(t, bin, nil)

	CompactOversized(claudeCfg(), root, "", 10) // budget below file size ⇒ compact

	if b, _ := os.ReadFile(gpath); strings.TrimSpace(string(b)) != strings.TrimSpace(result) {
		t.Errorf("over-budget file = %q, want compacted %q", b, result)
	}
	if b, _ := os.ReadFile(gpath + ".bak"); string(b) != compactInput {
		t.Errorf(".bak = %q, want the ORIGINAL content %q", b, compactInput)
	}
}

// compactMetaInput is a project-style input carrying the meta line (kb list/search
// resolve project names through it) — the fourth rejection guard's subject.
const compactMetaInput = "<!-- meta: project-root: /home/me/proj -->\n\n" +
	"## Environment\n- fact one with some padding\n- fact two with some padding\n"

// The four rejection guards: an empty result, a result not smaller than the input
// (including the EXACTLY-EQUAL boundary — the >= comparator: an identical rewrite is
// pointless to persist), a result dropping a section the input had, and a result
// dropping the input's meta line — each leaves the file untouched AND writes no .bak.
func TestCompactFile_RejectionGuards(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		result string
	}{
		{"empty", compactInput, "   \n  "},
		{"larger", compactInput, compactInput + "## Extra\n- padding padding padding padding\n"},
		{"equal", compactInput, compactInput},                                 // == input: pins the >= comparator boundary
		{"missing_section", compactInput, "## System\n- only system left\n"},  // dropped ## User
		{"missing_meta", compactMetaInput, "## Environment\n- merged fact\n"}, // dropped the meta line
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			gpath := writeGlobalKB(t, root, tc.input)
			bin := fakeMetadataHarness(t, tc.result)
			setCompactProcess(t, bin, nil)

			note := captureStderr(t, func() { CompactOversized(claudeCfg(), root, "", 10) })

			if b, _ := os.ReadFile(gpath); string(b) != tc.input {
				t.Errorf("rejected compaction must leave the file untouched, got %q", b)
			}
			if _, err := os.Stat(gpath + ".bak"); err == nil {
				t.Errorf("rejected compaction must NOT write a .bak")
			}
			if !strings.Contains(note, "compaction rejected") || !strings.Contains(note, "left unchanged") {
				t.Errorf("rejection must write a stderr note, got %q", note)
			}
		})
	}
}

// Failure tolerance: a harness error leaves the file untouched, writes no .bak,
// notes the failure on stderr, and does not panic (the wrap-up is unaffected).
func TestCompactFile_FailureTolerance(t *testing.T) {
	root := t.TempDir()
	gpath := writeGlobalKB(t, root, compactInput)
	bin := fakeExitHarness(t, 3)
	setCompactProcess(t, bin, nil)

	note := captureStderr(t, func() { CompactOversized(claudeCfg(), root, "", 10) })

	if b, _ := os.ReadFile(gpath); string(b) != compactInput {
		t.Errorf("harness failure must leave the file untouched, got %q", b)
	}
	if _, err := os.Stat(gpath + ".bak"); err == nil {
		t.Errorf("harness failure must NOT write a .bak")
	}
	if !strings.Contains(note, "compaction failed") || !strings.Contains(note, "left unchanged") {
		t.Errorf("harness failure must write a stderr note, got %q", note)
	}
}

// Cross-session race guard: a write landing between the initial read and the
// replace (a concurrent session's `remember`) ABORTS the compaction — the
// interleaved content survives, no .bak is written, and a stderr note explains the
// skip. The interleave is simulated via the compactPreReplace test seam (runs after
// CompactKB returns, before the pre-replace re-read — the least invasive hook).
func TestCompactFile_AbortsWhenFileChangesDuringCall(t *testing.T) {
	const result = "## System\n- merged\n\n## User\n- pref a\n"
	const interleaved = compactInput + "- fact three landed concurrently\n"
	root := t.TempDir()
	gpath := writeGlobalKB(t, root, compactInput)
	bin := fakeMetadataHarness(t, result)
	setCompactProcess(t, bin, nil)

	prev := compactPreReplace
	compactPreReplace = func(path string) {
		if err := os.WriteFile(path, []byte(interleaved), 0o644); err != nil {
			t.Errorf("interleaved write: %v", err)
		}
	}
	t.Cleanup(func() { compactPreReplace = prev })

	note := captureStderr(t, func() { CompactOversized(claudeCfg(), root, "", 10) })

	if b, _ := os.ReadFile(gpath); string(b) != interleaved {
		t.Errorf("race-aborted compaction must keep the interleaved content, got %q", b)
	}
	if _, err := os.Stat(gpath + ".bak"); err == nil {
		t.Errorf("race-aborted compaction must NOT write a .bak")
	}
	if !strings.Contains(note, "changed during compaction") {
		t.Errorf("race abort must write a stderr note, got %q", note)
	}
}

// Re-read failure: a file that becomes UNREADABLE inside the compaction window
// (simulated by deleting it via the compactPreReplace seam) aborts with the
// dedicated "re-read failed" note — NOT the "changed during compaction" note (an
// unreadable file must not claim it changed) — and writes no .bak.
func TestCompactFile_ReReadError_DistinctNote(t *testing.T) {
	const result = "## System\n- merged\n\n## User\n- pref a\n"
	root := t.TempDir()
	gpath := writeGlobalKB(t, root, compactInput)
	bin := fakeMetadataHarness(t, result)
	setCompactProcess(t, bin, nil)

	prev := compactPreReplace
	compactPreReplace = func(path string) {
		if err := os.Remove(path); err != nil {
			t.Errorf("remove inside the compaction window: %v", err)
		}
	}
	t.Cleanup(func() { compactPreReplace = prev })

	note := captureStderr(t, func() { CompactOversized(claudeCfg(), root, "", 10) })

	if _, err := os.Stat(gpath); err == nil {
		t.Errorf("re-read-failure abort must not resurrect the file via a replace")
	}
	if _, err := os.Stat(gpath + ".bak"); err == nil {
		t.Errorf("re-read-failure abort must NOT write a .bak")
	}
	if !strings.Contains(note, "re-read failed") {
		t.Errorf("re-read failure must write the dedicated stderr note, got %q", note)
	}
	if strings.Contains(note, "changed during compaction") {
		t.Errorf("an unreadable file must not claim it changed, got %q", note)
	}
}

// captureStderr runs f and returns whatever it wrote to os.Stderr (the same
// pipe-swap pattern the kb suite uses for Capped's truncation note).
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
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// writeGlobalKB writes content to the GLOBAL KB file under root and returns its path.
func writeGlobalKB(t *testing.T, root, content string) string {
	t.Helper()
	p := filepath.Join(root, "knowledge.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
