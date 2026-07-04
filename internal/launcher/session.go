package launcher

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/draft"
	"github.com/Townk/ai-playbook/internal/floatinput"
	"github.com/Townk/ai-playbook/internal/kb"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/reengage"
	"github.com/Townk/ai-playbook/internal/tools"
	"github.com/Townk/ai-playbook/internal/triage"
	"github.com/Townk/ai-playbook/internal/ui"
	"github.com/Townk/ai-playbook/pkg/driver"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// sessionMain is the `ai-playbook session` subcommand: the persistent docked
// pane. It reads the captured Request from --request <json> (written by the
// launcher) and runs the session body. A missing/empty --request falls back to
// capturing in-process (so `ai-playbook session` is also usable standalone).
func SessionMain() int {
	fs := flag.NewFlagSet("session", flag.ExitOnError)
	var requestPath, debugLog, titleFlag string
	fs.StringVar(&requestPath, "request", "", "path to the captured request JSON (written by the launcher)")
	fs.StringVar(&debugLog, "debug-log", "", "append a debug trace to this file (set by the launcher)")
	fs.StringVar(&titleFlag, "title", "", "working pane-header title (the classify-supplied label)")
	_ = fs.Parse(os.Args[2:]) // flag.ExitOnError: Parse never returns a non-nil error
	if debugLog == "" {
		debugLog = os.Getenv("AI_PLAYBOOK_DEBUG_LOG")
	}
	dbgInit(debugLog)
	ui.SetDebugLog(debugLog) // the ui pkg traces too; the pane got --debug-log as a flag (env dropped)
	dbg("session: start requestPath=%q", requestPath)

	// Load the mux once here and thread it through so openSession never re-loads it
	// independently — launcher and session always agree on null-vs-templated.
	m := mux.Load()

	var req capture.Request
	if requestPath != "" {
		r, err := readRequestJSON(requestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook session: read request: %v\n", err)
			return 1
		}
		req = r
		// The launcher handed the file off to us; we own its removal now.
		os.Remove(requestPath)
	} else {
		// Standalone: capture in-process (no launcher handoff).
		paneID := ""
		if p := os.Getenv("ZELLIJ_PANE_ID"); p != "" {
			paneID = "terminal_" + p
		}
		req = capture.Capture(capture.Options{
			Mux:         m,
			Atuin:       capture.NewAtuin(),
			PaneID:      paneID,
			UserRequest: os.Getenv("AI_PLAYBOOK_USER_REQUEST"),
		})
	}
	return runSession(req, titleFlag, m)
}

// runSession is the session BODY (was the inline troubleshoot): route the request
// (triage); on a cache HIT render + drive the cached playbook via the in-process
// `run` path; on a MISS author a fresh playbook with the capable agent, stream it
// into the same render+drive path, and cache it on completion. It owns the shared
// driver + tools backend (openSession) so authoring and the run blocks drive the
// SAME live shell.
//
// m is the already-selected mux threaded from the launcher (or SessionMain) so the
// session never re-loads it — launcher and session always agree on null-vs-templated.
func runSession(req capture.Request, title string, m mux.Mux) int {
	dbgEnv("runSession")
	c := cache.Open()
	noCache := os.Getenv("AI_PLAYBOOK_NO_CACHE") != ""
	d := triage.Route(req, c, noCache)
	dbg("runSession: triage outcome=%v noCache=%v", d.Outcome, noCache)

	// Configured shell (cfg.Driver.Shell) threaded into BOTH the session's shared
	// driver (openSession) and the ui's own-driver fallbacks (authorPlaybook's
	// RunStream, serveCachedPlaybook's ui.Run). "" preserves the zsh default.
	cfg, _ := config.Load() // always non-nil (Default on error)
	shell := cfg.Driver.Shell

	// Session setup: ONE shared shell driver is created here, at session start, so
	// BOTH authoring (the agent's tools backend) and the ui's run-blocks drive the
	// SAME live shell — the agent diagnoses in the exact environment the playbook's
	// steps will run in. A tools backend is exposed over a temp unix socket; the
	// claude harness reaches it via the MCP adapter (`ai-playbook mcp --socket`).
	// A failed setup degrades to no-tools authoring (sess is nil) — the ui then
	// opens its own driver, the pre-stage-5 behavior.
	// Open the session ASYNCHRONOUSLY: driver.Open spawns a shell that sources the
	// user's full profile (seconds of blank-pane startup). On a cache HIT we don't
	// want to pay that before rendering, so the session is built in the background
	// and the render path proceeds immediately; serveCachedPlaybook delivers the
	// orchestrator (built from the session's driver) to the ui once it lands.
	// No-mux ask bridge: with no multiplexer there is no float to host the agent's
	// `ask` dialog, so create a bridge that routes asks to the in-viewer overlay. It
	// is threaded into openSession (the tools-side AskFunc adapter) and the viewer
	// (RunStream/Main) so the two ends meet. nil when a real mux is present — that
	// path keeps the float ask UNCHANGED.
	var bridge *askbridge.Bridge
	if mux.IsNull(m) {
		bridge = askbridge.New()
	}
	sessCh := openSessionAsync(req, m, bridge, shell)

	switch d.Outcome {
	case triage.Hit:
		dbg("runSession: serving cached playbook")
		// serveCachedPlaybook OWNS the session: it renders instantly, waits for the
		// background open, and closes the session after ui.Run returns.
		return serveCachedPlaybook(d, req, sessCh, title, bridge, shell)
	default:
		// MISS: authoring needs the session up front (its driver-open wait is the
		// pre-existing behavior, covered by the authoring spinner). Block for it.
		sess := <-sessCh
		dbg("runSession: openSession sess!=nil=%v (agent tools %s)", sess != nil,
			map[bool]string{true: "enabled", false: "DISABLED"}[sess != nil])
		if sess != nil {
			defer sess.close()
		}
		dbg("runSession: authoring playbook (this runs the agent)")
		return authorPlaybook(req, d, c, noCache, sess, title)
	}
}

