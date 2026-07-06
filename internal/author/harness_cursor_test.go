package author

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/config"
)

// cursorFolded is the expected fold of the SYS/USER sentinels — cursor-agent
// has no system-prompt flags, so the system prompt travels at the head of the
// single positional user message (the multi-harness spec's documented
// fallback).
const cursorFolded = "<system_instructions>\nSYS\n</system_instructions>\n\nUSER"

// TestCursorHarness_ArgvCharacterization pins the EXACT ordered cursor-agent
// argv for every invocation shape RunHarnessEvents produces. The flag set is
// LIVE-VERIFIED (see the Argv rationale in harness_cursor.go): -p
// --output-format stream-json --stream-partial-output --mode ask --trust on
// every path, the prompt positional and LAST. Bare and append are deliberately
// the SAME shape — cursor-agent has neither replace/append system-prompt flags
// nor any context-suppression flags, so there is nothing to strip and nothing
// to replace; the RequireHarness-gated live tests re-verify the composition
// wherever the CLI exists.
func TestCursorHarness_ArgvCharacterization(t *testing.T) {
	h := cursorHarness{}
	common := []string{
		"-p",
		"--output-format", "stream-json",
		"--stream-partial-output",
		"--mode", "ask",
		"--trust",
	}
	app := func(rest ...string) []string { return append(append([]string{}, common...), rest...) }

	cases := []struct {
		name string
		inv  Invocation
		want []string
	}{
		{
			name: "normal, no model",
			inv:  Invocation{},
			want: app(cursorFolded),
		},
		{
			name: "normal, model",
			inv:  Invocation{Model: "gpt-5", Thinking: "high"},
			want: app("--model", "gpt-5", cursorFolded),
		},
		{
			name: "bare, model (the classify shape — identical to normal: no levers exist)",
			inv:  Invocation{Model: "gpt-5", Bare: true, Thinking: "off"},
			want: app("--model", "gpt-5", cursorFolded),
		},
		{
			name: "bare, no model",
			inv:  Invocation{Bare: true, Thinking: "off"},
			want: app(cursorFolded),
		},
		{
			// Cursor is BASIC so the launcher never produces a ToolArgv (gated on
			// Capabilities().Tools); the splice position is pinned as the
			// promotion seam.
			name: "tool argv splice position (promotion seam)",
			inv:  Invocation{ToolArgv: []string{"--future-attachment", "/tmp/x"}},
			want: app("--future-attachment", "/tmp/x", cursorFolded),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := h.Argv("SYS", "USER", tc.inv)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Argv:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestCursorFoldPrompt pins the fold composition: the system prompt fenced in
// an explicit tag, then a blank line, then the user message — byte-exact, so
// prompt-assembly drift is a deliberate change, not an accident.
func TestCursorFoldPrompt(t *testing.T) {
	got := cursorFoldPrompt("line one\nline two", "do the thing")
	want := "<system_instructions>\nline one\nline two\n</system_instructions>\n\ndo the thing"
	if got != want {
		t.Errorf("cursorFoldPrompt = %q, want %q", got, want)
	}
}

// TestCursorHarness_Env pins that cursor needs NO extra process env on any
// invocation shape — model selection is a flag, no thinking control exists,
// and the user's own authentication (CURSOR_API_KEY / login) must pass
// through untouched.
func TestCursorHarness_Env(t *testing.T) {
	h := cursorHarness{}
	for _, inv := range []Invocation{{}, {Thinking: "off"}, {Thinking: "high"}, {Bare: true}} {
		if env := h.Env(inv); len(env) != 0 {
			t.Errorf("Env(%+v) = %v, want none", inv, env)
		}
	}
}

// TestHarnessContract_CursorRow pins cursor's own contract values: the labels,
// the BASIC tier (no per-invocation MCP attachment exists — file discovery is
// isolation-unsafe — and the schema-enforced tool loop is unproven; see
// Capabilities in harness_cursor.go), the defaults row (everything on
// cursor's own defaults; the binary is cursor-agent, NOT the registry name),
// and the loud ToolTransport failure.
func TestHarnessContract_CursorRow(t *testing.T) {
	h, ok := harnessFor("cursor")
	if !ok {
		t.Fatal("cursor harness not registered")
	}
	if got := h.DisplayName(); got != "Cursor" {
		t.Errorf("DisplayName = %q, want Cursor", got)
	}
	if got := h.AdapterName(); got != "cursor" {
		t.Errorf("AdapterName = %q, want cursor", got)
	}
	if h.Capabilities().Tools {
		t.Error("cursor must ship BASIC (Tools=false) until the MCP attachment + tool loop are proven")
	}
	if d := HarnessDefaults("cursor"); d != (Defaults{Model: "", TriageModel: "", Thinking: "", Bin: "cursor-agent"}) {
		t.Errorf("cursor defaults row = %+v, want {\"\", \"\", \"\", cursor-agent}", d)
	}

	// BASIC: asking for a transport is a caller bug (callers gate on
	// Capabilities().Tools) and must fail loudly, never write a broken config.
	files, argv, err := h.ToolTransport(Invocation{SelfExe: "/path/to/ai-playbook"}, "/tmp/tools.sock", t.TempDir())
	if err == nil {
		t.Fatal("ToolTransport on a BASIC harness must fail")
	}
	if len(files) != 0 || len(argv) != 0 {
		t.Errorf("failed ToolTransport must return nothing (files %v, argv %v)", files, argv)
	}
}

// TestCursorHarness_BinResolution pins the bin seam the cursor row introduced:
// the registry name ("cursor", the [agent] harness value) is NOT the binary —
// the CLI installs as cursor-agent (legacy symlink, present on every install
// vintage) — and an explicit [agent].bin still wins.
func TestCursorHarness_BinResolution(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Harness = "cursor"
	if got := HarnessBin(cfg); got != "cursor-agent" {
		t.Errorf("HarnessBin(cursor) = %q, want cursor-agent (the harness name is not the binary)", got)
	}
	cfg.Agent.Bin = "agent"
	if got := HarnessBin(cfg); got != "agent" {
		t.Errorf("HarnessBin(bin override) = %q, want the explicit bin", got)
	}

	cfg = config.Default()
	cfg.Agent.Harness = "cursor"
	if got := HarnessDisplayName(cfg); got != "Cursor" {
		t.Errorf("HarnessDisplayName(cursor) = %q, want Cursor", got)
	}
	if !strings.Contains(HarnessBin(cfg), "cursor") {
		t.Errorf("HarnessBin(cursor) = %q, want a cursor binary", HarnessBin(cfg))
	}
}
