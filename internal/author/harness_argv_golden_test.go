package author

import (
	"reflect"
	"testing"
)

// TestClaudeHarness_ArgvCharacterization pins the EXACT ordered claude argv for
// every invocation shape RunHarnessEvents produces (normal/bare × tools/no-tools ×
// model set/empty). It is the refactor-safety golden for the ADR-0012 seam move
// (ClaudeArgs → the claude harness file, --mcp-config → ToolTransport argv): the
// expected slices below were captured BEFORE the move and must stay byte-identical
// after it — any drift in flag order/content is a regression, not a test to update.
func TestClaudeHarness_ArgvCharacterization(t *testing.T) {
	h := claudeHarness{}
	common := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--strict-mcp-config",
	}
	app := func(rest ...string) []string { return append(append([]string{}, common...), rest...) }

	cases := []struct {
		name string
		inv  Invocation
		want []string
	}{
		{
			name: "normal, no model, no tools",
			inv:  Invocation{},
			want: app("--append-system-prompt", "SYS", "USER"),
		},
		{
			name: "normal, model, no tools",
			inv:  Invocation{Model: "opus"},
			want: app("--model", "opus", "--append-system-prompt", "SYS", "USER"),
		},
		{
			name: "normal, model, tools",
			inv:  Invocation{Model: "opus", ToolArgv: []string{"--mcp-config", "/tmp/mcp.json"}},
			want: app("--model", "opus", "--mcp-config", "/tmp/mcp.json",
				"--append-system-prompt", "SYS", "USER"),
		},
		{
			name: "normal, no model, tools",
			inv:  Invocation{ToolArgv: []string{"--mcp-config", "/tmp/mcp.json"}},
			want: app("--mcp-config", "/tmp/mcp.json", "--append-system-prompt", "SYS", "USER"),
		},
		{
			name: "bare, model, no tools",
			inv:  Invocation{Model: "haiku", Bare: true},
			want: app("--model", "haiku",
				"--exclude-dynamic-system-prompt-sections", "--system-prompt", "SYS", "USER"),
		},
		{
			name: "bare, no model, no tools",
			inv:  Invocation{Bare: true},
			want: app("--exclude-dynamic-system-prompt-sections", "--system-prompt", "SYS", "USER"),
		},
		{
			name: "bare, no model, tools (splice position pinned before the bare flags)",
			inv:  Invocation{Bare: true, ToolArgv: []string{"--mcp-config", "/tmp/mcp.json"}},
			want: app("--mcp-config", "/tmp/mcp.json",
				"--exclude-dynamic-system-prompt-sections", "--system-prompt", "SYS", "USER"),
		},
	}
	for _, c := range cases {
		got := h.Argv("SYS", "USER", c.inv)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: claude argv drifted\n got: %#v\nwant: %#v", c.name, got, c.want)
		}
	}
}

// TestClaudeHarness_EnvCharacterization pins the MAX_THINKING_TOKENS env mapping
// across the thinking preferences (on/off/levels/bare integer/garbage). Same
// golden contract as the argv characterization: byte-identical across the move.
func TestClaudeHarness_EnvCharacterization(t *testing.T) {
	h := claudeHarness{}
	cases := []struct {
		thinking string
		want     string
	}{
		{"", "MAX_THINKING_TOKENS=8000"},
		{"medium", "MAX_THINKING_TOKENS=8000"},
		{"on", "MAX_THINKING_TOKENS=8000"},
		{"low", "MAX_THINKING_TOKENS=4000"},
		{"high", "MAX_THINKING_TOKENS=16000"},
		{"off", "MAX_THINKING_TOKENS=0"},
		{"none", "MAX_THINKING_TOKENS=0"},
		{"0", "MAX_THINKING_TOKENS=0"},
		{"12345", "MAX_THINKING_TOKENS=12345"},
		{"garbage", "MAX_THINKING_TOKENS=8000"},
	}
	for _, c := range cases {
		got := h.Env(Invocation{Thinking: c.thinking})
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("Env(thinking=%q) = %v, want [%q]", c.thinking, got, c.want)
		}
	}
}