// openSessionAsync runs openSession in the background and delivers the result
// (the *session, or nil on failure) on a buffered (cap 1) channel exactly once.
// It returns the channel immediately so the caller can render before the shell's
// blank-pane startup completes. The buffer guarantees the goroutine never blocks
// on the send even if the caller never reads (e.g. the cached path closes after
// ui.Run via the done latch), so there's no leak.
// m is threaded from the caller (never re-loaded) so all paths agree on null-vs-templated.
// shell is the configured selector (cfg.Driver.Shell) threaded to openSession's driver.
func openSessionAsync(req capture.Request, m mux.Mux, bridge *askbridge.Bridge, shell string) <-chan *session {
	ch := make(chan *session, 1)
	go func() { ch <- openSession(req, m, bridge, shell) }()
	return ch
}

// prefillTemplate ports assist::prefill_template: a ready-to-submit request
// derived from the captured context. For a FAILED command it seeds the request
// float with "Diagnose and fix why `<cmd>` failed (exit N) in <proj>" so the user
// can just press Enter; for an ordinary prompt it is empty.
func prefillTemplate(req capture.Request) string {
	if req.Kind != "error" {
		return ""
	}
	proj := req.Project.Name
	if proj == "" {
		proj = "this directory"
	}
	exit := req.Exit
	if exit == "" {
		exit = "?"
	}
	return fmt.Sprintf("Diagnose and fix why `%s` failed (exit %s) in %s", req.Command, exit, proj)
}

// session bundles the per-troubleshoot shared resources: the single live shell
// driver (shared by authoring tools and the ui run blocks), the tools backend
// serving it over a unix socket, the socket path, the path to this binary
// (for the claude --mcp-config), and the selected mux. A nil *session means tools
// setup failed and the session runs in the no-agent-tools fallback (the ui opens
// its own driver).
type session struct {
	drv     *driver.Driver
	srv     *tools.Server
	socket  string
	selfExe string
	m       mux.Mux           // already-selected mux (never re-loaded); used for ask seam + asker
	bridge  *askbridge.Bridge // no-mux ask overlay bridge (nil when a real mux is present)

	lastPB atomic.Pointer[draft.Playbook] // the most recent captured playbook (for the meta seam)
}

// bridgeAskFunc adapts an askbridge.Bridge to a tools.AskFunc: the agent's `ask`
// call BLOCKS here until the viewer overlay replies (or the headless guard cancels
// it). Used on the null-mux path in place of the float Asker — the in-viewer overlay
// replaces the float dialog when there is no multiplexer to host one.
func bridgeAskFunc(b *askbridge.Bridge) tools.AskFunc {
	return func(req floatinput.Request) (floatinput.Result, error) {
		a := b.Ask(req.Prompt, req.Type, req.Choices)
		return floatinput.Result{Value: a.Value, Submitted: a.Submitted}, nil
	}
}

// ActivityBuffer is the depth of the authoring fan-out's activity channel: enough
// to absorb a brief ui stall without blocking the event pump (sends drop-if-full).
const ActivityBuffer = 16

// driverOpen is the driver.Open seam: the single site openSession spawns the shared
// shell through. It is a package var so tests can capture the driver.Options (the
// regression guard that cfg.Driver.Shell actually reaches the runtime) without
// starting a live shell. Production uses the real driver.Open.
var driverOpen = driver.Open

