package author

import (
	"encoding/json"
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

// cursorFixture reads a captured cursor-agent CLI output sample from
// testdata/cursor/ (real captures, cursor-agent 2026.07.01-777f564; the
// status_logged_in email is redacted to a placeholder). These back the security
// boundary of the isolation guard, which otherwise has no unit coverage.
func cursorFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "cursor", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// TestParseCursorMCPList pins the isolation-guard parser against REAL
// `cursor-agent mcp list` output. The load-bearing property is the SECURITY one:
// every configured server name must be extracted so a foreign (leaked) server
// can never hide from the guard — including on a multi-colon status line
// ("name: Error: Connection failed") and without mistaking the colon-less
// "No MCP servers configured" chrome for a server.
func TestParseCursorMCPList(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    []string
	}{
		{"isolated root — only our server", "mcp_list_isolated.txt", []string{"ai-playbook"}},
		{"foreign server leaked (multi-colon status)", "mcp_list_foreign.txt", []string{"ai-playbook", "atlassian"}},
		{"no servers configured (colon-less chrome)", "mcp_list_empty.txt", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCursorMCPList(cursorFixture(t, tc.fixture))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseCursorMCPList = %q, want %q", got, tc.want)
			}
		})
	}

	// Synthetic robustness: a name with a multi-colon status still yields the
	// name; a colon-less line and a leading-colon line are NOT mistaken for
	// servers (a foreign server always appears as "name: status", so it can
	// never hide on such a line).
	t.Run("multi-colon and colon-less lines", func(t *testing.T) {
		out := "zellij: Error: nested: colons\n" +
			"some banner with no colon\n" +
			":leadingcolon\n" +
			"  context7 : ready \n"
		got := parseCursorMCPList(out)
		want := []string{"zellij", "context7"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseCursorMCPList = %q, want %q", got, want)
		}
	})
}

// TestCursorForeignServer pins the guard's leak DECISION against real output:
// only our server (or none) is a clean isolation; ANY other server present is a
// leak the guard must reject. This is the security gate the launcher turns into
// a BASIC degrade.
func TestCursorForeignServer(t *testing.T) {
	if _, ok := cursorForeignServer(cursorFixture(t, "mcp_list_isolated.txt")); ok {
		t.Error("isolated root (only ai-playbook) must PASS — reported a foreign server")
	}
	if _, ok := cursorForeignServer(cursorFixture(t, "mcp_list_empty.txt")); ok {
		t.Error("no servers configured must PASS — reported a foreign server")
	}
	foreign, ok := cursorForeignServer(cursorFixture(t, "mcp_list_foreign.txt"))
	if !ok {
		t.Fatal("a leaked foreign server (atlassian) must FAIL the guard")
	}
	if foreign != "atlassian" {
		t.Errorf("foreign server = %q, want atlassian", foreign)
	}
}

// TestCursorStatusAuthenticated pins the auth guard against the fail-OPEN bug:
// `cursor-agent status` prints "✓ Logged in as <email>" when authenticated and
// "Not logged in" when not — and the naive substring "logged in" is present in
// BOTH, so a logged-OUT redirect would have passed. Positive auth is required
// and a "not logged in" (or empty/error) output must fail closed.
func TestCursorStatusAuthenticated(t *testing.T) {
	if !cursorStatusAuthenticated(cursorFixture(t, "status_logged_in.txt")) {
		t.Error("a logged-in status must authenticate")
	}
	if cursorStatusAuthenticated(cursorFixture(t, "status_logged_out.txt")) {
		t.Error("a NOT-logged-in status must fail (the fail-open regression)")
	}
	for _, out := range []string{"", "   ", "error: could not reach auth store"} {
		if cursorStatusAuthenticated(out) {
			t.Errorf("empty/error status %q must fail closed", out)
		}
	}
}

// TestCursorToolHook pins the builtin-tool allowlist transport (Finding 1): the
// tool transport plants a preToolUse hook that permits only MCP tools and denies
// every builtin, with failClosed:true. This is the decisive safety gate — under
// agent mode (which FULL requires) cursor's builtin write/shell tools otherwise
// execute headlessly. Live-verified end to end by
// TestCursorLive_ToolHookBlocksBuiltins.
func TestCursorToolHook(t *testing.T) {
	h := cursorHarness{}
	dir := t.TempDir()
	files, _, err := h.ToolTransport(Invocation{SelfExe: "/path/to/ai-playbook"}, "/tmp/tools.sock", dir)
	if err != nil {
		t.Fatalf("ToolTransport: %v", err)
	}

	// hooks.json wires a single preToolUse hook with failClosed:true.
	hooksPath := filepath.Join(dir, ".cursor", "hooks.json")
	b, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var doc cursorHooksDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v\n%s", err, b)
	}
	pre := doc.Hooks["preToolUse"]
	if len(pre) != 1 {
		t.Fatalf("want exactly one preToolUse hook, got %d", len(pre))
	}
	if !pre[0].FailClosed {
		t.Error("the deny hook must be failClosed:true (a crash/timeout must BLOCK, not fail-open)")
	}
	scriptPath := filepath.Join(dir, ".cursor", "hooks", "pretool-allowlist.sh")
	if pre[0].Command != "sh "+scriptPath {
		t.Errorf("hook command = %q, want %q", pre[0].Command, "sh "+scriptPath)
	}

	// The script is written, lives under dir, and is listed among the transport
	// files (so WriteToolTransport's RemoveAll cleans it up).
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read hook script: %v", err)
	}
	if !strings.Contains(string(script), `"permission":"deny"`) || !strings.Contains(string(script), "MCP:") {
		t.Errorf("hook script is not the MCP allowlist:\n%s", script)
	}
	var sawScript, sawHooks bool
	for _, f := range files {
		rel, rerr := filepath.Rel(dir, f)
		if rerr != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("transport file outside dir: %s", f)
		}
		switch f {
		case scriptPath:
			sawScript = true
		case hooksPath:
			sawHooks = true
		}
	}
	if !sawScript || !sawHooks {
		t.Errorf("transport files must include the hook script and hooks.json; got %v", files)
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
	// The transport writes the isolated mcp.json plus the builtin-allowlist hook
	// (hooks.json + the script); every artifact must live under dir so cleanup
	// (RemoveAll(dir)) removes them.
	var mcpPath string
	for _, f := range files {
		if rel, rerr := filepath.Rel(dir, f); rerr != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("cursor transport wrote outside its dir: %s (dir %s)", f, dir)
		}
		if filepath.Base(f) == "mcp.json" {
			mcpPath = f
		}
	}
	if mcpPath == "" {
		t.Fatalf("cursor transport files = %v, want a .cursor/mcp.json", files)
	}
	b, rerr := os.ReadFile(mcpPath)
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
