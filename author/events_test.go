package author

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ai-playbook/agentstream"
	"ai-playbook/config"
)

func TestClaudeArgs_OwnedInvocation(t *testing.T) {
	args := ClaudeArgs("opus", "/tmp/mcp.json", "SYS", "USER")
	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"-p",
		"--output-format\x00stream-json",
		"--verbose",
		"--include-partial-messages",
		"--model\x00opus",
		"--mcp-config\x00/tmp/mcp.json",
		"--append-system-prompt\x00SYS",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q\n got: %v", want, args)
		}
	}
	// userMessage is the trailing positional arg.
	if args[len(args)-1] != "USER" {
		t.Errorf("last arg = %q, want USER", args[len(args)-1])
	}
}

func TestClaudeArgs_OmitsEmptyModelAndMCP(t *testing.T) {
	args := ClaudeArgs("", "", "SYS", "USER")
	joined := strings.Join(args, "\x00")
	if strings.Contains(joined, "--model") {
		t.Errorf("empty model should be omitted: %v", args)
	}
	if strings.Contains(joined, "--mcp-config") {
		t.Errorf("empty mcp-config should be omitted: %v", args)
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

// TestRunHarnessEvents_ThinkingEnvWired: a non-off thinking preference sets
// MAX_THINKING_TOKENS on the harness process env; "off" leaves it unset.
func TestRunHarnessEvents_ThinkingEnvWired(t *testing.T) {
	bin := writeFakeHarness(t)

	check := func(thinking string, want string) {
		cfg := config.Default()
		cfg.Agent.Harness = "claude"
		cfg.Agent.Thinking = thinking

		var cmd *exec.Cmd
		events, wait, err := RunHarnessEvents("SYS", "USER", AuthorOptions{
			Cfg: cfg,
			Command: func(b string, args []string) *exec.Cmd {
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

		var got string
		for _, kv := range cmd.Env {
			if strings.HasPrefix(kv, "MAX_THINKING_TOKENS=") {
				got = strings.TrimPrefix(kv, "MAX_THINKING_TOKENS=")
			}
		}
		if got != want {
			t.Errorf("thinking %q: MAX_THINKING_TOKENS=%q, want %q", thinking, got, want)
		}
	}
	check("medium", "8000")
	check("high", "16000")
	check("off", "") // unset → no MAX_THINKING_TOKENS in env
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
		Command: func(b string, args []string) *exec.Cmd {
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