// openSession creates the shared driver and starts the tools backend on a temp
// unix socket. The driver's cwd is the request's project root (else its cwd).
// shell is the configured selector (cfg.Driver.Shell) threaded from runSession;
// "" preserves the zsh default. Returns nil on any failure (driver open, socket
// dir, or Serve) so the caller degrades to no-tools authoring rather than aborting.
//
// m is the already-selected mux (threaded from the launcher / SessionMain): when
// m is a real mux, the float ask is wired as before. When m is the null mux (no
// multiplexer) and a bridge is supplied, the agent's `ask` tool is routed to the
// in-viewer overlay via the bridge (the float can't be hosted without a mux).
func openSession(req capture.Request, m mux.Mux, bridge *askbridge.Bridge, shell string) *session {
	cwd := req.ProjectRoot
	if cwd == "" {
		cwd = req.CWD
	}
	drv, err := driverOpen(driver.Options{Cwd: cwd, Shell: shell})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: driver.Open failed (%v); authoring without agent tools\n", err)
		return nil
	}
	dir, err := os.MkdirTemp("", "ai-playbook-sock")
	if err != nil {
		drv.Close()
		return nil
	}
	socket := filepath.Join(dir, "tools.sock")
	selfExe, _ := os.Executable()

	// Ask seam:
	//   - real multiplexer + resolvable binary → the agent's `ask` tool spawns
	//     `ai-playbook input … --out <tmp>` in a float (UNCHANGED).
	//   - null mux (no multiplexer) + bridge → route `ask` to the in-viewer overlay
	//     (the viewer renders the dialog over the document and replies via the bridge);
	//     this replaces the prior "ask unavailable" sentinel stopgap on the no-mux path.
	//   - otherwise (no selfExe and no bridge) → nil, so the tools backend returns the
	//     unavailable sentinel as before.
	var ask tools.AskFunc
	switch {
	case selfExe != "" && !mux.IsNull(m):
		asker := floatinput.Asker{SelfExe: selfExe, Mux: m}
		ask = asker.Ask
	case bridge != nil:
		ask = bridgeAskFunc(bridge)
	}

	// Build the session struct BEFORE tools.Serve so the submit_playbook capture
	// closure can close over it: the authoring agent calls submit_playbook → backend
	// → OnPlaybook → it stores the validated playbook on sess.lastPB (read by the
	// captured-meta seam via atomic.Load, so no data race). Store is the sole writer;
	// Load is called only after the claude round-trip completes on the create goroutine.
	sess := &session{drv: drv, socket: socket, selfExe: selfExe, m: m, bridge: bridge}
	onPlaybook := func(p draft.Playbook) {
		sess.lastPB.Store(&p)
	}

	// The agent's live activity (reasoning + tool calls) is no longer surfaced via
	// the tools backend's OnActivity hook — the normalized agentstream event stream
	// (AuthorEvents → fanOut) now feeds the ui activity line directly. tools.Serve
	// still runs the run/ask/remember execution the agent invokes; we just no longer
	// observe it for DISPLAY.
	srv, err := tools.Serve(socket, tools.Deps{
		Driver:             drv,
		ProjectRoot:        req.ProjectRoot,
		Cwd:                cwd,
		Ask:                ask,
		OnPlaybook:         onPlaybook,
		ValidateFileBlocks: fileBlockValidator(req.ProjectRoot),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: tools.Serve failed (%v); authoring without agent tools\n", err)
		drv.Close()
		os.RemoveAll(dir)
		return nil
	}
	sess.srv = srv
	return sess
}

// fileBlockValidator returns a func that walks every code ContentItem with a
// non-empty File field and rejects the submission if the file already exists under
// projectRoot. The returned error is model-facing: it names the conflicting path
// and directs the model to use a diff block instead. A path that does NOT exist is
// accepted (nil error). An empty projectRoot causes filepath.Join to treat File as
// relative to the process cwd — still functional.
func fileBlockValidator(projectRoot string) func(draft.Playbook) error {
	return func(pb draft.Playbook) error {
		for _, sec := range pb.Sections {
			for _, item := range sec.Content {
				if item.Kind != "code" || item.File == "" {
					continue
				}
				if _, err := os.Stat(filepath.Join(projectRoot, item.File)); err == nil {
					return fmt.Errorf("file %q already exists — use a diff block to edit an existing file (file= is for new files)", item.File)
				}
			}
		}
		return nil
	}
}

// bridgeOf returns the session's no-mux ask bridge, or nil for a nil session. It
// is the single accessor the authoring/serve paths use to thread the bridge into
// the viewer (RunStream/Main) so the agent's `ask` reaches the in-viewer overlay.
func bridgeOf(s *session) *askbridge.Bridge {
	if s == nil {
		return nil
	}
	return s.bridge
}

// close tears down the tools backend, the shared driver, and the socket temp dir.
func (s *session) close() {
	if s == nil {
		return
	}
	if s.srv != nil {
		s.srv.Close()
	}
	if s.drv != nil {
		s.drv.Close()
	}
	os.RemoveAll(filepath.Dir(s.socket))
}

// authoringAgent returns the agent the producer should use: the MCP-tools-wired
// harness agent when the session is up (so the agent diagnoses via the `run` tool
// in the user's real shell), else the plain harness agent (author-as-before). A
// missing selfExe also falls back (we can't point the harness's --mcp-config at
// ourselves). The fallback keeps the no-agent-tools path working. cfg selects the
// harness ([agent].harness) so re-engagement/fallback honor it just like the events
// path (finding A5c).
func (s *session) authoringAgent(cfg *config.Config) author.Agent {
	if s == nil || s.selfExe == "" {
		return author.HarnessAgent(author.AuthorOptions{Cfg: cfg})
	}
	return author.HarnessAgentWithMCP(cfg, s.selfExe, s.socket)
}

