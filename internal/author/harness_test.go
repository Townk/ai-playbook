package author

import (
	"reflect"
	"testing"
)

// TestClaudeHarness_ArgvGolden pins the EXACT ordered argv the claude harness
// builds for a fixed Invocation. It is a characterization guard for the C1c
// interface extraction: moving the claude arm behind the Harness interface must
// keep the owned argv byte-compatible with the events path, so this asserts the
// full slice (not a subset) — a drift in flag order/content fails here.
func TestClaudeHarness_ArgvGolden(t *testing.T) {
	got := claudeHarness{}.Argv("SYS", "USER", Invocation{
		Model:         "opus",
		MCPConfigPath: "/tmp/mcp.json",
		Bare:          false,
		Thinking:      "medium",
	})
	want := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--strict-mcp-config",
		"--model", "opus",
		"--mcp-config", "/tmp/mcp.json",
		"--append-system-prompt", "SYS",
		"USER",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("claude argv drifted\n got: %#v\nwant: %#v", got, want)
	}
}

// TestClaudeHarness_ArgvGolden_Bare pins the stripped CLASSIFY argv: --system-prompt
// (replace, not append), --exclude-dynamic-system-prompt-sections, no --mcp-config.
func TestClaudeHarness_ArgvGolden_Bare(t *testing.T) {
	got := claudeHarness{}.Argv("SYS", "USER", Invocation{Model: "haiku", Bare: true})
	want := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--strict-mcp-config",
		"--model", "haiku",
		"--exclude-dynamic-system-prompt-sections",
		"--system-prompt", "SYS",
		"USER",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bare claude argv drifted\n got: %#v\nwant: %#v", got, want)
	}
}

// TestClaudeHarness_AdapterAndEnv pins the adapter name and the MAX_THINKING_TOKENS
// mapping (always set explicitly; 0 disables — omitting would leave claude's default
// thinking ON, the old bug).
func TestClaudeHarness_AdapterAndEnv(t *testing.T) {
	h := claudeHarness{}
	if h.AdapterName() != "claude" {
		t.Errorf("adapter = %q, want claude", h.AdapterName())
	}
	cases := []struct {
		thinking string
		want     string
	}{
		{"medium", "MAX_THINKING_TOKENS=8000"},
		{"high", "MAX_THINKING_TOKENS=16000"},
		{"off", "MAX_THINKING_TOKENS=0"},
		{"", "MAX_THINKING_TOKENS=8000"},
	}
	for _, c := range cases {
		got := h.Env(Invocation{Thinking: c.thinking})
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("Env(thinking=%q) = %v, want [%q]", c.thinking, got, c.want)
		}
	}
}
