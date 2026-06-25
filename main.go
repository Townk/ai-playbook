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

	"ai-playbook/author"
	"ai-playbook/cache"
	"ai-playbook/capture"
	"ai-playbook/driver"
	"ai-playbook/floatinput"
	"ai-playbook/input"
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
	if err := m.SpawnDocked(mux.SpawnOptions{
		Cmd:  []string{selfExe, "session", "--request", f.Name()},
		Cwd:  cwd,
		Name: "ai-assist",
	}); err != nil {
		os.Remove(f.Name())
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: spawn session pane: %v\n", err)
		return 1
	}
	return 0
}

// sessionMain is the `ai-playbook session` subcommand: the persistent docked
// pane. It reads the captured Request from --request <json> (written by the
// launcher) and runs the session body. A missing/empty --request falls back to
// capturing in-process (so `ai-playbook session` is also usable standalone).
func sessionMain() int {
	fs := flag.NewFlagSet("session", flag.ExitOnError)
	var requestPath string
	fs.StringVar(&requestPath, "request", "", "path to the captured request JSON (written by the launcher)")
	fs.Parse(os.Args[2:])

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
	c := cache.Open()
	noCache := os.Getenv("AI_ASSIST_NO_CACHE") != ""
	d := triage.Route(req, c, noCache)

	// Session setup: ONE shared shell driver is created here, at session start, so
	// BOTH authoring (the agent's tools backend) and the ui's run-blocks drive the
	// SAME live shell — the agent diagnoses in the exact environment the playbook's
	// steps will run in. A tools backend is exposed over a temp unix socket; the
	// claude harness reaches it via the MCP adapter (`ai-playbook mcp --socket`).
	// A failed setup degrades to no-tools authoring (sess is nil) — the ui then
	// opens its own driver, the pre-stage-5 behavior.
	sess := openSession(req)
	if sess != nil {
		defer sess.close()
	}

	switch d.Outcome {
	case triage.Hit:
		return serveCachedPlaybook(d, req, sess)
	default:
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
	agent := sess.authoringAgent()
	stream, err := author.Author(req, agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: author: %v\n", err)
		return 1
	}
	defer stream.Close()

	cwd := req.ProjectRoot
	if cwd == "" {
		cwd = req.CWD
	}

	// Re-engagement context (stage 4c-ii): the in-process regenerate / followup /
	// wrapup kinds re-invoke the author. regenerate re-stores the fresh playbook
	// (cache + keys), so it gets them; followup/wrapup only need the request +
	// agent. When the cache is disabled/bypassed the keys are empty and regenerate
	// authors-without-re-storing (matching the shell's cache-bypassed re-run).
	reengage := &orchestrator.Reengage{
		Req:         req,
		Agent:       agent,
		Cache:       c,
		RequestJSON: requestJSON(req),
	}
	if !d.Disabled && !noCache {
		reengage.CtxHash = d.CtxHash
		reengage.ReqHash = d.ReqHash
	}

	// Tee the produced playbook into a buffer as the ui consumes it, so we can
	// persist it on completion. The ui reuses the SESSION's shared driver (the same
	// shell the agent diagnosed in via its tools backend) when one is up; else it
	// opens its own.
	var sharedDrv *driver.Driver
	if sess != nil {
		sharedDrv = sess.drv
	}
	var body bytes.Buffer
	code := ui.RunStream(stream, ui.StreamOptions{
		Harness:  "Claude Code",
		Cwd:      cwd,
		Tee:      &body,
		Driver:   sharedDrv,
		Reengage: reengage,
	})

	// Cache-store on completion — only when the cache wasn't disabled/bypassed and
	// the keys are valid. The disabled guard (failure with empty scrollback) and
	// the no-cache bypass both leave the entry unstored, matching the shell.
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
		Cache:       cache.Open(),
		CtxHash:     d.CtxHash,
		ReqHash:     d.ReqHash,
		RequestJSON: requestJSON(req),
	})

	// Reuse the session's shared driver for the cached replay's run blocks (the
	// same shell the re-engagement agent's tools backend drives), stashed for
	// ui.Main to consume. nil session → ui.Main opens its own driver.
	if sess != nil {
		ui.SetDriver(sess.drv)
	}

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