// asker builds the ui.AskFunc that backs the pager's `f` keybind (spec §D): it
// spawns `ai-playbook input … --out` in a float (the same floatinput.Asker the
// agent's `ask` tool uses), opened in cwd, and returns the user's typed adjustment.
// The ui passes a prompt ("What should I change?"); the Request is fixed text type.
// Returns nil when we can't spawn ourselves (no selfExe / nil session) or when the
// mux is null — with no multiplexer the terminal is owned by the inline TUI and we
// can't open a float, so the `f` keybind no-ops (same as the no-selfExe case).
func (s *session) asker(cwd string) ui.AskFunc {
	if s == nil || s.selfExe == "" || mux.IsNull(s.m) {
		return nil
	}
	a := floatinput.Asker{SelfExe: s.selfExe, Mux: s.m}
	return func(prompt string) (string, bool) {
		res, err := a.Ask(floatinput.Request{Type: "text", Prompt: prompt, Cwd: cwd})
		if err != nil {
			return "", false
		}
		return res.Value, res.Submitted
	}
}

// writeMCPConfig writes the claude --mcp-config pointing at this session's tools
// backend and returns its path (and a removal func), so the owned AuthorEvents
// invocation reaches the agent's run/ask/remember tools. Returns "" when the
// session can't be wired (nil session, no selfExe, or a write failure) — the
// caller then authors without tools. The removal func is always safe to call.
func (s *session) writeMCPConfig() (path string, remove func()) {
	if s == nil || s.selfExe == "" {
		return "", func() {}
	}
	p, err := author.WriteMCPConfig(s.selfExe, s.socket)
	if err != nil {
		dbg("authorPlaybook: WriteMCPConfig failed (%v); authoring without agent tools", err)
		return "", func() {}
	}
	return p, func() { os.Remove(p) }
}

// authorPlaybook handles a cache MISS (stage 4b): run the capable agent to author
// a fresh playbook, stream it into the ui's in-process render+drive path (the same
// path `run <file.md>` uses), and — when the cache wasn't disabled — persist the
// produced playbook on completion.
//
// The agent's stdout STREAM is fed to ui.RunStream as the input source so the ui
// renders it incrementally and drives its run blocks against the user's real
// shell. The stream is teed to a buffer so that after the ui returns we store the
// captured body via cache.Store(ctxHash, reqHash, "playbook", body, …) alongside
// the original request.json sidecar. Storing respects triage's decision: skipped
// when the cache was disabled (unreliable key) or bypassed (no-cache).
func authorPlaybook(req capture.Request, d triage.Decision, c *cache.Cache, noCache bool, sess *session, title string) int {
	cwd := req.ProjectRoot
	if cwd == "" {
		cwd = req.CWD
	}

	// Re-engagement context (stage 4c-ii / 2b): the in-process regenerate / followup
	// / finalplaybook kinds re-invoke the author. Events (part 2b) is the OWNED normalized
	// event producer — it streams the model's live reasoning + tool activity during
	// the re-engagement wait, exactly like the initial authoring; Agent is the text
	// fallback. regenerate re-stores the fresh playbook (cache + keys), so it gets
	// them; followup/finalplaybook only need the request + producer. When the cache is
	// disabled/bypassed the keys are empty and regenerate authors-without-re-storing
	// (matching the shell's cache-bypassed re-run).
	var sharedDrv *driver.Driver
	if sess != nil {
		sharedDrv = sess.drv
	}

	// cfg is loaded here (before the reengage construction) so StoreDir can be wired
	// from cfg.GlobalStoreDir() — the single resolver that both the writer
	// (CommitPlaybook) and the reader (store.Index) use, ensuring they never diverge.
	cfg, _ := config.Load() // always non-nil (Default on error)
	// Structured authoring folds classification into the single submit_playbook call,
	// so the captured pb's Meta drives the saved front matter + project_bound
	// (capturedMetaSeam, folded into newAuthoringReengage) — NO metadata model pass.
	// Falls back to the model classifier only when nothing was submitted.
	reengage := newAuthoringReengage(req, d, c, noCache, sess, cfg)

	// INITIAL authoring runs the SHARED structured-authoring core (structuredStream):
	// the OWNED claude stream-json invocation (AuthorEvents, Structured: true) is fanned
	// into a narration reader + activity feed, while the agent submits the playbook via
	// submit_playbook → OnPlaybook → sess.lastPB. The viewer (RunStream, Structured:
	// true) shows the ProgressWidget while draining narration, then renders body() (the
	// captured playbook) on EOF as the finalDraft. The failure context
	// (req.Command/Exit/Scrollback) flows through author's SystemPrompt/BuildUserMessage
	// unchanged. The mcp-config wires the agent's run/ask/remember tools to this backend.
	cs, err := structuredStream(req, sess, cfg)
	if err != nil {
		// Fallback: the harness binary may be missing or the harness unsupported.
		// Author via the existing (markdown) text path so authoring still works.
		dbg("authorPlaybook: structured author stream failed (%v); falling back to text author path", err)
		return authorPlaybookText(req, d, c, noCache, reengage, cwd, sharedDrv, title, bridgeOf(sess), cfg.Driver.Shell)
	}
	defer cs.close()

	code := ui.RunStream(cs.reader, ui.StreamOptions{
		Harness:    "Claude Code",
		Title:      title,
		Cwd:        cwd,
		Shell:      cfg.Driver.Shell, // configured shell for RunStream's own-driver fallback
		Driver:     sharedDrv,
		Reengage:   reengage,
		Activity:   cs.activity,
		Asker:      sess.asker(cwd), // `f` proactive amend (spec §D)
		AskBridge:  bridgeOf(sess),  // no-mux agent `ask` → in-viewer overlay
		Structured: true,            // drain narration; render the captured playbook on EOF
		Body:       cs.body,         // the captured rendered playbook (finalDraft on EOF)
	})

	// Cache-store on completion — only when the cache wasn't disabled/bypassed and the
	// keys are valid. The body is the DETERMINISTIC render of the captured structured
	// playbook (fan-out text fallback only if the model submitted nothing). The disabled
	// guard (failure with empty scrollback) and the no-cache bypass leave it unstored.
	body := cs.body()
	if !d.Disabled && !noCache && d.CtxHash != "" && d.ReqHash != "" && body != "" {
		if _, serr := c.Store(d.CtxHash, d.ReqHash, "playbook", body, nil, requestJSON(req)); serr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: cache store: %v\n", serr)
		}
	}
	return code
}

