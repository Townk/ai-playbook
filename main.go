// ai-playbook — unified terminal AI-assist / playbook binary.
//
// Subcommands (git-style; the binary self-spawns for floats/panes):
//
//	troubleshoot   AI producer: capture → triage → author a playbook → drive it
//	run <file.md>  playbook runtime: render + orchestrate a playbook artifact
//	input          the multi-line input widget
//	selftest       drive the user's real shell and report (validates the driver)
//
// Stage 1 ships the driver core + selftest; the rest are stubs filled in by the
// strangler migration (see docs/superpowers/specs/2026-06-24-ai-playbook-unification-design.md).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-playbook/agentstream"
	"ai-playbook/author"
	"ai-playbook/cache"
	"ai-playbook/capture"
	"ai-playbook/config"
	"ai-playbook/driver"
	"ai-playbook/floatinput"
	"ai-playbook/input"
	"ai-playbook/kb"
	"ai-playbook/mcpserver"
	"ai-playbook/mux"
	"ai-playbook/orchestrator"
	"ai-playbook/tools"
	"ai-playbook/triage"
	"ai-playbook/ui"

	"bytes"
	"encoding/json"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "selftest":
		os.Exit(selftest())
	case "troubleshoot":
		os.Exit(troubleshoot())
	case "session":
		os.Exit(sessionMain())
	case "run":
		os.Exit(ui.Main())
	case "mcp":
		os.Exit(mcpMain())
	case "input":
		os.Exit(input.Main())
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "ai-playbook: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ai-playbook {troubleshoot|session [--request <json>]|run <file.md>|mcp --socket <path>|input|selftest}")
}

// mcpMain is the `ai-playbook mcp --socket <path>` subcommand: an MCP stdio
// server (the claude harness adapter) whose tool calls dial the session's tools
// backend at <path>. claude launches this via --mcp-config; it forwards run /
// remember / ask to the unix socket. Blocks until the client disconnects.
func mcpMain() int {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	var socket string
	fs.StringVar(&socket, "socket", "", "path to the session's tools-backend unix socket")
	argv := os.Args[2:]
	fs.Parse(argv)
	if socket == "" {
		fmt.Fprintln(os.Stderr, "ai-playbook mcp: --socket <path> is required")
		return 2
	}
	if err := mcpserver.Run(socket); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook mcp: %v\n", err)
		return 1
	}
	return 0
}

// selftest drives the user's REAL shell (unaltered) and reports — the live
// counterpart to the package's deterministic tests.
func selftest() int {
	say := func(f string, a ...any) { fmt.Printf("selftest> "+f+"\n", a...) }
	fails := 0
	chk := func(name string, ok bool, detail string) {
		if ok {
			say("  PASS — %s", name)
		} else {
			say("  FAIL — %s (%s)", name, detail)
			fails++
		}
	}

	d, err := driver.Open(driver.Options{})
	if err != nil {
		say("FATAL: %v", err)
		return 1
	}
	defer d.Close()
	say("driver up: real zsh -il, unaltered")

	have := func(name string) bool { return d.Run("command -v "+name+" >/dev/null 2>&1", 5*time.Second).Exit == 0 }
	home, _ := os.UserHomeDir()

	// interactive env
	if app := filepath.Join(home, "Projects/platforms/android/SampleApp1"); dirExists(app) {
		r := d.Run("builtin cd -- "+app+"; gg build 2>&1", 30*time.Second)
		say("  'gg build' → exit=%d out=%q", r.Exit, head(r.Out, 70))
		chk("gg resolves (not command-not-found)", !strings.Contains(r.Out, "not found"), r.Out)
	}

	// auto-env on cd
	if have("mise") {
		dir, _ := os.MkdirTemp("", "selftest-mise")
		defer os.RemoveAll(dir)
		os.WriteFile(filepath.Join(dir, "mise.toml"), []byte("[env]\nSELFTEST_MISE = \"mise-works\"\n"), 0644)
		d.Run("mise trust "+dir+" 2>/dev/null || true", 10*time.Second)
		d.Run("builtin cd -- "+dir, 10*time.Second)
		r := d.Run("print -r -- ${SELFTEST_MISE:-MISSING}", 10*time.Second)
		chk("mise [env] on cd", r.Out == "mise-works", r.Out)
		d.Run("builtin cd -- /tmp", 5*time.Second)
	} else {
		say("  (mise not installed — skipping auto-env check)")
	}

	// capture, persistence, kill
	r := d.Run("print -r -- o; print -ru2 -- e; (exit 7)", 10*time.Second)
	chk("stdout/stderr/exit", r.Out == "o" && r.Err == "e" && r.Exit == 7, fmt.Sprintf("%+v", r))
	d.Run("builtin cd -- /tmp", 5*time.Second)
	chk("cd persists", d.Run("pwd", 5*time.Second).Out == "/tmp", "")
	chk("timeout kills + survives", d.Run("sleep 30", 2*time.Second).TimedOut && d.Run("echo alive", 5*time.Second).Out == "alive", "")

	say("")
	if fails == 0 {
		say("RESULT: ALL PASS")
		return 0
	}
	say("RESULT: %d FAILED", fails)
	return 1
}

