// harness_pi.go — the pi harness (github.com/earendil-works/pi-mono, the
// `@earendil-works/pi-coding-agent` CLI): the owned pi argv, the native
// --thinking mapping, and the extension tool transport. Everything pi-specific
// in package author lives HERE (the ADR-0012 seam), plus the embedded
// extension source in pi_extension.ts. Live-characterized against pi 0.80.3.
package author

import (
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func init() {
	registerHarness("pi", piHarness{}, Defaults{
		// Model "": pi picks the user's own configured default model.
		Model: "",
		// TriageModel "": the classify pass ALSO runs on the user's default pi
		// model. pi is multi-provider and `pi --list-models` enumerates its whole
		// model DB (270+ rows on 0.80.3) regardless of which providers the user
		// actually holds credentials for, so any concrete cheap-model default we
		// could bake in (a haiku/flash-class pattern) risks resolving to a
		// provider the user cannot call — turning every classify into a hard
		// failure. The user's own default model is the only always-authenticated
		// choice; a cheaper triage model is one [agent] triage_model line away.
		TriageModel: "",
		// "medium" → `--thinking medium`, so reasoning streams as live activity
		// by default (pi surfaces the actual reasoning text in --mode json).
		Thinking: "medium",
	})
}

// piExtensionSource is the embedded tool-transport extension ToolTransport
// writes (see pi_extension.ts): it registers run/ask/remember/submit_playbook,
// forwarding each call to the session's tools backend over its unix socket
// (internal/tools' newline-framed JSON RPC), mirroring the tool surface
// internal/mcpserver exposes to claude.
//
//go:embed pi_extension.ts
var piExtensionSource string

// piSocketPlaceholder is the quoted token in pi_extension.ts that ToolTransport
// replaces with the JS string literal of the session's socket path.
const piSocketPlaceholder = `"__AI_PLAYBOOK_SOCKET__"`

// piHarness is the pi {owned argv + stream adapter + process env + tool
// transport} contract. pi is a FULL harness: its tool loop validates extension
// tool arguments against their JSON schemas BEFORE execute (typebox Compile +
// Check in pi's agent core) and reports a validation failure — or a thrown
// execute error — back to the model as an error tool result, which is the
// schema-enforced re-ask loop submit_playbook/run/ask/remember ride on.
type piHarness struct{}

func (piHarness) AdapterName() string { return "pi" }

func (piHarness) DisplayName() string { return "pi" }

func (piHarness) Capabilities() Capabilities { return Capabilities{Tools: true} }

// Env: pi needs no extra process env — thinking is a native flag (--thinking),
// model selection is a flag, and provider credentials come from the user's own
// environment untouched.
func (piHarness) Env(inv Invocation) []string { return nil }

// WorkingDir: pi authoring reads the project from the caller's cwd — no scratch
// redirect (only cursor's FULL path needs one; see the Harness contract).
func (piHarness) WorkingDir(Invocation) string { return "" }

// Argv builds the OWNED pi argv for the streaming event path. The invocation
// flags and the stream adapter are a single matched contract; the user only
// selects value prefs (model, bin) via config [agent]:
//
//	pi -p --mode json --no-session --no-extensions [--no-tools]
//	   --thinking <level> [--model <model>] [toolArgv...]
//	   --append-system-prompt <systemPrompt> <userMessage>
//
// Flag rationale (all live-verified on 0.80.3):
//
//   - --no-session: never write session state (the one-shot contract,
//     ADR-0012 decision 6).
//   - --no-extensions: disable extension DISCOVERY on every path — the user's
//     installed extensions/packages are process spawns and tool surfaces
//     irrelevant to authoring, exactly like claude's --strict-mcp-config for
//     the user's global MCP servers. Explicit --extension paths (our tool
//     transport) still load, per `pi --help`.
//   - --no-tools, only when NO tool transport is attached: pi's built-in
//     bash/edit/write execute WITHOUT permission gates in print mode, so a
//     plain (tool-less) invocation must not leave the model an unsanctioned
//     mutation channel. With the transport attached the flag set is
//     --no-builtin-tools + --extension (in inv.ToolArgv), leaving EXACTLY our
//     four tools. (--no-builtin-tools with zero remaining tools hangs pi
//     0.80.3 in -p --mode json — live-characterized — so the two flags are
//     kept strictly paired with the transport.)
//   - --thinking is always passed explicitly (both ways): pi otherwise falls
//     back to the user's settings.json defaultThinkingLevel, which would make
//     the owned invocation nondeterministic.
//
// When bare is set (the cheap CLASSIFY pass), the call REPLACES the default
// system prompt with --system-prompt (instead of --append-system-prompt) and
// adds --no-context-files --no-skills --no-prompt-templates — dropping
// AGENTS.md/CLAUDE.md discovery and the skill/template resources, the analog
// of claude's --exclude-dynamic-system-prompt-sections (live-verified: the
// bare composition cut the probe's input tokens ~5x). The authoring path
// (bare=false) keeps --append-system-prompt and pi's full default context.
func (piHarness) Argv(systemPrompt, userMessage string, inv Invocation) []string {
	args := []string{
		"-p",
		"--mode", "json",
		"--no-session",
		"--no-extensions",
	}
	if len(inv.ToolArgv) == 0 {
		args = append(args, "--no-tools")
	}
	args = append(args, "--thinking", piThinkingLevel(inv.Thinking))
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	args = append(args, inv.ToolArgv...)
	if inv.Bare {
		args = append(args,
			"--no-context-files",
			"--no-skills",
			"--no-prompt-templates",
			"--system-prompt", systemPrompt,
			userMessage,
		)
		return args
	}
	args = append(args, "--append-system-prompt", systemPrompt, userMessage)
	return args
}

// ToolTransport writes pi's transport artifact — the embedded extension with
// the session's socket path spliced in — into dir, and returns the argv
// addition that attaches it: --no-builtin-tools (only OUR tools; see the Argv
// rationale) + --extension <path>. Unlike claude's transport, the extension
// dials the tools backend directly over the unix socket, so SelfExe (the
// `ai-playbook mcp` re-exec) is not needed.
func (piHarness) ToolTransport(inv Invocation, socketPath, dir string) (files []string, argv []string, err error) {
	if socketPath == "" {
		return nil, nil, errors.New("pi tool transport: the tools backend socket path is unknown")
	}
	// json.Marshal, NOT strconv.Quote: the spliced literal is parsed by JS, and
	// a JSON string literal is valid JS with identical semantics for EVERY
	// input, while Go quoting diverges on exotica (Go's \a is not a JS escape —
	// a BEL in the path would silently corrupt it — and a non-printable astral
	// rune would emit Go's \U…, a JS syntax error).
	lit, err := json.Marshal(socketPath)
	if err != nil {
		return nil, nil, err
	}
	src := strings.Replace(piExtensionSource, piSocketPlaceholder, string(lit), 1)
	if src == piExtensionSource {
		return nil, nil, errors.New("pi tool transport: the embedded extension is missing the socket placeholder")
	}
	path := filepath.Join(dir, "ai-playbook-pi-extension.ts")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		return nil, nil, err
	}
	return []string{path}, []string{"--no-builtin-tools", "--extension", path}, nil
}

// piThinkingLevel maps the config [agent].thinking preference to pi's native
// --thinking flag (off|minimal|low|medium|high|xhigh — all six are pi levels;
// minimal and xhigh have no claude analog and are simply passed through when a
// user asks for them explicitly). An empty/unrecognized preference defaults to
// "medium" so reasoning activity streams out of the box, and "off" (including
// opts.NoThinking, resolved into inv.Thinking) disables thinking — mirroring
// the claude mapping's semantics on pi's level vocabulary. Bare integer
// budgets (a claude-ism: MAX_THINKING_TOKENS) have no pi equivalent: 0 means
// off, any other integer falls back to medium.
func piThinkingLevel(thinking string) string {
	switch thinking {
	case "off", "none", "0":
		return "off"
	case "minimal", "low", "medium", "high", "xhigh":
		return thinking
	case "on", "":
		return "medium"
	default:
		if n, err := strconv.Atoi(thinking); err == nil && n == 0 {
			return "off"
		}
		return "medium"
	}
}