// reengageStructured reports whether a re-engagement kind authors a playbook
// (submit_playbook) vs continuing the troubleshoot in markdown. Followup is
// markdown continuation; FinalPlaybook + Regenerate produce a draft.
// DriftRegen returns a raw unified diff (text), not a structured draft.
func reengageStructured(kind reengage.ReengageKind) bool {
	return kind != reengage.KindReengageFollowup && kind != reengage.KindReengageDriftRegen
}

// reengagePrompts builds the (system, user) prompt pair for one re-engagement
// invocation: the per-kind base prompt (regenerate → the standard authoring prompt
// + folded KB; followup → the failed-output prompt; finalplaybook → the
// FINAL-PLAYBOOK prompt; drift-regen → the drift prompt), then the session
// constraints folded into the system prompt via author.WithConstraints for ALL four
// kinds (spec "refuse-solution" §1). A nil/empty constraints list leaves the system
// prompt byte-identical to the pre-feature output (characterization-tested). It is a
// pure function (no session/process state) so the constraints injection is testable
// without spawning the harness.
func reengagePrompts(req capture.Request, kind reengage.ReengageKind, base, change string, constraints []string, cfg *config.Config) (sys, user string) {
	switch kind {
	case reengage.KindReengageFollowup:
		sys = author.FollowupPrompt(req, change) // change carries the failed output for followup
		user = author.BuildUserMessage(req)
	case reengage.KindReengageFinalPlaybook:
		// FINAL-PLAYBOOK (stage 2): fresh when base=="" (change = the troubleshoot
		// content to distill), amend when base!="" (fold change into the base).
		sys = author.FinalPlaybookPrompt(req, base, change)
		user = author.BuildUserMessage(req)
	case reengage.KindReengageDriftRegen:
		sys, user = author.DriftRegenPrompt(base, change) // base=current file, change=stale patch
	default: // KindReengageRegenerate → the standard authoring prompt + folded KB
		sys = author.SystemPrompt(req, author.KnowledgeBase(kb.Load(req.ProjectRoot)), driver.ResolveShellName(cfg.Driver.Shell))
		user = author.BuildUserMessage(req)
	}
	return author.WithConstraints(sys, constraints), user
}

// buildReengageEvents builds the reengage.EventsFunc that re-engagement
// (regenerate/followup/finalplaybook) uses to stream the model's live reasoning +
// tool activity, exactly like the initial authoring. It lives in main (which imports
// author) so the orchestrator stays free of an author import on the event path.
//
// Per invocation it builds the right prompt for the kind (regenerate → the
// standard authoring prompt; followup → the failed-output prompt; finalplaybook →
// the FINAL-PLAYBOOK prompt), lazily writes a fresh --mcp-config pointing at the
// session's tools backend (so the re-engaged agent still reaches run/ask/remember),
// and runs the OWNED harness invocation via author.RunHarnessEvents. The returned
// close/wait func reaps the process AND removes the per-invocation mcp-config.
//
// A nil session (no tools backend) authors-without-tools (mcp path stays empty),
// which still streams reasoning. Returns nil so the orchestrator falls back to the
// text Agent only if config can't be loaded — otherwise the EventsFunc is always
// returned and the orchestrator prefers it.
func buildReengageEvents(req capture.Request, sess *session) reengage.EventsFunc {
	return func(kind reengage.ReengageKind, base, change string, constraints []string) (<-chan agentstream.Event, func() error, error) {
		// Per-invocation mcp-config so the re-engaged agent reaches the live backend.
		mcpPath, removeMCP := sess.writeMCPConfig()

		cfg, _ := config.Load()

		sys, user := reengagePrompts(req, kind, base, change, constraints, cfg)

		// cfg already loaded above.
		events, wait, err := author.RunHarnessEvents(sys, user, author.AuthorOptions{
			Cfg:           cfg,
			MCPConfigPath: mcpPath,
			Structured:    reengageStructured(kind),
		})
		if err != nil {
			removeMCP()
			return nil, nil, err
		}
		// Wrap wait to also remove the per-invocation mcp-config once the process exits.
		closeFn := func() error {
			werr := wait()
			removeMCP()
			return werr
		}
		return events, closeFn, nil
	}
}

