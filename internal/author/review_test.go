package author

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/config"
)

// runReviewStream drives ReviewStream against a fake harness emitting
// resultText, swapping the reviewProcess package var (the same fake-harness
// pattern runClassify/runMeta use, wired through the package var instead of
// an options field) and capturing the owned argv + *exec.Cmd (for the
// thinking-env assertion — RunHarnessEvents sets cmd.Env AFTER the Command
// callback returns, so gotCmd's Env is only authoritative once ReviewStream
// itself has returned).
func runReviewStream(t *testing.T, systemPrompt, userMessage, resultText string) (<-chan agentstream.Event, func() error, []string, *exec.Cmd, error) {
	t.Helper()
	bin := fakeMetadataHarness(t, resultText)

	var gotArgs []string
	var gotCmd *exec.Cmd
	old := reviewProcess
	reviewProcess = func(_ context.Context, b string, args []string) *exec.Cmd {
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

// ReviewStream's event stream, once drained, carries the harness's review text.
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

// ReviewStream runs the BARE quick-model argv (mirroring ClassifyRequest):
// --system-prompt (replace, not append), --strict-mcp-config +
// --exclude-dynamic-system-prompt-sections, no --mcp-config.
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

// ReviewStream leaves NoThinking false (unlike classify/metadata's bare
// calls), so MAX_THINKING_TOKENS is a positive budget, not "off" (0) — the
// model-activity feed has reasoning content to display while validate
// streams progress.
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
// with an unusable channel — so validate's caller can detect it.
func TestReviewStream_PropagatesNoBackendError(t *testing.T) {
	missingBin := filepath.Join(t.TempDir(), "no-such-claude-binary")

	old := reviewProcess
	reviewProcess = func(_ context.Context, b string, args []string) *exec.Cmd {
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
