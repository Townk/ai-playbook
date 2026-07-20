package author

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// defaultCallTimeout bounds a quick STRUCTURED call (classify, metadata) to the
// owned harness: those two calls gate every request and are meant to complete in
// a few seconds, so a hung/stalled harness process must not block the caller
// indefinitely (A5a). 60s is generous headroom over the ~2-3s these calls
// normally take. The interactive streaming authoring kinds (initial authoring,
// followup/regenerate/wrap-up) intentionally leave AuthorOptions.Timeout at its
// zero value (no bound) — long tool-using author sessions are expected; their
// FAILURES surface instead (A5a-full): the agentstream fan-out propagates the
// producer's wait error (truncation, timeout kill, non-zero exit) to the doc
// reader, so the ui renders the failure rather than a silent clean finish. The
// single-shot drift-regenerate call is bounded by DriftRegenTimeout. A caller
// may override per call via AuthorOptions.Timeout (the classify/metadata call
// sites do; tests use it to force a short deadline).
const defaultCallTimeout = 60 * time.Second

// DriftRegenTimeout bounds the single-shot drift-regenerate call: a no-tools,
// diff-only invocation gated behind a viewer button, so a stalled harness must
// not hang the drift action forever (A5a-full). Generous headroom over the
// seconds-to-a-minute the call normally takes on large files.
const DriftRegenTimeout = 5 * time.Minute

// AuthorOptions tunes AuthorEvents. Cfg supplies the harness selection + value
// prefs ([agent]); ToolArgv, when set, wires the tools backend into the owned
// argv. Command is the test seam: when non-nil it replaces the real process
// launch with a caller-built *exec.Cmd (the fake-harness pattern), so
// AuthorEvents is unit-testable without a real harness on PATH.
type AuthorOptions struct {
	Cfg *config.Config
	// ToolArgv, when non-empty, is the tool-transport argv addition returned by
	// Harness.ToolTransport (claude: ["--mcp-config", <path>]). It is spliced
	// into the owned argv AND triggers the tool-instruction fold into the system
	// prompt. Empty → no tools backend (the plain invocation).
	ToolArgv []string
	// ToolDir is the per-invocation transport root returned by
	// WriteToolTransport. It reaches the harness's Env via inv.ToolDir: cursor
	// redirects its config ROOT there (HOME=<ToolDir>) so it reads only OUR MCP
	// config; claude/pi ignore it. Set only on the tool paths, alongside ToolArgv.
	ToolDir string
	// ModelOverride, when non-empty, replaces cfg [agent].Model for THIS invocation
	// only (the owned argv's --model). It is the seam the cheap CLASSIFY pass uses to
	// run on the triage model without disturbing the authoring path (which keeps
	// using cfg [agent].Model). Empty → the configured Model is used as before.
	ModelOverride string
	// Structured, when true, appends StructuredToolInstruction() instead of
	// ToolInstruction when ToolArgv is set: the create flow directs the model to
	// diagnose with run/ask and return the playbook as DATA via submit_playbook (the
	// host renders the markdown), rather than writing {id=…} markdown itself. Only the
	// create path sets it; the markdown authoring paths leave it false.
	Structured bool
	// Bare, when true, strips the owned claude argv to a BARE quick-model call: it
	// REPLACES the default system prompt (--system-prompt, not --append-system-prompt)
	// and adds --strict-mcp-config + --exclude-dynamic-system-prompt-sections, so the
	// classify pass runs without CLAUDE.md auto-discovery, auto-memory, global MCP, or
	// the dynamic machine sections. The AUTHORING path leaves this false (full context).
	Bare bool
	// NoThinking, when true, forces MAX_THINKING_TOKENS=0 for THIS invocation,
	// disabling extended thinking regardless of cfg [agent].Thinking. The quick
	// structured calls (classify, metadata) set it: a triage/JSON decision needs no
	// reasoning, and leaving thinking on costs ~4-6s of latency (haiku thinks by
	// default). The AUTHORING path leaves this false so its reasoning streams as
	// live activity.
	NoThinking bool
	// OnText, when non-nil, is called with the ACCUMULATED assistant text as each
	// stream-json TEXT delta arrives (a live tap of the model output, used by the
	// classify pass to surface its reasoning on the float's thinking line). nil →
	// no-op; behavior unchanged.
	OnText func(accumulated string)
	// Timeout bounds THIS invocation's owned harness process via
	// exec.CommandContext: when the process has not exited within Timeout, it is
	// killed and wait() returns an error joining context.DeadlineExceeded. Zero
	// (the default) means no bound — used by the streaming authoring path.
	// ClassifyRequest/PlaybookMetadata set this to defaultCallTimeout when unset
	// (A5a); a caller (or a test) may set it explicitly to override, including to
	// a short deadline for deterministic timeout tests.
	Timeout time.Duration
	// Command overrides process construction for tests. It receives the resolved
	// ctx (carrying the Timeout deadline, if any), bin, and owned argv, and
	// returns the *exec.Cmd to run. A seam that wants the timeout to actually
	// kill its process must build via exec.CommandContext(ctx, ...); a seam that
	// ignores ctx runs unbounded. nil → exec.CommandContext(ctx, bin, args...).
	Command func(ctx context.Context, bin string, args []string) *exec.Cmd
	// Adapter overrides the resolved agentstream.Adapter for tests (e.g. one with
	// a deterministic, test-shaped failure mode) without touching the shipped
	// agentstream registry. nil → the harness's normal registered adapter
	// (agentstream.Get(adapterName)).
	Adapter agentstream.Adapter
}

