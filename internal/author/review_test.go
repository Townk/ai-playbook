package author

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/config"
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

	got, err := ReviewOnce(config.Default(), systemPrompt, userMessage)
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

	got, err := ReviewOnce(config.Default(), "you are a reviewer", "the playbook body")
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

// runReviewStream drives ReviewStream against a fake harness emitting
// resultText, swapping the reviewProcess package var (the same test seam
// runReview uses) and capturing the owned argv + *exec.Cmd (for the thinking-env
// assertion — RunHarnessEvents sets cmd.Env AFTER the Command callback returns,
// so gotCmd's Env is only authoritative once ReviewStream itself has returned).
func runReviewStream(t *testing.T, systemPrompt, userMessage, resultText string) (<-chan agentstream.Event, func() error, []string, *exec.Cmd, error) {
	t.Helper()
	bin := fakeMetadataHarness(t, resultText)

	var gotArgs []string
	var gotCmd *exec.Cmd
	old := reviewProcess
	reviewProcess = func(b string, args []string) *exec.Cmd {
		gotArgs = args
		gotCmd = exec.Command(bin, args...)
		return gotCmd
	}
	t.Cleanup(func() { reviewProcess = old })

	events, closeFn, err := ReviewStream(config.Default(), systemPrompt, userMessage)
	return events, closeFn, gotArgs, gotCmd, err
}

// drainReviewText accumulates the review text carried by events — the Final
// event's text, falling back to accumulated TextDelta if no Final is emitted —
// mirroring runMetadataOnce's own drain of the same RunHarnessEvents stream.
func drainReviewText(events <-chan agentstream.Event) string {
	var final, deltas strings.Builder
	haveFinal := false
	for e := range events {
		switch e.Kind {
		case agentstream.Final:
			final.WriteString(e.Text)
			haveFinal = true
		case agentstream.TextDelta:
			deltas.WriteString(e.Text)
		}
	}
	if haveFinal {
		return final.String()
	}
	return deltas.String()
}

// ReviewStream's event stream, once drained, carries the harness's review text —
// the same text ReviewOnce returns directly, just delivered incrementally.
func TestReviewStream_StreamsHarnessText(t *testing.T) {
	events, closeFn, _, _, err := runReviewStream(t, "you are a reviewer", "the playbook body", "looks good")
	if err != nil {
		t.Fatalf("ReviewStream: %v", err)
	}
	got := drainReviewText(events)
	if werr := closeFn(); werr != nil {
		t.Fatalf("closeFn: %v", werr)
	}
	if got != "looks good" {
		t.Errorf("ReviewStream drained text = %q, want %q", got, "looks good")
	}
}

// ReviewStream runs the same BARE quick-model argv as ReviewOnce (mirrored
// AuthorOptions except NoThinking): --system-prompt (replace, not append),
// --strict-mcp-config + --exclude-dynamic-system-prompt-sections, no --mcp-config.
func TestReviewStream_BareArgvNoMCP(t *testing.T) {
	events, closeFn, args, _, err := runReviewStream(t, "you are a reviewer", "the playbook body", "ok")
	if err != nil {
		t.Fatalf("ReviewStream: %v", err)
	}
	for range events {
	}
	if werr := closeFn(); werr != nil {
		t.Fatalf("closeFn: %v", werr)
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
		t.Errorf("ReviewStream must use --system-prompt (replace), not --append-system-prompt: %v", args)
	}
	if !has("--strict-mcp-config") || !has("--exclude-dynamic-system-prompt-sections") {
		t.Errorf("ReviewStream must add --strict-mcp-config + --exclude-dynamic-system-prompt-sections: %v", args)
	}
	if has("--mcp-config") {
		t.Errorf("ReviewStream must NOT attach --mcp-config: %v", args)
	}
}

// ReviewStream's one intentional difference from ReviewOnce: NoThinking is left
// false, so MAX_THINKING_TOKENS is a positive budget (not the "off" 0 ReviewOnce
// forces) — the model-activity feed has reasoning content to display while
// validate streams progress.
func TestReviewStream_ThinkingEnabled(t *testing.T) {
	events, closeFn, _, cmd, err := runReviewStream(t, "you are a reviewer", "the playbook body", "ok")
	if err != nil {
		t.Fatalf("ReviewStream: %v", err)
	}
	for range events {
	}
	if werr := closeFn(); werr != nil {
		t.Fatalf("closeFn: %v", werr)
	}
	got := "<unset>"
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "MAX_THINKING_TOKENS=") {
			got = strings.TrimPrefix(kv, "MAX_THINKING_TOKENS=")
		}
	}
	if got == "0" || got == "<unset>" {
		t.Errorf("ReviewStream must leave thinking enabled (NoThinking:false): MAX_THINKING_TOKENS=%q", got)
	}
}

// A no-backend/failed start (e.g. the resolved binary doesn't exist) must
// surface as the 3rd return value UNCHANGED — not swallowed into a nil error
// with an unusable channel — so Task 2's validate caller can detect it.
func TestReviewStream_PropagatesNoBackendError(t *testing.T) {
	missingBin := filepath.Join(t.TempDir(), "no-such-claude-binary")

	old := reviewProcess
	reviewProcess = func(b string, args []string) *exec.Cmd {
		return exec.Command(missingBin, args...)
	}
	t.Cleanup(func() { reviewProcess = old })

	events, closeFn, err := ReviewStream(config.Default(), "you are a reviewer", "the playbook body")
	if err == nil {
		t.Fatal("expected a start error from a missing harness binary")
	}
	if events != nil {
		t.Errorf("events should be nil on a start error, got %v", events)
	}
	if closeFn != nil {
		t.Errorf("closeFn should be nil on a start error")
	}
}
