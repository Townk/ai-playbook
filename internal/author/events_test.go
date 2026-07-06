package author

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/config"
)

func TestClaudeArgs_OwnedInvocation(t *testing.T) {
	args := claudeArgs("opus", []string{"--mcp-config", "/tmp/mcp.json"}, "SYS", "USER", false)
	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"-p",
		"--output-format\x00stream-json",
		"--verbose",
		"--include-partial-messages",
		"--model\x00opus",
		"--mcp-config\x00/tmp/mcp.json",
		"--append-system-prompt\x00SYS",
		"--strict-mcp-config", // skip the user's global MCP servers on every path
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q\n got: %v", want, args)
		}
	}
	// The authoring path keeps the dynamic sections + appends (not replaces) the
	// system prompt — only the bare classify uses these.
	for _, bad := range []string{"--system-prompt", "--exclude-dynamic-system-prompt-sections"} {
		for _, a := range args {
			if a == bad {
				t.Errorf("authoring argv must not contain bare flag %q: %v", bad, args)
			}
		}
	}
	// userMessage is the trailing positional arg.
	if args[len(args)-1] != "USER" {
		t.Errorf("last arg = %q, want USER", args[len(args)-1])
	}
}

func TestClaudeArgs_OmitsEmptyModelAndMCP(t *testing.T) {
	args := claudeArgs("", nil, "SYS", "USER", false)
	joined := strings.Join(args, "\x00")
	if strings.Contains(joined, "--model") {
		t.Errorf("empty model should be omitted: %v", args)
	}
	if strings.Contains(joined, "--mcp-config") {
		t.Errorf("empty mcp-config should be omitted: %v", args)
	}
}

// TestClaudeArgs_Bare: the bare quick-model call REPLACES the system prompt
// (--system-prompt, NOT --append-system-prompt), adds --strict-mcp-config and
// --exclude-dynamic-system-prompt-sections, and (since classify passes no
// mcp-config) carries no --mcp-config.
func TestClaudeArgs_Bare(t *testing.T) {
	args := claudeArgs("haiku", nil, "SYS", "USER", true)
	has := func(tok string) bool {
		for _, a := range args {
			if a == tok {
				return true
			}
		}
		return false
	}
	if !has("--system-prompt") {
		t.Errorf("bare argv must use --system-prompt (replace): %v", args)
	}
	if has("--append-system-prompt") {
		t.Errorf("bare argv must NOT use --append-system-prompt: %v", args)
	}
	if !has("--strict-mcp-config") {
		t.Errorf("bare argv must include --strict-mcp-config: %v", args)
	}
	if !has("--exclude-dynamic-system-prompt-sections") {
		t.Errorf("bare argv must include --exclude-dynamic-system-prompt-sections: %v", args)
	}
	if has("--mcp-config") {
		t.Errorf("bare classify passes no mcp-config: %v", args)
	}
	if args[len(args)-1] != "USER" {
		t.Errorf("last arg = %q, want USER", args[len(args)-1])
	}
}