// AuthorEvents runs the configured harness on req and returns a channel of
// normalized agentstream.Events, a close/wait func that reaps the process, and a
// start error. It builds the STANDARD authoring prompt (system prompt + folded KB,
// user message) from req and delegates the owned invocation + adapter to
// RunHarnessEvents. Re-engagement (followup/wrapup/regenerate) reuses
// RunHarnessEvents directly with its own prompts.
//
// The returned func() error waits for the process to exit (reaping it) and
// returns its exit error; call it after draining the channel.
func AuthorEvents(req capture.Request, opts AuthorOptions) (<-chan agentstream.Event, func() error, error) {
	// Resolve the effective shell from cfg so the authoring prompt names the right
	// shell and gives shell-appropriate guidance (POSIX-only for sh, etc.).
	cfg := opts.Cfg
	if cfg == nil {
		cfg = config.Default()
	}
	shell := driver.ResolveShellName(cfg.Driver.Shell)
	global, project := recallFor(req.ProjectRoot, cfg)
	sys := SystemPrompt(req, global, project, shell)
	user := BuildUserMessage(req)
	return RunHarnessEvents(sys, user, opts)
}

// RunHarnessEvents is the OWNED harness invocation + adapter core: given a final
// systemPrompt and userMessage, it selects the harness from cfg [agent].Harness
// (claude implemented; pi/cursor return a clear "not yet supported" error),
// resolves the bin (cfg [agent].Bin else the harness name on PATH), builds the
// OWNED argv, starts the process, and streams its stdout through
// agentstream.Get(adapter)'s Parse, forwarding each event on the returned channel
// (closed on EOF).
//
// When opts.ToolArgv is set (the Harness.ToolTransport attachment), the harness's
// tool-use instruction is appended to systemPrompt (so the agent reaches the
// session's run/ask/remember backend) and the transport argv is wired into the
// owned argv — exactly as the standard authoring path does, so re-engagement's
// followup/wrapup/regenerate prompts get the same tools.
//
// The returned func() error waits for the process to exit (reaping it) and returns
// its exit error; call it after draining the channel.
func RunHarnessEvents(systemPrompt, userMessage string, opts AuthorOptions) (<-chan agentstream.Event, func() error, error) {
	cfg := opts.Cfg
	if cfg == nil {
		cfg = config.Default()
	}

	harnessName := resolveHarnessName(cfg)
	h, ok := harnessFor(harnessName)
	if !ok {
		return nil, nil, fmt.Errorf("harness %q not yet supported", harnessName)
	}

	// Prompt assembly stays HERE (harness-free): fold the tool instruction into the
	// system prompt when the tools backend is wired, then resolve the per-call knobs.
	// The Harness only turns those into its owned argv/adapter/env.
	sys := systemPrompt
	if len(opts.ToolArgv) > 0 {
		if opts.Structured {
			sys += StructuredToolInstruction()
		} else {
			sys += ToolInstruction
		}
	}
	// Value-pref resolution (ADR-0012 decision 4): an explicit [agent] value wins;
	// an empty one falls through to the harness's own defaults row.
	defaults := HarnessDefaults(harnessName)
	// Per-invocation model override (the classify pass selects the triage model)
	// falls back to the configured authoring model when unset.
	model := cfg.Agent.Model
	if model == "" {
		model = defaults.Model
	}
	if opts.ModelOverride != "" {
		model = opts.ModelOverride
	}
	// NoThinking forces thinking off for THIS call regardless of cfg.Thinking.
	thinking := cfg.Agent.Thinking
	if thinking == "" {
		thinking = defaults.Thinking
	}
	if opts.NoThinking {
		thinking = "off"
	}
	inv := Invocation{
		Model:    model,
		ToolArgv: opts.ToolArgv,
		ToolDir:  opts.ToolDir,
		Bare:     opts.Bare,
		Thinking: thinking,
	}
	args := h.Argv(sys, userMessage, inv)
	adapterName := h.AdapterName()
	extraEnv := h.Env(inv)

	adapter, ok := agentstream.Get(adapterName)
	if !ok {
		// Should not happen for a shipped harness; fall back to passthrough.
		adapter, _ = agentstream.Get("text")
	}
	if opts.Adapter != nil {
		// Test seam: substitute a fake Adapter (e.g. one that treats malformed
		// stream-json as fatal) without touching the shipped agentstream registry.
		adapter = opts.Adapter
	}

	bin := HarnessBin(cfg)

	ctx := context.Background()
	cancel := func() {}
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}

	cmd := buildCommand(ctx, opts.Command, bin, args)
	// Working directory: a harness that must run in a scratch cwd (cursor's FULL
	// path — a builtin/hook bypass must not touch the user's real project)
	// returns it here; "" inherits the caller's cwd (claude/pi, and cursor's
	// tool-less paths). Applied after buildCommand so it wins over a test seam's
	// own cmd.Dir — production and tests both run the FULL path in the scratch
	// transport root.
	if wd := h.WorkingDir(inv); wd != "" {
		cmd.Dir = wd
	}
	if len(extraEnv) > 0 {
		// Inherit the parent env (nil Env == os.Environ at exec time) and append
		// our extras. Set explicitly only when adding, to avoid disturbing a
		// test-seam command that may have configured its own Env.
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, extraEnv...)
	}
	// Capture stderr (don't pipe to the terminal — it bled into the no-mux inline
	// UI); surface it only when the process fails. See author.go withStderr.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}

	// parseErr captures adapter.Parse's return (a fatal read failure or a
	// detected stream-contract violation — the claude adapter is strict since
	// A5b-strict: non-JSON lines and a missing result envelope error). It is set
	// exactly once by the goroutine below, then read from wait() AFTER the
	// draining caller has observed the events channel close; the close
	// happens-after this assignment (deferred close runs after Parse returns),
	// so there is no data race despite the cross-goroutine access (A5b: this
	// used to be silently discarded via `_ = adapter.Parse(...)`, so a
	// truncated/malformed stream on a clean exit (0) was indistinguishable from
	// success).
	var parseErr error
	events := make(chan agentstream.Event)
	go func() {
		defer close(events)
		parseErr = adapter.Parse(stdout, func(e agentstream.Event) { events <- e })
	}()

	wait := func() error {
		defer cancel()
		joined := errors.Join(parseErr, cmd.Wait())
		if ctxErr := ctx.Err(); ctxErr != nil {
			// cmd.Wait() alone reports the killed process ("signal: killed"), not
			// WHY it was killed — join in the context error (A5a) so a caller can
			// distinguish "the harness stalled past its timeout" from an ordinary
			// process failure (e.g. via errors.Is(err, context.DeadlineExceeded)).
			joined = errors.Join(joined, ctxErr)
		}
		return withStderr(joined, &stderr)
	}
	return events, wait, nil
}

// buildCommand applies the test seam if present, else exec.CommandContext(ctx,
// ...) — so the default (production) path is always bounded by ctx's deadline
// (A5a). See AuthorOptions.Command for the seam's ctx-propagation contract.
func buildCommand(ctx context.Context, override func(context.Context, string, []string) *exec.Cmd, bin string, args []string) *exec.Cmd {
	if override != nil {
		return override(ctx, bin, args)
	}
	return exec.CommandContext(ctx, bin, args...)
}