// troubleshoot is the LAUNCHER: it runs transiently in the user's ORIGIN pane
// (spawned by the ZLE trigger), gathers the bounded origin context, asks the user
// for their request via an input FLOAT, then spawns the persistent docked SESSION
// pane (`ai-playbook session`) and exits. The docked pane owns the rest of the
// lifecycle (triage → author/serve → drive); the launcher must return promptly so
// the user's prompt stays live.
//
// Topology (mirrors the old ai-assist-summon → input-float → docked-render flow,
// now one binary): capture here (while we still hold the origin shell's env) →
// SpawnFloat `ai-playbook input … --out <tmp>` with the prefilled request →
// poll the out-file for the submitted request → on cancel, exit cleanly → on
// submit, write the captured Request to a temp JSON and SpawnDocked
// `ai-playbook session --request <json>`. See runSession for the body.
//
// An explicit request on the CLI (args after `troubleshoot`, or
// $AI_ASSIST_USER_REQUEST) SKIPS the float — the request is already known. Off a
// mux (no zellij) there is no float/pane to spawn; the launcher runs the session
// INLINE in the current pane (the pre-topology behavior), so headless and SSH
// contexts still work.
func troubleshoot() int {
	dbgInit(os.Getenv("AI_ASSIST_DEBUG_LOG"))
	cliRequest := strings.TrimSpace(strings.Join(os.Args[2:], " "))
	if cliRequest == "" {
		cliRequest = os.Getenv("AI_ASSIST_USER_REQUEST")
	}

	// pane id from env (mirrors the shell's ZELLIJ_PANE_ID → terminal_<id>).
	paneID := ""
	if p := os.Getenv("ZELLIJ_PANE_ID"); p != "" {
		paneID = "terminal_" + p
	}

	m := mux.Load()

	// Capture the bounded origin context NOW, in the origin pane, while we still
	// hold the origin shell's env (atuin session, cwd, pane id, scrollback).
	req := capture.Capture(capture.Options{
		Mux:         m,
		Atuin:       capture.NewAtuin(),
		PaneID:      paneID,
		UserRequest: cliRequest,
	})
	dbg("troubleshoot: cmd=%q exit=%q kind=%q cwd=%q root=%q paneID=%q cliReq=%q",
		req.Command, req.Exit, req.Kind, req.CWD, req.ProjectRoot, paneID, cliRequest)

	// In Zellij with no explicit request: ask via the input float, then spawn the
	// docked session pane. Off-Zellij (or with an explicit request and no pane id)
	// run the session inline — there is no pane to dock into.
	inZellij := os.Getenv("ZELLIJ") != "" || paneID != ""
	if cliRequest == "" && inZellij {
		selfExe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: cannot resolve self: %v\n", err)
			return 1
		}
		return launch(m, selfExe, req)
	}

	// Inline path (off-Zellij, or explicit request given): run the session body in
	// the current pane.
	return runSession(req)
}