// TestClaudeThinkingTokens: the config thinking preference maps to a sane
// MAX_THINKING_TOKENS budget; empty defaults to "on" (>0) so reasoning streams.
func TestClaudeThinkingTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 8000},
		{"medium", 8000},
		{"on", 8000},
		{"low", 4000},
		{"high", 16000},
		{"off", 0},
		{"none", 0},
		{"0", 0},
		{"12345", 12345},
		{"garbage", 8000},
	}
	for _, c := range cases {
		if got := claudeThinkingTokens(c.in); got != c.want {
			t.Errorf("claudeThinkingTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestRunHarnessEvents_ThinkingEnvWired: MAX_THINKING_TOKENS is ALWAYS set
// explicitly — a budget when thinking is on, 0 to disable. "off" (and the
// NoThinking option) emit 0, NOT an unset var: omitting it would leave Claude
// Code's default thinking ON (the old bug). NoThinking overrides cfg.Thinking.
func TestRunHarnessEvents_ThinkingEnvWired(t *testing.T) {
	bin := writeFakeHarness(t)

	check := func(thinking string, noThinking bool, want string) {
		cfg := config.Default()
		cfg.Agent.Harness = "claude"
		cfg.Agent.Thinking = thinking

		var cmd *exec.Cmd
		events, wait, err := RunHarnessEvents("SYS", "USER", AuthorOptions{
			Cfg:        cfg,
			NoThinking: noThinking,
			Command: func(_ context.Context, b string, args []string) *exec.Cmd {
				cmd = exec.Command(bin, args...) // captured; Env set by RunHarnessEvents after this returns
				return cmd
			},
		})
		if err != nil {
			t.Fatalf("RunHarnessEvents(%q): %v", thinking, err)
		}
		for range events {
		}
		_ = wait()

		got := "<unset>"
		for _, kv := range cmd.Env {
			if strings.HasPrefix(kv, "MAX_THINKING_TOKENS=") {
				got = strings.TrimPrefix(kv, "MAX_THINKING_TOKENS=")
			}
		}
		if got != want {
			t.Errorf("thinking=%q noThinking=%v: MAX_THINKING_TOKENS=%q, want %q", thinking, noThinking, got, want)
		}
	}
	check("medium", false, "8000")
	check("high", false, "16000")
	check("off", false, "0")   // off → EXPLICIT 0 (omitting leaves Claude's default thinking ON)
	check("medium", true, "0") // NoThinking forces 0 regardless of cfg.Thinking
}

// TestAuthorEvents_UnsupportedHarness: pi/cursor → a clear error, no process.
func TestAuthorEvents_UnsupportedHarness(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Harness = "pi"
	_, _, err := AuthorEvents(sampleFailure(), AuthorOptions{Cfg: cfg})
	if err == nil {
		t.Fatal("expected error for unsupported harness")
	}
	if !strings.Contains(err.Error(), "pi") || !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("error = %q, want a clear not-yet-supported message", err)
	}
}

// fakeHarnessScript is a tiny POSIX shell script that emits canned claude
// stream-json on stdout, ignoring its arguments — standing in for a real claude.
const fakeHarnessScript = `#!/bin/sh
cat <<'NDJSON'
{"type":"system","subtype":"init"}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"step one"}}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"run","input":{"command":"make test"}}]}}
{"type":"result","result":"# Playbook\nrun make test\n"}
NDJSON
`

// writeFakeHarness writes the canned-stream script to a temp file and returns its
// path. Skips on Windows (POSIX sh not available).
func writeFakeHarness(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-harness shell script requires a POSIX shell")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-claude")
	if err := os.WriteFile(p, []byte(fakeHarnessScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAuthorEvents_FakeHarness drives AuthorEvents against a fake harness binary
// (a script emitting canned stream-json), asserting the normalized events arrive
// and the process is reaped. No real claude is used.
func TestAuthorEvents_FakeHarness(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir()) // deterministic empty KB
	bin := writeFakeHarness(t)

	cfg := config.Default()
	cfg.Agent.Harness = "claude"
	cfg.Agent.Bin = bin

	events, wait, err := AuthorEvents(sampleFailure(), AuthorOptions{Cfg: cfg})
	if err != nil {
		t.Fatalf("AuthorEvents: %v", err)
	}

	var got []agentstream.Event
	for e := range events {
		got = append(got, e)
	}
	if err := wait(); err != nil {
		t.Fatalf("process wait (reap) failed: %v", err)
	}

	want := []agentstream.Event{
		{Kind: agentstream.TextDelta, Text: "step one"},
		{Kind: agentstream.ToolActivity, Text: "❯ make test"},
		{Kind: agentstream.Final, Text: "# Playbook\nrun make test\n"},
	}
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d\n got: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = {%s %q}, want {%s %q}",
				i, got[i].Kind, got[i].Text, want[i].Kind, want[i].Text)
		}
	}
}

