package author

import (
	"os"
	"path/filepath"
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
// --output-format stream-json --stream-partial-output --trust on every path,
// the prompt positional and LAST. --mode ask rides ONLY the tool-less paths
// (it keeps cursor read-only); the FULL tool path DROPS it because cursor-agent
// refuses MCP tool calls in ask mode. Bare and append are deliberately the SAME
// shape — cursor-agent has neither replace/append system-prompt flags nor any
// context-suppression flags, so there is nothing to strip and nothing to
// replace; the RequireHarness-gated live tests re-verify wherever the CLI exists.
func TestCursorHarness_ArgvCharacterization(t *testing.T) {
	h := cursorHarness{}
	// Tool-less shape: -p … --stream-partial-output --mode ask --trust …
	ask := func(rest ...string) []string {
		return append([]string{
			"-p",
			"--output-format", "stream-json",
			"--stream-partial-output",
			"--mode", "ask",
			"--trust",
		}, rest...)
	}
	// FULL tool shape: --mode ask is DROPPED (cursor refuses tools in ask mode).
	tool := func(rest ...string) []string {
		return append([]string{
			"-p",
			"--output-format", "stream-json",
			"--stream-partial-output",
			"--trust",
		}, rest...)
	}

	cases := []struct {
		name string
		inv  Invocation
		want []string
	}{
		{
			name: "normal, no model",
			inv:  Invocation{},
			want: ask(cursorFolded),
		},
		{
			name: "normal, model",
			inv:  Invocation{Model: "gpt-5", Thinking: "high"},
			want: ask("--model", "gpt-5", cursorFolded),
		},
		{
			name: "bare, model (the classify shape — identical to normal: no levers exist)",
			inv:  Invocation{Model: "gpt-5", Bare: true, Thinking: "off"},
			want: ask("--model", "gpt-5", cursorFolded),
		},
		{
			name: "bare, no model",
			inv:  Invocation{Bare: true, Thinking: "off"},
			want: ask(cursorFolded),
		},
		{
			// FULL: tools wired ⇒ --mode ask dropped, the attach argv
			// (--approve-mcps in production) spliced before the prompt.
			name: "tool argv drops --mode ask (FULL path)",
			inv:  Invocation{ToolArgv: []string{"--approve-mcps"}},
			want: tool("--approve-mcps", cursorFolded),
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

// TestCursorHarness_Env pins cursor's process env contract. On the TOOL-LESS
// paths (no ToolDir) cursor needs NO extra env — model selection is a flag, no
// thinking control exists, and the user's own authentication (login/keychain)
// must pass through the untouched real HOME. On the TOOL path Env redirects
// HOME to the transport root so cursor-agent reads OUR isolated
// `<ToolDir>/.cursor/mcp.json` instead of the user's global one (Phase C).
func TestCursorHarness_Env(t *testing.T) {
	h := cursorHarness{}
	for _, inv := range []Invocation{{}, {Thinking: "off"}, {Thinking: "high"}, {Bare: true}} {
		if env := h.Env(inv); len(env) != 0 {
			t.Errorf("Env(%+v) = %v, want none", inv, env)
		}
	}
	// Tool path: HOME points at the transport root (the isolation mechanism).
	got := h.Env(Invocation{ToolDir: "/tmp/apb-xyz"})
	if want := []string{"HOME=/tmp/apb-xyz"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Env(ToolDir) = %v, want %v", got, want)
	}
}

// TestHarnessContract_CursorRow pins cursor's own contract values: the labels,
// the FULL tier (Phase C — isolated MCP config via a HOME redirect + a
// schema-enforced tool loop; see Capabilities in harness_cursor.go), the
// defaults row (everything on cursor's own defaults; the binary is cursor-agent,
// NOT the registry name), and the config-root redirect transport (writes
// `<dir>/.cursor/mcp.json` holding ONLY our server, attaches via --approve-mcps).
// The Bin is left EMPTY here so the transport skips its live isolation guard —
// this pins the construction contract, not a CLI probe.
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
	if !h.Capabilities().Tools {
		t.Error("cursor must ship FULL (Tools) after the Phase C isolation promotion")
	}
	if d := HarnessDefaults("cursor"); d != (Defaults{Model: "", TriageModel: "", Thinking: "", Bin: "cursor-agent"}) {
		t.Errorf("cursor defaults row = %+v, want {\"\", \"\", \"\", cursor-agent}", d)
	}

	// FULL transport: writes an isolated .cursor/mcp.json under dir and attaches
	// via --approve-mcps. No Bin ⇒ the live isolation guard is skipped.
	dir := t.TempDir()
	files, argv, err := h.ToolTransport(Invocation{SelfExe: "/path/to/ai-playbook"}, "/tmp/tools.sock", dir)
	if err != nil {
		t.Fatalf("ToolTransport: %v", err)
	}
	if want := []string{"--approve-mcps"}; !reflect.DeepEqual(argv, want) {
		t.Errorf("cursor transport argv = %v, want %v", argv, want)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "mcp.json" {
		t.Fatalf("cursor transport files = %v, want one .cursor/mcp.json", files)
	}
	if rel, rerr := filepath.Rel(dir, files[0]); rerr != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("cursor transport wrote outside its dir: %s (dir %s)", files[0], dir)
	}
	b, rerr := os.ReadFile(files[0])
	if rerr != nil {
		t.Fatalf("read transport artifact: %v", rerr)
	}
	for _, want := range []string{`"mcpServers"`, `"ai-playbook"`, `"/path/to/ai-playbook"`, `"mcp"`, `"/tmp/tools.sock"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("mcp.json missing %s:\n%s", want, b)
		}
	}

	// SelfExe is load-bearing: without it cursor cannot point the MCP config
	// back at us, so the transport must fail loudly (never a broken config).
	if _, _, err := h.ToolTransport(Invocation{}, "/tmp/tools.sock", t.TempDir()); err == nil {
		t.Error("ToolTransport with no SelfExe must fail (never a silent broken config)")
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