// launch is the testable launcher core: spawn the request input FLOAT (prefilled
// from the captured context), read back the submitted request, and on submit
// spawn the docked SESSION pane carrying the context. On cancel it exits cleanly
// (0) with no session spawned. selfExe + m are injected so it is unit-testable
// with a fake mux (no live zellij).
func launch(m mux.Mux, selfExe string, req capture.Request) int {
	asker := floatinput.Asker{SelfExe: selfExe, Mux: m}
	res, err := asker.Ask(floatinput.Request{
		Type:   "text",
		Title:  "ai-assist",
		Prompt: "How can I help you today?",
		Value:  prefillTemplate(req),
		Cwd:    req.CWD,
	})
	dbg("launch: Ask returned submitted=%v err=%v value=%q", res.Submitted, err, res.Value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: request float: %v\n", err)
		return 1
	}
	if !res.Submitted {
		// User cancelled the request float — exit cleanly, no session spawned.
		return 0
	}
	req.UserRequest = strings.TrimSpace(res.Value)
	return spawnSession(m, selfExe, req)
}

// spawnSession writes the captured Request to a temp JSON file and opens the
// persistent docked pane running `ai-playbook session --request <json>`. The
// launcher then exits — the docked pane is the session. The temp file is NOT
// removed here (the spawned pane reads it asynchronously and removes it itself).
func spawnSession(m mux.Mux, selfExe string, req capture.Request) int {
	f, err := os.CreateTemp("", "aapb-request-*.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: %v\n", err)
		return 1
	}
	if _, err := f.WriteString(requestJSON(req)); err != nil {
		f.Close()
		os.Remove(f.Name())
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: %v\n", err)
		return 1
	}
	f.Close()

	cwd := req.ProjectRoot
	if cwd == "" {
		cwd = req.CWD
	}
	sessionCmd := []string{selfExe, "session", "--request", f.Name()}
	if dbgPath != "" {
		// Carry the debug-log path into the spawned pane explicitly — the pane
		// inherits the zellij server's env, not ours, so AI_ASSIST_DEBUG_LOG may
		// not reach it.
		sessionCmd = append(sessionCmd, "--debug-log", dbgPath)
	}
	dbg("spawnSession: cwd=%q jsonPath=%q cmd=%q", cwd, f.Name(), sessionCmd)
	if err := m.SpawnDocked(mux.SpawnOptions{
		Cmd:  sessionCmd,
		Cwd:  cwd,
		Name: "ai-assist",
	}); err != nil {
		dbg("spawnSession: SpawnDocked FAILED err=%v", err)
		os.Remove(f.Name())
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: spawn session pane: %v\n", err)
		return 1
	}
	dbg("spawnSession: SpawnDocked OK")
	return 0
}

// sessionMain is the `ai-playbook session` subcommand: the persistent docked
// pane. It reads the captured Request from --request <json> (written by the
// launcher) and runs the session body. A missing/empty --request falls back to
// capturing in-process (so `ai-playbook session` is also usable standalone).
func sessionMain() int {
	fs := flag.NewFlagSet("session", flag.ExitOnError)
	var requestPath, debugLog string
	fs.StringVar(&requestPath, "request", "", "path to the captured request JSON (written by the launcher)")
	fs.StringVar(&debugLog, "debug-log", "", "append a debug trace to this file (set by the launcher)")
	fs.Parse(os.Args[2:])
	if debugLog == "" {
		debugLog = os.Getenv("AI_ASSIST_DEBUG_LOG")
	}
	dbgInit(debugLog)
	ui.SetDebugLog(debugLog) // the ui pkg traces too; the pane got --debug-log as a flag (env dropped)
	dbg("session: start requestPath=%q", requestPath)

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
			Mux:         mux.Load(),
			Atuin:       capture.NewAtuin(),
			PaneID:      paneID,
			UserRequest: os.Getenv("AI_ASSIST_USER_REQUEST"),
		})
	}
	return runSession(req)
}