// TestAuthorEvents_CommandSeam exercises the injectable Command seam: the harness
// is launched via a caller-built *exec.Cmd, and the resolved bin + owned argv are
// captured for assertion.
func TestAuthorEvents_CommandSeam(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	bin := writeFakeHarness(t)

	cfg := config.Default()
	cfg.Agent.Model = "sonnet"

	var gotBin string
	var gotArgs []string
	events, wait, err := AuthorEvents(sampleFailure(), AuthorOptions{
		Cfg: cfg,
		Command: func(_ context.Context, b string, args []string) *exec.Cmd {
			gotBin = b
			gotArgs = args
			// Run the real fake script regardless of the resolved bin.
			return exec.Command(bin, args...)
		},
	})
	if err != nil {
		t.Fatalf("AuthorEvents: %v", err)
	}
	for range events {
	}
	if err := wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	// Default harness "claude" resolves bin to the harness name (no Bin override).
	if gotBin != "claude" {
		t.Errorf("resolved bin = %q, want claude (harness name)", gotBin)
	}
	joined := strings.Join(gotArgs, "\x00")
	if !strings.Contains(joined, "--output-format\x00stream-json") {
		t.Errorf("owned argv missing stream-json flags: %v", gotArgs)
	}
	if !strings.Contains(joined, "--model\x00sonnet") {
		t.Errorf("config model not threaded into argv: %v", gotArgs)
	}
}

// fakeStrictAdapter is a minimal agentstream.Adapter used ONLY to exercise A5b's
// error-join plumbing through the Adapter override seam: it treats ANY non-JSON
// line as a fatal parse error, standing in for "the stream-json contract was
// violated". (The shipped claudeAdapter now enforces the same strictness itself
// — A5b-strict — but this test keeps its own adapter so it pins the SEAM's
// plumbing independent of the shipped adapter's rules.)
type fakeStrictAdapter struct{}

func (fakeStrictAdapter) Parse(r io.Reader, emit func(agentstream.Event)) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			return fmt.Errorf("invalid stream-json line: %q", line)
		}
		emit(agentstream.Event{Kind: agentstream.TextDelta, Text: line})
	}
	return sc.Err()
}

// invalidStreamHarnessScript exits 0 while emitting one line that is NOT valid
// JSON — a stream-contract violation a real (non-tolerant) adapter would flag,
// paired with a "successful" exit code.
const invalidStreamHarnessScript = `#!/bin/sh
cat <<'NDJSON'
{"type":"system","subtype":"init"}
this is not valid stream-json at all
NDJSON
`

// TestRunHarnessEvents_SurfacesParseError is the A5b RED/GREEN case: a fake
// harness emits invalid stream-json and exits 0 (cmd.Wait() alone reports
// success). Before the fix, the goroutine's `_ = adapter.Parse(...)` discarded
// the parse error entirely, so wait() returned nil — a truncated/malformed
// stream on a clean exit was indistinguishable from success. After the fix,
// wait() joins the parse error with cmd.Wait()'s and returns non-nil.
func TestRunHarnessEvents_SurfacesParseError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-harness shell script requires a POSIX shell")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-claude-invalid")
	if err := os.WriteFile(bin, []byte(invalidStreamHarnessScript), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Agent.Harness = "claude"

	events, wait, err := RunHarnessEvents("SYS", "USER", AuthorOptions{
		Cfg:     cfg,
		Adapter: fakeStrictAdapter{},
		Command: func(ctx context.Context, b string, args []string) *exec.Cmd {
			return exec.CommandContext(ctx, bin, args...)
		},
	})
	if err != nil {
		t.Fatalf("RunHarnessEvents: %v", err)
	}
	for range events {
	}
	if werr := wait(); werr == nil {
		t.Fatal("wait() = nil, want a non-nil error surfacing the invalid stream-json (A5b)")
	} else if !strings.Contains(werr.Error(), "invalid stream-json") {
		t.Errorf("wait() error = %q, want it to mention the parse failure", werr)
	}
}