// buildMetadataSeam builds the reengage.Reengage.Metadata seam (spec §B):
// CommitPlaybook calls it to classify the FINISHED playbook into description /
// category / tags + per-var rationales. It lives in main (which imports author) so
// the orchestrator stays free of an author import on the commit path. The mapping
// flattens author.Metadata → reengage.PlaybookMeta, building EnvNotes
// (name → why) from ImportantEnvVars. A classification failure is returned as an
// error; CommitPlaybook then persists with empty model fields (never fails the
// commit).
func buildMetadataSeam(sess *session) func(doc string) (reengage.PlaybookMeta, error) {
	return func(doc string) (reengage.PlaybookMeta, error) {
		cfg, _ := config.Load()
		meta, err := author.PlaybookMetadata(doc, author.AuthorOptions{Cfg: cfg})
		if err != nil {
			// Non-fatal: CommitPlaybook persists a metadata-less front matter (name +
			// env + provenance) rather than failing the commit. Log so a classifier
			// outage is visible instead of silently dropping description/tags/category.
			dbg("playbook metadata classification failed; persisting without model fields: %v", err)
			return reengage.PlaybookMeta{}, err
		}
		notes := make(map[string]string, len(meta.ImportantEnvVars))
		for _, ev := range meta.ImportantEnvVars {
			if ev.Name != "" {
				notes[ev.Name] = ev.Why
			}
		}
		return reengage.PlaybookMeta{
			Description: meta.Description,
			Category:    meta.Category,
			Tags:        meta.Tags,
			EnvNotes:    notes,
		}, nil
	}
}

// buildEnvLookup builds the reengage.Reengage.EnvLookup seam (spec §C): the
// ground-truth environment lookup CommitPlaybook uses to fill (and redact) the
// front-matter env values. It dumps the DRIVER shell's environment ONCE (lazily, on
// first lookup) via `env` and caches the parsed map in the closure, so the snapshot
// reflects the live session shell (PATH/ANDROID_HOME/etc. the user actually has).
// A nil driver or a failed/empty dump yields an always-miss lookup (referenced vars
// are simply omitted from the front matter). The orchestrator never calls the driver
// directly — the dump is wired here so CommitPlaybook stays deterministically testable.
func buildEnvLookup(d *driver.Driver) func(name string) (string, bool) {
	var (
		once sync.Once
		envm map[string]string
	)
	load := func() {
		envm = map[string]string{}
		if d == nil {
			return
		}
		res := d.Run("env", DefaultEnvDumpTimeout)
		if res.Exit != 0 {
			return
		}
		for _, line := range strings.Split(res.Out, "\n") {
			if i := strings.IndexByte(line, '='); i > 0 {
				envm[line[:i]] = line[i+1:]
			}
		}
	}
	return func(name string) (string, bool) {
		once.Do(load)
		v, ok := envm[name]
		return v, ok
	}
}

// DefaultEnvDumpTimeout bounds the one-shot driver `env` dump for the EnvLookup seam.
const DefaultEnvDumpTimeout = 10 * time.Second

// authorPlaybookText is the fallback authoring path: it runs the existing
// io.ReadCloser-based author.Author (the text harness invocation) when the owned
// AuthorEvents stream can't start (harness binary missing / unsupported). It tees
// the produced playbook into a buffer for the cache, exactly as before part 2a.
func authorPlaybookText(req capture.Request, d triage.Decision, c *cache.Cache, noCache bool, reengage *reengage.Reengage, cwd string, sharedDrv *driver.Driver, title string, bridge *askbridge.Bridge, shell string) int {
	stream, err := author.Author(req, reengage.Agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: author: %v\n", err)
		return 1
	}
	defer stream.Close()

	var body bytes.Buffer
	code := ui.RunStream(stream, ui.StreamOptions{
		Harness:   "Claude Code",
		Title:     title,
		Cwd:       cwd,
		Shell:     shell, // configured shell for RunStream's own-driver fallback
		Tee:       &body,
		Driver:    sharedDrv,
		Reengage:  reengage,
		AskBridge: bridge, // no-mux agent `ask` → in-viewer overlay
	})

	if !d.Disabled && !noCache && d.CtxHash != "" && d.ReqHash != "" && body.Len() > 0 {
		if _, serr := c.Store(d.CtxHash, d.ReqHash, "playbook", body.String(), nil, requestJSON(req)); serr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: cache store: %v\n", serr)
		}
	}
	return code
}

// requestJSON serializes the captured Request into the request.json shape the
// shell wrote, for the cache sidecar (faithful regenerate context). It mirrors
// assist::build_request's JSON object.
func requestJSON(req capture.Request) string {
	type origin struct {
		PaneID      string `json:"pane_id,omitempty"`
		CWD         string `json:"cwd,omitempty"`
		ProjectRoot string `json:"project_root,omitempty"`
	}
	type command struct {
		Text       string `json:"text,omitempty"`
		Exit       string `json:"exit,omitempty"`
		DurationMs string `json:"duration_ms,omitempty"`
	}
	type project struct {
		Name   string `json:"name,omitempty"`
		Branch string `json:"branch,omitempty"`
	}
	doc := struct {
		Version     int     `json:"version"`
		Kind        string  `json:"kind"`
		Origin      origin  `json:"origin"`
		Command     command `json:"command"`
		Scrollback  string  `json:"scrollback,omitempty"`
		UserRequest string  `json:"user_request,omitempty"`
		Project     project `json:"project"`
	}{
		Version:     1,
		Kind:        req.Kind,
		Origin:      origin{PaneID: req.PaneID, CWD: req.CWD, ProjectRoot: req.ProjectRoot},
		Command:     command{Text: req.Command, Exit: req.Exit, DurationMs: req.DurationMs},
		Scrollback:  req.Scrollback,
		UserRequest: req.UserRequest,
		Project:     project{Name: req.Project.Name, Branch: req.Project.Branch},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return string(b)
}

// readRequestJSON is the inverse of requestJSON: it decodes the request JSON the
// launcher wrote (at --request <path>) back into a capture.Request for the docked
// session. It is the launcher→session context-passing decoder.
func readRequestJSON(path string) (capture.Request, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return capture.Request{}, err
	}
	return decodeRequestJSON(data)
}

