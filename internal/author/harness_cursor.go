// harness_cursor.go — the Cursor harness (Cursor's headless CLI agent,
// installed as `agent` with the legacy `cursor-agent` symlink): the owned
// cursor-agent argv and the system-prompt fold. Everything cursor-specific in
// package author lives HERE (the ADR-0012 seam).
//
// LIVE-VERIFIED against cursor-agent 2026.07.01-777f564: the owned argv and the
// stream mapping below were checked against the real CLI (Phase A of the cursor
// promotion brief), correcting several doc-derived assumptions — see the
// --trust rationale on Argv and the thinking/tool_call notes on the adapter
// (agentstream/cursor.go). The RequireHarness-gated live tests
// (harness_cursor_live_test.go) re-verify wherever the CLI exists.
package author

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// cursorBin is the default executable for the cursor harness. The install
// script (https://cursor.com/install, 2026.07.01) symlinks the CLI as BOTH
// `agent` (primary) and `cursor-agent` (legacy); the legacy name is the
// default here because it exists on every install vintage and is unambiguous
// on PATH, while bare `agent` is only present on newer installs. Users on the
// primary name can set [agent] bin = "agent".
const cursorBin = "cursor-agent"

func init() {
	registerHarness("cursor", cursorHarness{}, Defaults{
		// Model "": cursor-agent picks its own default model selection when
		// --model is omitted, and the available catalog depends on the user's
		// Cursor subscription (`cursor-agent --list-models`) — the harness
		// default is the only always-valid choice, same reasoning as pi.
		Model: "",
		// TriageModel "": the classify pass also runs on cursor's own default —
		// any concrete cheap-model id we could bake in may not exist in the
		// user's plan/catalog, turning every classify into a hard failure. A
		// cheaper triage model is one [agent] triage_model line away.
		TriageModel: "",
		// Thinking "": cursor-agent has no reasoning-control flag or env var
		// (verified: `cursor-agent --help` lists none), so there is no lever for
		// this preference to drive — the harness ignores it. (Thinking events DO
		// stream in stream-json and the adapter surfaces them as Reasoning; there
		// is simply no way to tune the level.)
		Thinking: "",
		// Bin: the registry name ("cursor") is NOT the binary name — see
		// cursorBin.
		Bin: cursorBin,
	})
}

// cursorHarness is the Cursor {owned argv + stream adapter + process env + tool
// transport} contract. Cursor is a FULL harness (Phase C of the promotion
// brief): its tool loop passes each MCP tool's inputSchema to the model (which
// self-conforms) AND relays a tool's error result back for a re-ask — the
// schema-enforced loop submit_playbook/run/ask/remember ride on — and its MCP
// config root is ISOLATED per-invocation via a HOME redirect (see ToolTransport).
type cursorHarness struct{}

func (cursorHarness) AdapterName() string { return "cursor" }

func (cursorHarness) DisplayName() string { return "Cursor" }

// Capabilities: FULL — the promotion the brief's Phase C reopened and proved
// (cursor-agent 2026.07.01-777f564). Phase B found the naive attach unsafe
// (project `.cursor/mcp.json` MERGES with the user's global `~/.cursor/mcp.json`;
// no --strict-mcp-config analog); Phase C found the STRUCTURAL fix: cursor-agent
// resolves its global MCP config from os.homedir()/.cursor/mcp.json and honors a
// HOME override (there is NO CURSOR_CONFIG_DIR/CURSOR_HOME/XDG_CONFIG_HOME config
// root var — the other candidates are no-ops). Redirecting HOME at a pristine
// per-invocation root that holds ONLY our server isolates the config cleanly:
//
//  1. Isolation (deterministic, `cursor-agent mcp list` oracle). Under
//     HOME=<root> the model sees ONLY our server — the user's global
//     atlassian/zellij/glean/context7 do not load. ToolTransport verifies this
//     at wire time (the Step-5 guard) before enabling tools.
//  2. Auth survives (darwin). The macOS keychain is HOME-relative
//     ($HOME/Library/Keychains), so the transport symlinks the REAL keychain
//     dir into the redirect root (no access cursor-agent lacks in BASIC). The
//     guard's `status` probe confirms auth held; if not, it degrades to BASIC.
//  3. Approval is safe once isolated. --approve-mcps under the redirect can
//     approve ONLY our server (nothing else is configured); it never touches
//     the user's durable ~/.cursor state (diff-verified before/after).
//  4. Schema enforcement. cursor-agent passes inputSchema to the model and
//     relays a tool's isError result back, driving an automatic re-ask
//     (live-verified: an even-port-only server rejected an odd port and the
//     model retried with an even one) — enough for structured submit_playbook.
//
// See docs/specifications/multi-harness.md (cursor section) for the full Phase C
// probe record.
func (cursorHarness) Capabilities() Capabilities { return Capabilities{Tools: true} }

