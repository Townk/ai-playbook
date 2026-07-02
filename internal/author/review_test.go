package author

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runReview drives ReviewOnce against a fake harness emitting resultText,
// swapping the reviewProcess package var (ReviewOnce's exported signature takes
// no AuthorOptions, so there's no per-call Command to inject — this is the same
// fake-harness pattern runClassify/runMeta use, just wired through the package
// var instead of an options field) and capturing the owned argv.
func runReview(t *testing.T, systemPrompt, userMessage, resultText string) (string, []string, error) {
	t.Helper()
	bin := fakeMetadataHarness(t, resultText)

	var gotArgs []string
	old := reviewProcess
	reviewProcess = func(b string, args []string) *exec.Cmd {
		gotArgs = args
		return exec.Command(bin, args...)
	}
	t.Cleanup(func() { reviewProcess = old })

	got, err := ReviewOnce(systemPrompt, userMessage)
	return got, gotArgs, err
}

// ReviewOnce returns the harness's result text unchanged — no JSON parse.
func TestReviewOnce_ReturnsHarnessText(t *testing.T) {
	got, _, err := runReview(t, "you are a reviewer", "the playbook body", "looks good")
	if err != nil {
		t.Fatalf("ReviewOnce: %v", err)
	}
	if got != "looks good" {
		t.Errorf("ReviewOnce = %q, want %q", got, "looks good")
	}
}

// A multi-line, non-JSON critique still comes back verbatim — ReviewOnce must
// never try to extract/parse a JSON object out of it (that's classify/metadata's
// job, not review's).
func TestReviewOnce_ReturnsFreeTextVerbatim(t *testing.T) {
	const critique = "Looks mostly fine.\n\n- step 2 is missing a rollback\n- {not json}\n"
	got, _, err := runReview(t, "you are a reviewer", "the playbook body", critique)
	if err != nil {
		t.Fatalf("ReviewOnce: %v", err)
	}
	if got != critique {
		t.Errorf("ReviewOnce = %q, want the harness text verbatim %q", got, critique)
	}
}

// ReviewOnce runs the BARE quick-model argv (mirroring ClassifyRequest): the
// replacing --system-prompt (not --append-system-prompt), --strict-mcp-config +
// --exclude-dynamic-system-prompt-sections, and NO --mcp-config (no tools
// backend).
func TestReviewOnce_BareArgvNoMCP(t *testing.T) {
	_, args, err := runReview(t, "you are a reviewer", "the playbook body", "ok")
	if err != nil {
		t.Fatalf("ReviewOnce: %v", err)
	}
	has := func(tok string) bool {
		for _, a := range args {
			if a == tok {
				return true
			}
		}
		return false
	}
	if !has("--system-prompt") || has("--append-system-prompt") {
		t.Errorf("ReviewOnce must use --system-prompt (replace), not --append-system-prompt: %v", args)
	}
	if !has("--strict-mcp-config") || !has("--exclude-dynamic-system-prompt-sections") {
		t.Errorf("ReviewOnce must add --strict-mcp-config + --exclude-dynamic-system-prompt-sections: %v", args)
	}
	if has("--mcp-config") {
		t.Errorf("ReviewOnce must NOT attach --mcp-config: %v", args)
	}
}

// A failing harness returns the error UNCHANGED to the caller — no retry loop
// (exactly one process invocation) — so Task 4's validate pass can detect a
// no-backend/harness-unsupported condition from the error itself.
func TestReviewOnce_PropagatesHarnessErrorWithNoRetry(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf x >> " + counter + "\necho 'boom: harness unavailable' >&2\nexit 3\n"
	bin := filepath.Join(dir, "fake-claude-fail")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	old := reviewProcess
	reviewProcess = func(b string, args []string) *exec.Cmd {
		return exec.Command(bin, args...)
	}
	t.Cleanup(func() { reviewProcess = old })

	got, err := ReviewOnce("you are a reviewer", "the playbook body")
	if err == nil {
		t.Fatal("expected an error from a failing harness")
	}
	if got != "" {
		t.Errorf("got = %q, want empty on error", got)
	}
	if !strings.Contains(err.Error(), "boom: harness unavailable") {
		t.Errorf("error should carry the captured stderr, got: %v", err)
	}
	calls, readErr := os.ReadFile(counter)
	if readErr != nil {
		t.Fatalf("read call counter: %v", readErr)
	}
	if len(calls) != 1 {
		t.Errorf("harness invoked %d times, want exactly 1 (no retry loop)", len(calls))
	}
}