// runSession is the session BODY (was the inline troubleshoot): route the request
// (triage); on a cache HIT render + drive the cached playbook via the in-process
// `run` path; on a MISS author a fresh playbook with the capable agent, stream it
// into the same render+drive path, and cache it on completion. It owns the shared
// driver + tools backend (openSession) so authoring and the run blocks drive the
// SAME live shell.
func runSession(req capture.Request) int {
	dbgEnv("runSession")
	c := cache.Open()
	noCache := os.Getenv("AI_ASSIST_NO_CACHE") != ""
	d := triage.Route(req, c, noCache)
	dbg("runSession: triage outcome=%v noCache=%v", d.Outcome, noCache)

	// Session setup: ONE shared shell driver is created here, at session start, so
	// BOTH authoring (the agent's tools backend) and the ui's run-blocks drive the
	// SAME live shell — the agent diagnoses in the exact environment the playbook's
	// steps will run in. A tools backend is exposed over a temp unix socket; the
	// claude harness reaches it via the MCP adapter (`ai-playbook mcp --socket`).
	// A failed setup degrades to no-tools authoring (sess is nil) — the ui then
	// opens its own driver, the pre-stage-5 behavior.
	sess := openSession(req)
	dbg("runSession: openSession sess!=nil=%v (agent tools %s)", sess != nil,
		map[bool]string{true: "enabled", false: "DISABLED"}[sess != nil])
	if sess != nil {
		defer sess.close()
	}

	switch d.Outcome {
	case triage.Hit:
		dbg("runSession: serving cached playbook")
		return serveCachedPlaybook(d, req, sess)
	default:
		dbg("runSession: authoring playbook (this runs the agent)")
		return authorPlaybook(req, d, c, noCache, sess)
	}
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
// serving it over a unix socket, the socket path, and the path to this binary
// (for the claude --mcp-config). A nil *session means tools setup failed and the
// session runs in the no-agent-tools fallback (the ui opens its own driver).
type session struct {
	drv     *driver.Driver
	srv     *tools.Server
	socket  string
	selfExe string
}

// activityBuffer is the depth of the authoring fan-out's activity channel: enough
// to absorb a brief ui stall without blocking the event pump (sends drop-if-full).
const activityBuffer = 16

// openSession creates the shared driver and starts the tools backend on a temp
// unix socket. The driver's cwd is the request's project root (else its cwd).
// Returns nil on any failure (driver open, socket dir, or Serve) so the caller
// degrades to no-tools authoring rather than aborting.
func openSession(req capture.Request) *session {
	cwd := req.ProjectRoot
	if cwd == "" {
		cwd = req.CWD
	}
	drv, err := driver.Open(driver.Options{Cwd: cwd})
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

	// Ask seam (the ask-FLOAT): when we can resolve our own binary, the agent's
	// `ask` tool spawns `ai-playbook input … --out <tmp>` in a float and returns
	// the user's answer. Without selfExe we can't spawn ourselves, so ask stays the
	// unavailable sentinel (deps.Ask nil).
	var ask tools.AskFunc
	if selfExe != "" {
		asker := floatinput.Asker{SelfExe: selfExe, Mux: mux.Load()}
		ask = asker.Ask
	}

	// The agent's live activity (reasoning + tool calls) is no longer surfaced via
	// the tools backend's OnActivity hook — the normalized agentstream event stream
	// (AuthorEvents → fanOut) now feeds the ui activity line directly. tools.Serve
	// still runs the run/ask/remember execution the agent invokes; we just no longer
	// observe it for DISPLAY.
	srv, err := tools.Serve(socket, tools.Deps{
		Driver:      drv,
		ProjectRoot: req.ProjectRoot,
		Cwd:         cwd,
		Ask:         ask,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: tools.Serve failed (%v); authoring without agent tools\n", err)
		drv.Close()
		os.RemoveAll(dir)
		return nil
	}
	return &session{drv: drv, srv: srv, socket: socket, selfExe: selfExe}
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
// claude agent when the session is up (so the agent diagnoses via the `run` tool
// in the user's real shell), else the plain claude agent (author-as-before). A
// missing selfExe also falls back (we can't point claude's --mcp-config at
// ourselves). The fallback keeps the no-agent-tools path working.
func (s *session) authoringAgent() author.Agent {
	if s == nil || s.selfExe == "" {
		return author.ClaudeAgent
	}
	return author.ClaudeAgentWithMCP(s.selfExe, s.socket)
}

// asker builds the ui.AskFunc that backs the pager's `f` keybind (spec §D): it
// spawns `ai-playbook input … --out` in a float (the same floatinput.Asker the
// agent's `ask` tool uses), opened in cwd, and returns the user's typed adjustment.
// The ui passes a prompt ("What should I change?"); the Request is fixed text type.
// Returns nil when we can't spawn ourselves (no selfExe / nil session) → `f` no-ops.
func (s *session) asker(cwd string) ui.AskFunc {
	if s == nil || s.selfExe == "" {
		return nil
	}
	a := floatinput.Asker{SelfExe: s.selfExe, Mux: mux.Load()}
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
func authorPlaybook(req capture.Request, d triage.Decision, c *cache.Cache, noCache bool, sess *session) int {
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
	reengage := &orchestrator.Reengage{
		Req:         req,
		Agent:       sess.authoringAgent(),
		Events:      buildReengageEvents(req, sess),
		Cache:       c,
		RequestJSON: requestJSON(req),
	}
	if !d.Disabled && !noCache {
		reengage.CtxHash = d.CtxHash
		reengage.ReqHash = d.ReqHash
	}

	var sharedDrv *driver.Driver
	if sess != nil {
		sharedDrv = sess.drv
	}

	// INITIAL authoring runs the OWNED claude stream-json invocation (AuthorEvents):
	// the normalized event stream is fanned into the ui's EXISTING reader-based
	// playbook stream + activity line, so the wait shows the model's live REASONING
	// + tool activity while the playbook still streams. The mcp-config wires the
	// agent's run/ask/remember tools to this session's backend.
	mcpPath, removeMCP := sess.writeMCPConfig()
	cfg, _ := config.Load()
	events, closeFn, err := author.AuthorEvents(req, author.AuthorOptions{
		Cfg:           cfg,
		MCPConfigPath: mcpPath,
	})
	if err != nil {
		// Fallback: the harness binary may be missing or the harness unsupported.
		// Author via the existing text path so authoring still works.
		dbg("authorPlaybook: AuthorEvents failed (%v); falling back to text author path", err)
		removeMCP()
		return authorPlaybookText(req, d, c, noCache, reengage, cwd, sharedDrv)
	}

	// Fan the events into the playbook reader + activity feed; Body() holds the
	// accumulated playbook for the cache once the reader hits EOF.
	reader, activity, fo := agentstream.FanOut(events, closeFn, activityBuffer)
	defer reader.Close()
	defer removeMCP()

	code := ui.RunStream(reader, ui.StreamOptions{
		Harness:  "Claude Code",
		Cwd:      cwd,
		Driver:   sharedDrv,
		Reengage: reengage,
		Activity: activity,
		Asker:    sess.asker(cwd), // `f` proactive amend (spec §D)
	})

	// Cache-store on completion — only when the cache wasn't disabled/bypassed and
	// the keys are valid. The body comes from the fan-out (TextDelta accumulation,
	// or Final's authoritative text). The disabled guard (failure with empty
	// scrollback) and the no-cache bypass both leave the entry unstored.
	body := fo.Body()
	if !d.Disabled && !noCache && d.CtxHash != "" && d.ReqHash != "" && body != "" {
		if _, serr := c.Store(d.CtxHash, d.ReqHash, "playbook", body, nil, requestJSON(req)); serr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: cache store: %v\n", serr)
		}
	}
	return code
}

// buildReengageEvents builds the orchestrator.EventsFunc that re-engagement
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
func buildReengageEvents(req capture.Request, sess *session) orchestrator.EventsFunc {
	return func(kind orchestrator.ReengageKind, base, change string) (<-chan agentstream.Event, func() error, error) {
		// Per-invocation mcp-config so the re-engaged agent reaches the live backend.
		mcpPath, removeMCP := sess.writeMCPConfig()

		var sys, user string
		switch kind {
		case orchestrator.KindReengageFollowup:
			sys = author.FollowupPrompt(req, change) // change carries the failed output for followup
			user = author.BuildUserMessage(req)
		case orchestrator.KindReengageFinalPlaybook:
			// FINAL-PLAYBOOK (stage 2): fresh when base=="" (change = the troubleshoot
			// content to distill), amend when base!="" (fold change into the base).
			sys = author.FinalPlaybookPrompt(req, base, change)
			user = author.BuildUserMessage(req)
		default: // KindReengageRegenerate → the standard authoring prompt + folded KB
			sys = author.SystemPrompt(req, author.KnowledgeBase(kb.Load(req.ProjectRoot)))
			user = author.BuildUserMessage(req)
		}

		cfg, _ := config.Load()
		events, wait, err := author.RunHarnessEvents(sys, user, author.AuthorOptions{
			Cfg:           cfg,
			MCPConfigPath: mcpPath,
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

// authorPlaybookText is the fallback authoring path: it runs the existing
// io.ReadCloser-based author.Author (the text harness invocation) when the owned
// AuthorEvents stream can't start (harness binary missing / unsupported). It tees
// the produced playbook into a buffer for the cache, exactly as before part 2a.
func authorPlaybookText(req capture.Request, d triage.Decision, c *cache.Cache, noCache bool, reengage *orchestrator.Reengage, cwd string, sharedDrv *driver.Driver) int {
	stream, err := author.Author(req, reengage.Agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: author: %v\n", err)
		return 1
	}
	defer stream.Close()

	var body bytes.Buffer
	code := ui.RunStream(stream, ui.StreamOptions{
		Harness:  "Claude Code",
		Cwd:      cwd,
		Tee:      &body,
		Driver:   sharedDrv,
		Reengage: reengage,
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

// serveCachedPlaybook renders the cached entry through the existing in-process
// `run` path. The entry on disk carries YAML front matter; we strip it to the
// body, write it to a temp file, and reuse ui.Main() (which spins up the driver +
// orchestrator and drives the playbook in-process), passing --cached for the
// header badge and --cwd so runs execute in the request's project root.
func serveCachedPlaybook(d triage.Decision, req capture.Request, sess *session) int {
	raw, err := os.ReadFile(d.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: read cache entry: %v\n", err)
		return 1
	}
	content := string(raw)
	body := cache.Body(content)
	created, _ := cache.Field(content, "created_at")

	f, err := os.CreateTemp("", "aapb-cached-*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: %v\n", err)
		return 1
	}
	tmp := f.Name()
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: %v\n", err)
		return 1
	}
	f.Close()
	defer os.Remove(tmp)

	cwd := req.ProjectRoot
	if cwd == "" {
		cwd = req.CWD
	}

	// Re-engagement context for the cached replay (stage 4c-ii): the cached pill's
	// regenerate button (and the w-key wrap-up / verify follow-up) re-author the
	// ORIGINAL request in-process. regenerate re-stores the fresh playbook under the
	// SAME keys so the next identical request hits the refreshed entry — matching
	// ai-assist-regenerate. Stashed for ui.Main to attach to the orchestrator.
	ui.SetReengage(&orchestrator.Reengage{
		Req:         req,
		Agent:       sess.authoringAgent(),
		Events:      buildReengageEvents(req, sess),
		Cache:       cache.Open(),
		CtxHash:     d.CtxHash,
		ReqHash:     d.ReqHash,
		RequestJSON: requestJSON(req),
	})

	// Reuse the session's shared driver for the cached replay's run blocks (the
	// same shell the re-engagement agent's tools backend drives), stashed for
	// ui.Main to consume. nil session → ui.Main opens its own driver. Re-engagement
	// during the cached replay now streams the model's live reasoning + tool activity
	// via Reengage.Events (part 2b), with the text Agent as the fallback.
	if sess != nil {
		ui.SetDriver(sess.drv)
	}

	// Stage 4 (spec §C amend-on-rerun): this is a cache HIT — we are SERVING an
	// existing playbook for this context. Stash its body as the served base so a
	// failing step → troubleshoot → confirm/`w`-generate AMENDS this playbook
	// (base=servedBase) instead of starting fresh, and the improved version is
	// re-cached under the SAME CtxHash/ReqHash (populated on the Reengage above) —
	// the served entry is overwritten, never lost. Amend-vs-fresh is naturally scoped
	// by the cache key: a same-context failure serves+amends this entry; a different
	// context is a different cache entry → a cache MISS → authorPlaybook (servedBase
	// stays "" → fresh). The base is the INPUT to the amend; the output is base+fix.
	ui.SetServedBase(body)

	// Stage 5 (spec §D): stash the request-input-float asker so the served playbook's
	// `f` keybind proactively amends it (base = the displayed content, change = the
	// user's typed adjustment) → REPLACE draft → `w` to re-cache. nil session / no
	// selfExe → nil → `f` no-ops.
	ui.SetAsker(sess.asker(cwd))

	// Reuse the `run` subcommand entrypoint in-process by shaping os.Args the way
	// ui.Main() parses them (os.Args[1]="run", flags from os.Args[2:]).
	argv := []string{os.Args[0], "run"}
	if created != "" {
		argv = append(argv, "--cached", created)
	}
	if cwd != "" {
		argv = append(argv, "--cwd", cwd)
	}
	argv = append(argv, tmp)
	os.Args = argv
	return ui.Main()
}

func dirExists(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
func head(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}