// Env redirects HOME at the transport root on the TOOL path so cursor-agent
// resolves its MCP config from the pristine `<ToolDir>/.cursor/mcp.json` we
// populated (holding ONLY our server) instead of the user's global
// `~/.cursor/mcp.json` — the ISOLATION mechanism (Phase C). ToolDir is set only
// when tools are wired (WriteToolTransport → RunHarnessEvents); the tool-less
// paths (authoring text, classify, metadata) leave it empty and cursor-agent
// runs against the user's own HOME untouched. Authentication survives the
// redirect via the keychain symlink ToolTransport plants under the root.
func (cursorHarness) Env(inv Invocation) []string {
	if inv.ToolDir != "" {
		return []string{"HOME=" + inv.ToolDir}
	}
	return nil
}

// Argv builds the OWNED cursor-agent argv for the streaming event path. The
// invocation flags and the stream adapter are a single matched contract; the
// user only selects value prefs (model, bin) via config [agent]:
//
//	cursor-agent -p --output-format stream-json --stream-partial-output
//	             --mode ask --trust [--model <model>] [toolArgv...] <foldedPrompt>
//
// Flag rationale (live-verified against cursor-agent 2026.07.01-777f564):
//
//   - -p --output-format stream-json: the documented headless NDJSON shape
//     (cursor.com/docs/cli/reference/output-format). The prompt is positional,
//     per the documented examples (cursor.com/docs/cli/overview).
//   - --stream-partial-output: without it, assistant text arrives only as
//     whole message segments; with it, the CLI streams real text deltas the
//     adapter turns into live TextDelta events (the same role as claude's
//     --include-partial-messages). The adapter's dedup rule assumes this flag
//     is always set — see the cursor adapter (agentstream/cursor.go).
//   - --mode ask: Cursor's documented READ-ONLY mode ("answers questions and
//     explores code without making any edits",
//     cursor.com/help/ai-features/ask-mode; the CLI flag:
//     cursor.com/docs/cli/reference/parameters). Print mode can otherwise use
//     write and shell tools (cursor.com/docs/cli/reference/permissions) — an
//     unsanctioned mutation channel for an invocation whose entire job is to
//     produce text (the same hazard pi's --no-tools closes). Every cursor path
//     is text-producing (BASIC ⇒ text authoring, classify, followup, review),
//     so read-only is correct for all of them; a FULL promotion revisits this.
//   - --trust: REQUIRED for a headless run in a directory the user has not
//     already trusted interactively. Without it cursor-agent refuses to start
//     ("Workspace Trust Required") and the stream never opens — the BASIC floor
//     cannot be met (live-verified: a fresh dir fails, adding --trust succeeds).
//     Crucially --trust is EPHEMERAL and NARROW: it writes NOTHING durable
//     (verified — the ~/.cursor state files are byte-identical before and after
//     a --trust run) and, unlike --force/--yolo, does NOT lift the per-command
//     permission gates; it only clears the one-time workspace-trust prompt for
//     this invocation. The flag is documented as headless-only ("only works
//     with --print/headless mode", `cursor-agent --help`).
//   - NO session flags: --resume/--continue are never emitted (the one-shot
//     contract). cursor-agent has no documented flag to suppress its local
//     chat persistence (each print run still records a session id); no state
//     is ever REUSED, which is what the contract requires.
//   - NO --force/--yolo: those lift the per-command permission gates (run
//     everything), a mutation channel --mode ask + --trust deliberately do not
//     open — read-only tools only, no state mutation.
//
// System-prompt handling: cursor-agent has NEITHER a replace nor an append
// system-prompt flag (none documented in cursor.com/docs/cli/reference/
// parameters), and no context-suppression flags either — .cursor/rules,
// AGENTS.md, and CLAUDE.md auto-load unconditionally (cursor.com/docs/cli/
// using). So BOTH paths use the multi-harness spec's documented fallback:
// the system prompt is folded into the single positional user message
// (cursorFoldPrompt), and bare and append are the SAME argv shape — there is
// nothing to strip and nothing to replace.
func (cursorHarness) Argv(systemPrompt, userMessage string, inv Invocation) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--stream-partial-output",
	}
	// Mode is a function of the tier of THIS invocation:
	//
	//   - Tool-less (BASIC text authoring, classify, followup, review): --mode
	//     ask keeps cursor-agent READ-ONLY, closing its built-in write/shell
	//     mutation channel for a turn whose only job is to produce text.
	//   - Tools wired (FULL): --mode ask is DROPPED. cursor-agent REFUSES MCP
	//     tool calls in ask mode ("I'm in Ask mode, which restricts me to
	//     read-only actions" — live-verified), so our run/ask/remember/
	//     submit_playbook tools would never dispatch. Default (agent) mode is
	//     required; the model is steered to READ-ONLY diagnosis via `run` and
	//     to emit the playbook as its deliverable by the tool-instruction fold,
	//     the same contract the claude/pi FULL paths run under.
	if len(inv.ToolArgv) == 0 {
		args = append(args, "--mode", "ask")
	}
	args = append(args, "--trust")
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	// The tool transport's attach argv (cursor: ["--approve-mcps"], so our sole
	// isolated server loads headlessly without an interactive approval prompt).
	args = append(args, inv.ToolArgv...)
	args = append(args, cursorFoldPrompt(systemPrompt, userMessage))
	return args
}