// decodeRequestJSON decodes the nested request JSON (the requestJSON shape:
// origin/command/project objects) into a flat capture.Request. Shared by
// readRequestJSON (file) and answerMain (the --request flag value).
func decodeRequestJSON(data []byte) (capture.Request, error) {
	var doc struct {
		Kind   string `json:"kind"`
		Origin struct {
			PaneID      string `json:"pane_id"`
			CWD         string `json:"cwd"`
			ProjectRoot string `json:"project_root"`
		} `json:"origin"`
		Command struct {
			Text       string `json:"text"`
			Exit       string `json:"exit"`
			DurationMs string `json:"duration_ms"`
		} `json:"command"`
		Scrollback  string `json:"scrollback"`
		UserRequest string `json:"user_request"`
		Project     struct {
			Name   string `json:"name"`
			Branch string `json:"branch"`
		} `json:"project"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return capture.Request{}, err
	}
	return capture.Request{
		Kind:        doc.Kind,
		Command:     doc.Command.Text,
		Exit:        doc.Command.Exit,
		DurationMs:  doc.Command.DurationMs,
		CWD:         doc.Origin.CWD,
		ProjectRoot: doc.Origin.ProjectRoot,
		PaneID:      doc.Origin.PaneID,
		Scrollback:  doc.Scrollback,
		UserRequest: doc.UserRequest,
		Project:     capture.Project{Name: doc.Project.Name, Branch: doc.Project.Branch},
	}, nil
}

// serveCachedPlaybook renders the cached entry through the in-process viewer. The
// entry on disk carries YAML front matter; we strip it to the body, write it to a
// temp file, and hand it to ui.Run (which spins up the driver + orchestrator and
// drives the playbook in-process), setting Cached for the header badge and Cwd so
// runs execute in the request's project root.
// cachedTime parses a cache entry's created_at timestamp into the (time, cached)
// pair the viewer's badge uses: an RFC3339 value yields (t, true); an empty or
// malformed value yields (zero, false) so no badge is shown. It mirrors the parse
// ui.Main did on the former --cached flag, shared by the serveCachedPlaybook and
// AnswerMain paths that build ui.Options directly.
func cachedTime(created string) (time.Time, bool) {
	if created == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// strippedAmendBase returns the literate amend base for a served playbook: the
// front-matter-stripped body. cache.Body has already removed the OUTER (cache)
// front matter, so body still begins with the playbook's own front matter; amend
// operates on the literate content (H1 + body), not the YAML (the front matter is
// regenerated at persist), so we strip the playbook front matter here (§E/§F). A
// body without front matter is returned unchanged.
func strippedAmendBase(body string) string {
	if _, stripped, ok := frontmatter.Parse(body); ok {
		return stripped
	}
	return body
}

// reengageReady builds the OrchReady the cached-replay background goroutine delivers
// once the async session open lands. A nil session (the background open failed) → an
// empty OrchReady{} so the ui clears its pending state and stays degraded (shell
// buttons remain disabled) instead of hanging. Otherwise it folds the re-engagement
// context + the session's shared shell driver into a live orchestrator (built with
// ui's internal cliMux via ui.BuildOrch) and the request-input-float asker that backs
// the served pager's `f` keybind. This is the single logic site for the bundle the
// async path folds into the OrchReady (formerly stashed via the pending* globals).
func reengageReady(d triage.Decision, req capture.Request, sess *session, cwd string) ui.OrchReady {
	if sess == nil {
		return ui.OrchReady{}
	}
	// Resolve the global store dir so the cached-replay's CommitPlaybook (w key /
	// wrap-up) writes to the same directory store.Index reads from. config.Load never
	// returns nil (Default on error); on a config-file error storeDir stays "" and
	// CommitPlaybook falls back to its dataRoot/playbooks default (back-compat). cfg
	// also selects the harness for the text-fallback Agent (finding A5c).
	cfg, _ := config.Load()
	orch, eng := ui.BuildOrch(sess.drv, escalateReengage(d, req, sess, cfg))
	return ui.OrchReady{Orch: orch, Reeng: eng, Asker: sess.asker(cwd)}
}

// escalateReengage builds the cached-replay re-engagement context (the escalate/
// troubleshoot bundle) reengageReady hands to ui.BuildOrch. It is named (rather than
// inlined) so the StoreDir wiring — the write/read convergence with store.Index — is
// unit-testable without spawning the async ready goroutine. storeDir resolves to
// cfg.GlobalStoreDir(); an empty cfg leaves it "" so CommitPlaybook falls back to its
// dataRoot/playbooks default (back-compat).
func escalateReengage(d triage.Decision, req capture.Request, sess *session, cfg *config.Config) *reengage.Reengage {
	var storeDir string
	if cfg != nil {
		storeDir = cfg.GlobalStoreDir()
	}
	return &reengage.Reengage{
		Req:         req,
		Agent:       sess.authoringAgent(cfg),
		Events:      buildReengageEvents(req, sess),
		Cache:       cache.Open(),
		CtxHash:     d.CtxHash,
		ReqHash:     d.ReqHash,
		RequestJSON: requestJSON(req),
		Body:        reengageBody(sess, req),
		Metadata:    buildMetadataSeam(sess),
		EnvLookup:   buildEnvLookup(sess.drv),
		StoreDir:    storeDir,
	}
}

func serveCachedPlaybook(d triage.Decision, req capture.Request, sessCh <-chan *session, title string, bridge *askbridge.Bridge, shell string) int {
	raw, err := os.ReadFile(d.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: read cache entry: %v\n", err)
		return 1
	}
	content := string(raw)
	body := cache.Body(content)
	created, _ := cache.Field(content, "created_at")

	tmp, err := writeTempFile("apb-cached-*.md", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: %v\n", err)
		return 1
	}
	defer os.Remove(tmp)

	cwd := req.ProjectRoot
	if cwd == "" {
		cwd = req.CWD
	}

	// ASYNC orchestrator delivery (cached playbooks render instantly): the session's
	// shell driver is still opening in the background (openSessionAsync). Rather than
	// block here — which would re-introduce the blank-pane startup wait before the
	// cached playbook appears — we render IMMEDIATELY and hand the ui an OrchReady
	// channel. A goroutine waits for the background open, builds the orchestrator
	// (re-engagement context + shared driver folded in), and delivers it on readyCh;
	// the ui enables the shell-action buttons once it lands. A nil session (background
	// open failed) → an empty OrchReady{} so the ui clears the pending state and stays
	// degraded instead of hanging.
	//
	// The re-engagement context (stage 4c-ii): the cached pill's regenerate button
	// (and the w-key wrap-up / verify follow-up) re-author the ORIGINAL request
	// in-process, re-storing the fresh playbook under the SAME keys so the next
	// identical request hits the refreshed entry — matching ai-assist-regenerate.
	//
	// held captures the session for cleanup after ui.Run returns; it is written
	// before close(done) and read only after <-done, so the access is race-free.
	readyCh := make(chan ui.OrchReady, 1)
	held := (*session)(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sess := <-sessCh
		held = sess
		readyCh <- reengageReady(d, req, sess, cwd)
	}()

	// Build the viewer Options for the served draft. The re-engagement context, the
	// shared driver, and the request-input-float asker (the `f` keybind) all depend on
	// the still-opening session, so they are NOT set here — they are folded into the
	// OrchReady the background goroutine delivers on Ready once the open lands. Only the
	// session-independent fields are set now.
	//
	// Stage 4 (spec §C amend-on-rerun): this is a cache HIT — we are SERVING an existing
	// playbook for this context. ServedBase carries its body so a failing step →
	// troubleshoot → confirm/`w`-generate AMENDS this playbook (base=ServedBase) instead
	// of starting fresh, and the improved version is re-cached under the SAME
	// CtxHash/ReqHash — the served entry is overwritten, never lost. Amend-vs-fresh is
	// naturally scoped by the cache key: a same-context failure serves+amends this entry;
	// a different context is a cache MISS → authorPlaybook (ServedBase stays "" → fresh).
	//
	// Stage 5 (spec §E/§F): cache.Body strips the OUTER (cache) layer, so `body` still
	// begins with the playbook's own front matter. Amend operates on the literate content
	// (H1 + body), not the YAML — the front matter is regenerated at persist — so strip
	// the playbook front matter before stashing the base.
	opts := ui.Options{
		File:  tmp, // the temp file carries no front matter → renders as-is (bypasses adapt-on-run)
		Cwd:   cwd,
		Title: title, // classify-supplied label overrides the cached H1 until regenerate
		// No-mux ask overlay: re-engagement (regenerate/followup) re-invokes the agent,
		// whose `ask` reaches the overlay. nil when a real mux is present (float unchanged).
		AskBridge: bridge,
		// Configured shell for ui.Run's own-driver fallback; on the cached path the run
		// blocks normally drive the session's shared driver (delivered via Ready).
		Shell:      shell,
		ServedBase: strippedAmendBase(body),
		Ready:      readyCh,
	}
	if t, ok := cachedTime(created); ok {
		opts.Cached = true
		opts.CachedAt = t
	}
	code := uiRunFn(opts)

	// Close the session exactly once, after the ui exits: the background goroutine
	// always sends on readyCh and then closes done (openSessionAsync always delivers),
	// so <-done never hangs — whether or not the orchestrator went live. held is set
	// by the goroutine before close(done), so reading it after <-done is race-free.
	<-done
	if held != nil {
		held.close()
	}
	return code
}