// cursorMCPListTimeout bounds each wire-time isolation-guard probe (`mcp list`,
// `status`). Both are local, model-free CLI calls that returned in ~1-4s during
// Phase C; a short bound keeps a stalled CLI from hanging authoring (a stall
// degrades to BASIC, never blocks).
const cursorMCPListTimeout = 20 * time.Second

// ToolTransport wires cursor's ISOLATED tools by REDIRECTING its config root.
// cursor-agent has no per-invocation MCP-config flag (no --mcp-config /
// --strict-mcp-config analog); it resolves the global config from
// os.homedir()/.cursor/mcp.json and honors a HOME override (Phase C). So the
// transport:
//
//  1. writes `<dir>/.cursor/mcp.json` holding ONLY our server
//     (`<SelfExe> mcp --socket <socketPath>`, the same document claude emits);
//  2. on darwin, symlinks the REAL macOS keychain dir into `<dir>/Library` so
//     authentication survives the HOME redirect (the keychain is HOME-relative;
//     this exposes nothing cursor-agent lacks in BASIC — it already reads this
//     keychain to authenticate);
//  3. runs the Step-5 isolation guard when a bin is known (production + the live
//     test); and
//  4. returns ["--approve-mcps"] as the attach argv — under the isolated root
//     that approves ONLY our server.
//
// Env(inv) returns HOME=<dir> for the matching invocation, so cursor-agent reads
// the config we just wrote. WriteToolTransport removes `dir` when the stream
// closes.
func (cursorHarness) ToolTransport(inv Invocation, socketPath, dir string) (files []string, argv []string, err error) {
	if inv.SelfExe == "" {
		return nil, nil, errors.New("cursor tool transport: the ai-playbook executable path (SelfExe) is unknown")
	}
	if socketPath == "" {
		return nil, nil, errors.New("cursor tool transport: the tools backend socket path is unknown")
	}

	cursorDir := filepath.Join(dir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o700); err != nil {
		return nil, nil, err
	}
	doc, err := mcpServersDocument(inv.SelfExe, socketPath)
	if err != nil {
		return nil, nil, err
	}
	cfgPath := filepath.Join(cursorDir, "mcp.json")
	if err := os.WriteFile(cfgPath, doc, 0o600); err != nil {
		return nil, nil, err
	}
	files = append(files, cfgPath)

	// Auth survival under the redirect. cursor-agent's darwin credential store
	// is the macOS keychain, resolved via $HOME/Library/Keychains — so a bare
	// HOME override loses auth. Symlinking the REAL keychain dir into the root
	// restores it while the MCP config stays isolated (only .cursor/mcp.json is
	// ours; nothing else about the user's HOME is exposed). On non-darwin the
	// credential store differs and is not redirect-validated here; the guard's
	// `status` probe catches a broken auth and degrades to BASIC.
	if runtime.GOOS == "darwin" {
		if realHome, herr := os.UserHomeDir(); herr == nil && realHome != dir {
			libDir := filepath.Join(dir, "Library")
			if err := os.MkdirAll(libDir, 0o700); err != nil {
				return nil, nil, err
			}
			// Best-effort: a missing keychain surfaces as the guard's auth
			// failure (→ BASIC), not a transport-write error.
			_ = os.Symlink(filepath.Join(realHome, "Library", "Keychains"), filepath.Join(libDir, "Keychains"))
		}
	}

	// Step-5 runtime guard. By construction `dir` is a fresh temp root holding
	// exactly our one-server config — but never trust the redirect held. When a
	// bin is known (production sets inv.Bin via WriteToolTransport; the CLI-free
	// unit contract passes none), verify with cursor-agent's own oracle that
	// under HOME=<dir> ONLY our server is visible and auth survived. Any foreign
	// server or lost auth fails the transport → the caller degrades to BASIC.
	if inv.Bin != "" {
		if verr := cursorVerifyIsolation(inv.Bin, dir); verr != nil {
			return nil, nil, verr
		}
	}

	return files, []string{"--approve-mcps"}, nil
}

// cursorVerifyIsolation is the Step-5 guard: under HOME=home it confirms
// cursor-agent sees ONLY our server (no leaked global MCP server) and is still
// authenticated. Both probes are deterministic — `mcp list` reports configured
// servers and `status` reports login state, neither makes a model call. A
// non-nil error means "do NOT enable tools" and the caller degrades to BASIC.
func cursorVerifyIsolation(bin, home string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cursorMCPListTimeout)
	defer cancel()
	env := append(os.Environ(), "HOME="+home)

	list := exec.CommandContext(ctx, bin, "mcp", "list")
	list.Env = env
	out, lerr := list.CombinedOutput()
	if lerr != nil {
		return fmt.Errorf("cursor tool transport: isolation probe (%s mcp list) failed: %w (output: %s)", bin, lerr, strings.TrimSpace(string(out)))
	}
	for _, name := range parseCursorMCPList(string(out)) {
		if name != mcpServerName {
			return fmt.Errorf("cursor tool transport: refusing to enable tools — foreign MCP server %q is visible under the isolated config root (only %q may be present); staying BASIC", name, mcpServerName)
		}
	}

	status := exec.CommandContext(ctx, bin, "status")
	status.Env = env
	sout, _ := status.CombinedOutput()
	if !strings.Contains(strings.ToLower(string(sout)), "logged in") {
		return fmt.Errorf("cursor tool transport: authentication did not survive the HOME redirect (%s status: %s); staying BASIC", bin, strings.TrimSpace(string(sout)))
	}
	return nil
}

// parseCursorMCPList extracts the server NAMES from `cursor-agent mcp list`
// output (one "servername: status" line per configured server; the status may
// itself contain colons, e.g. "Error: Connection failed"). Blank/garbage lines
// are ignored — the guard only cares about the SET of configured server names.
func parseCursorMCPList(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		names = append(names, strings.TrimSpace(line[:i]))
	}
	return names
}

// cursorFoldPrompt is the system-prompt fold (the spec's documented fallback
// for a harness with no system-prompt flags — see the Argv note): the system
// prompt travels at the head of the single positional user message, fenced in
// an explicit tag so the model reads it as standing instructions rather than
// part of the request.
func cursorFoldPrompt(systemPrompt, userMessage string) string {
	return "<system_instructions>\n" + systemPrompt + "\n</system_instructions>\n\n" + userMessage
}
