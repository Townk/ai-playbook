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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-playbook/internal/agentstream"
	"ai-playbook/internal/author"
	"ai-playbook/internal/cache"
	"ai-playbook/internal/capture"
	"ai-playbook/internal/config"
	"ai-playbook/internal/driver"
	"ai-playbook/internal/floatinput"
	"ai-playbook/internal/frontmatter"
	"ai-playbook/internal/input"
	"ai-playbook/internal/kb"
	"ai-playbook/internal/mcpserver"
	"ai-playbook/internal/mux"
	"ai-playbook/internal/orchestrator"
	"ai-playbook/internal/tools"
	"ai-playbook/internal/triage"
	"ai-playbook/internal/ui"

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
	case "answer":
		os.Exit(answerMain())
	case "finalize":
		os.Exit(finalize())
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
	fmt.Fprintln(os.Stderr, "usage: ai-playbook {troubleshoot|session [--request <json>]|run <file.md>|answer --request <json> --content <file> [--cached <iso>] [--title <t>] [--cwd <dir>]|finalize [--dry-run] <file.md>|mcp --socket <path>|input|selftest}")
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
// $AI_PLAYBOOK_USER_REQUEST) SKIPS the float — the request is already known. Off a
// mux (no zellij) there is no float/pane to spawn; the launcher runs the session
// INLINE in the current pane (the pre-topology behavior), so headless and SSH
// contexts still work.
func troubleshoot() int {
	dbgInit(os.Getenv("AI_PLAYBOOK_DEBUG_LOG"))
	cliRequest := strings.TrimSpace(strings.Join(os.Args[2:], " "))
	if cliRequest == "" {
		cliRequest = os.Getenv("AI_PLAYBOOK_USER_REQUEST")
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
		return launch(m, selfExe, req, author.ClassifyRequest)
	}

	// Inline path (off-Zellij, or explicit request given): classify + route in the
	// current pane (command → print it, answer → print prose, escalate → session).
	return runInline(req)
}

// requestHistoryPath is the JSONL request-history file for the troubleshoot
// request float: <data-root>/request-history.jsonl. Only the request float wires
// this (the ask tool + the `f` amend float pass no history per the spec scope).
func requestHistoryPath() string {
	return filepath.Join(cache.DefaultRoot(), "request-history.jsonl")
}

// classifyFunc is the launcher's classify seam (stage C): the cheap-model triage
// pass that routes a submitted request to command / answer / escalate. It defaults
// to author.ClassifyRequest; tests inject a fake returning each kind.
type classifyFunc func(req capture.Request, opts author.AuthorOptions) (author.Classification, error)

// launch is the testable launcher core (stage C). It spawns the request input
// FLOAT (prefilled from the captured context) in --thinking mode, reads back the
// submitted request, then CLASSIFIES it (cheap triage model) and routes three ways:
//
//   - command  → the single shell command is typed into the ORIGIN pane (no CR;
//     the user reviews + presses Enter). NO docked/floating pane.
//   - answer   → a short prose answer is rendered in a docked pager (`run <md>`).
//   - escalate → the full docked SESSION pane (the current author/serve flow).
//
// The float STAYS OPEN animating "Thinking…" during the classify; once classified
// the launcher writes <out>.done to close it and WAITS for the float to fully tear
// down (waitFloatClosed) BEFORE routing — so the result pane spawns with the origin
// tiled pane focused, not the floating thinking pane (else `new-pane` would open the
// "docked" pane floating behind the float). On cancel it exits cleanly (0) with
// nothing written and no route taken. A classify error degrades to escalate (never
// block the user). selfExe + m + classify are injected so it is unit-testable with a
// fake mux + fake classify (no live zellij, no live model).
func launch(m mux.Mux, selfExe string, req capture.Request, classify classifyFunc) int {
	if classify == nil {
		classify = author.ClassifyRequest
	}
	asker := floatinput.Asker{SelfExe: selfExe, Mux: m}
	res, out, err := asker.AskThinking(floatinput.Request{
		Type:    "text",
		Title:   "ai-playbook",
		Prompt:  "How can I help you today?",
		Value:   prefillTemplate(req),
		Cwd:     req.CWD,
		History: requestHistoryPath(),
	})
	dbg("launch: AskThinking returned submitted=%v err=%v value=%q out=%q", res.Submitted, err, res.Value, out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: request float: %v\n", err)
		return 1
	}
	if !res.Submitted {
		// User cancelled the request float — it writes its own <out>.cancel and
		// exits itself, so we write NO .done and take no route. Exit cleanly.
		return 0
	}
	req.UserRequest = strings.TrimSpace(res.Value)

	// CACHE-BY-KIND: a repeat request (same context + request) is served straight
	// from the cache, skipping the cheap classify ENTIRELY. triage.Route computes
	// the same (ctxHash, reqHash) keys the session uses and does the lookup; on a
	// hit we route by the stored `kind` with no model call. AI_PLAYBOOK_NO_CACHE
	// bypasses the lookup (matches runSession; the env rename is a separate task).
	c := cache.Open()
	noCache := os.Getenv("AI_PLAYBOOK_NO_CACHE") != ""
	d := triage.Route(req, c, noCache)
	dbg("launch: triage outcome=%v noCache=%v disabled=%v", d.Outcome, noCache, d.Disabled)

	if d.Outcome == triage.Hit {
		if raw, rerr := os.ReadFile(d.Path); rerr == nil {
			content := string(raw)
			kind, _ := cache.Field(content, "kind")
			body := cache.Body(content)
			title, _ := cache.Field(content, "title")
			created, _ := cache.Field(content, "created_at")
			dbg("launch: cache HIT path=%q kind=%q bodyLen=%d", d.Path, kind, len(body))
			// Short-circuit ONLY the cheap, launcher-owned kinds (command/answer). A
			// cached PLAYBOOK is deliberately NOT served straight from here: the SAME
			// request classifies differently across contexts (command in one, escalate
			// in another — both observed in the on-disk cache), so serving a frozen
			// playbook pops a side pane for a prompt the user now expects as a command.
			// For a playbook/unknown hit, fall through to the classify (it re-decides the
			// kind); if it's still escalate, the session re-runs triage.Route and serves
			// THIS cached playbook (no re-author). The float is closed here only on a
			// short-circuit; the fall-through lets the classify path close it as usual.
			switch kind {
			case "command":
				closeFloat(out)
				dbg("launch: hit route=command pane=%q bodyLen=%d", req.PaneID, len(body))
				if terr := m.TypeInto(req.PaneID, body); terr != nil {
					dbg("launch: TypeInto origin pane failed: %v", terr)
				}
				return 0
			case "answer":
				closeFloat(out)
				dbg("launch: hit route=answer")
				return spawnAnswer(m, selfExe, req, body, title, created)
			default: // playbook / unknown → re-classify (don't serve a frozen playbook)
				dbg("launch: hit kind=%q not short-circuited; re-classifying", kind)
			}
		} else {
			dbg("launch: cache HIT but path %q unreadable; falling through to classify", d.Path)
		}
	}

	// CACHE MISS / disabled / no-cache: CLASSIFY the submitted request on the cheap
	// triage model. Any error routes to escalate (the safe default — never block the
	// user on a classify failure).
	cfg, _ := config.Load()
	cls, cerr := classify(req, author.AuthorOptions{Cfg: cfg, OnText: newThinkingWriter(out)})
	if cerr != nil {
		dbg("launch: classify failed (%v); escalating", cerr)
		cls = author.Classification{Kind: author.KindEscalate}
	}
	dbg("launch: classify kind=%q contentLen=%d", cls.Kind, len(cls.Content))

	// CLOSE the thinking float BEFORE routing. The float, animating, polls for
	// <out>.done and exits; writing it now (not after the route) means the spawn
	// runs with the ORIGIN tiled pane focused — not the floating thinking pane. If
	// we spawned first, `zellij action new-pane` would inherit the focused float's
	// FLOATING context and the "docked" result pane would open floating behind the
	// float (the confirmed bug). waitFloatClosed then blocks until the float has
	// fully torn down (it writes <out>.closed on thinking-exit) plus a short margin
	// for zellij to drop the floating pane and restore focus to the origin.
	closeFloat(out)

	// Only command/answer classifications are cacheable here; escalate is stored by
	// the session itself (the `playbook` entry). The disabled guard (failure with
	// empty scrollback) and the no-cache bypass both leave the entry unstored.
	cacheable := !d.Disabled && !noCache && d.CtxHash != "" && d.ReqHash != ""

	switch cls.Kind {
	case author.KindCommand:
		// Store the classified command so the next identical request hits (best-effort:
		// a store error is logged, never fatal; store BEFORE the route regardless).
		if cacheable {
			if _, serr := c.Store(d.CtxHash, d.ReqHash, "command", cls.Content, nil, requestJSON(req)); serr != nil {
				dbg("launch: cache store (command) failed: %v", serr)
			}
		}
		// Stage the command into the ORIGIN pane with NO trailing CR (mux.TypeInto →
		// `zellij action write-chars --pane-id <pane>`), so it lands at the prompt for
		// the user to review and run. The explicit pane id makes the write
		// focus-independent. No docked/floating pane is opened.
		dbg("launch: route=command pane=%q contentLen=%d", req.PaneID, len(cls.Content))
		if terr := m.TypeInto(req.PaneID, cls.Content); terr != nil {
			dbg("launch: TypeInto origin pane failed: %v", terr)
		}
		return 0
	case author.KindAnswer:
		// Store the classified answer (carrying the title extra, when present) so the
		// next identical request hits. Best-effort; store BEFORE the route.
		if cacheable {
			var extras map[string]string
			if cls.Title != "" {
				extras = map[string]string{"title": cls.Title}
			}
			if _, serr := c.Store(d.CtxHash, d.ReqHash, "answer", cls.Content, extras, requestJSON(req)); serr != nil {
				dbg("launch: cache store (answer) failed: %v", serr)
			}
		}
		// Render the short prose answer in a docked pager (no run blocks → just prose).
		// A freshly-classified answer is not cached-served, so no --cached badge.
		dbg("launch: route=answer")
		return spawnAnswer(m, selfExe, req, cls.Content, cls.Title, "")
	default: // escalate (incl. empty/unknown kind) — the session writes the playbook entry
		dbg("launch: route=escalate kind=%q", cls.Kind)
		return spawnSession(m, selfExe, req, cls.Title)
	}
}

// closeFloat tears the thinking float down BEFORE routing: it writes <out>.done
// (the float polls for it and exits) then waits for the float to fully close
// (<out>.closed) plus a margin, so zellij has restored focus to the origin tiled
// pane and the result pane docks instead of opening floating behind the float.
// Factored so BOTH the cache-hit and cache-miss paths close the float identically.
// A no-op on an empty out path (the inline/off-zellij path has no float).
func closeFloat(out string) {
	writeDoneFile(out)
	waitFloatClosed(out)
}

// floatClosePoll / floatCloseCap / floatCloseMargin tune waitFloatClosed: poll
// every floatClosePoll for the float's <out>.closed marker up to floatCloseCap,
// then sleep floatCloseMargin so zellij finishes dropping the floating pane and
// restores focus to the origin tiled pane before the route spawns. Package-level
// vars so tests shrink them (the live values give the float ~2s to tear down).
var (
	floatClosePoll   = 25 * time.Millisecond
	floatCloseCap    = 2 * time.Second
	floatCloseMargin = 150 * time.Millisecond
)

// waitFloatClosed blocks until the thinking float has fully torn down — it polls
// for the float's <out>.closed marker (written on thinking-exit, after the tea
// program returns) every floatClosePoll up to floatCloseCap. Once seen (or on the
// cap), it sleeps floatCloseMargin so zellij has dropped the floating pane and
// returned focus to the origin tiled pane BEFORE the caller spawns the result
// pane (so `new-pane` docks instead of inheriting the float's floating context).
// A no-op (no poll, no margin) on an empty path — the inline/off-zellij path has
// no float.
func waitFloatClosed(out string) {
	if out == "" {
		return
	}
	marker := out + input.ClosedSuffix
	deadline := time.Now().Add(floatCloseCap)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			dbg("waitFloatClosed: float closed (marker present)")
			break
		}
		time.Sleep(floatClosePoll)
	}
	time.Sleep(floatCloseMargin)
}

// writeDoneFile writes the thinking float's <out>.done close marker (an empty file,
// atomic enough). The float, while animating, polls for it (input.DoneSuffix) and
// exits when it appears. Mirrors input.writeCancelFile. A no-op on an empty path.
func writeDoneFile(outFile string) {
	if outFile == "" {
		return
	}
	_ = os.WriteFile(outFile+input.DoneSuffix, nil, 0o600)
}

// thinkingTailRunes bounds the sliding-tail window written to <out>.thinking — the
// float renders ONE line under the box, so only the most recent runes matter.
const thinkingTailRunes = 200

// thinkingWriteEvery throttles how often the classify stream's onDelta rewrites
// <out>.thinking, so a chatty token stream doesn't hammer the FS. A package var so
// tests can adjust it.
var thinkingWriteEvery = 60 * time.Millisecond

// thinkingTail collapses every whitespace run in s to a single space and returns
// the LAST maxRunes runes (a sliding tail window), so the float's one-line thinking
// display shows the most recent model output without growing unbounded. Rune-safe:
// the cut lands on a rune boundary, never mid-rune.
func thinkingTail(s string, maxRunes int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	r := []rune(collapsed)
	if maxRunes >= 0 && len(r) > maxRunes {
		r = r[len(r)-maxRunes:]
	}
	return string(r)
}

// writeThinkingFile atomically (temp+rename) writes line to <out>.thinking, which
// the thinking float polls into its dark-grey line. Best-effort: a write error is
// swallowed (the live line is cosmetic; it must never block the classify).
func writeThinkingFile(out, line string) {
	if out == "" {
		return
	}
	path := out + input.ThinkingSuffix
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(line), 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

// newThinkingWriter returns an OnText callback that writes a single-line,
// whitespace-collapsed sliding tail (≤ thinkingTailRunes) of the accumulated
// classify model output to <out>.thinking, throttled to at most one write per
// thinkingWriteEvery (the first call always writes). A no-op on an empty out path
// (the inline/off-zellij path has no float). Runs while the float is still up.
func newThinkingWriter(out string) func(string) {
	if out == "" {
		return nil
	}
	var last time.Time
	return func(accumulated string) {
		now := time.Now()
		if !last.IsZero() && now.Sub(last) < thinkingWriteEvery {
			return
		}
		last = now
		// Show only the classify JSON's "content" value as it forms (the answer /
		// command text), not the raw {"kind":…,"content":"…} envelope.
		writeThinkingFile(out, thinkingTail(extractJSONContent(accumulated), thinkingTailRunes))
	}
}

// extractJSONContent returns the (possibly still-streaming) value of the "content"
// field from a partial JSON object, decoding JSON string escapes best-effort. It
// returns "" until the content value has begun — so the thinking line shows just the
// answer/command text forming, not the JSON envelope. Robust to truncated input (a
// dangling escape or incomplete \u at the stream tail stops cleanly).
func extractJSONContent(s string) string {
	k := strings.Index(s, `"content"`)
	if k < 0 {
		return ""
	}
	rest := s[k+len(`"content"`):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return ""
	}
	rest = rest[colon+1:]
	open := strings.IndexByte(rest, '"')
	if open < 0 {
		return ""
	}
	rest = rest[open+1:]
	var b strings.Builder
	for j := 0; j < len(rest); j++ {
		c := rest[j]
		if c == '"' {
			break // closing quote of the value
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if j+1 >= len(rest) {
			break // dangling escape at the stream tail
		}
		j++
		switch rest[j] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '"', '\\', '/':
			b.WriteByte(rest[j])
		case 'u':
			if j+4 < len(rest) {
				if v, err := strconv.ParseUint(rest[j+1:j+5], 16, 32); err == nil {
					b.WriteRune(rune(v))
				}
				j += 4
			} else {
				j = len(rest) // incomplete \uXXXX at the tail
			}
		default:
			b.WriteByte(rest[j])
		}
	}
	return b.String()
}

// spawnAnswer renders a SHORT prose answer (the classify "answer" route): write the
// content to a temp markdown file and open it in a docked pager via `ai-playbook
// answer --request <json> --content <answer.md> …`. The `answer` subcommand renders
// the prose (no run blocks, no authoring loop) AND wires the cached pill's reload to
// re-run the cheap classify in place (re-caching the fresh prose) — so the request
// JSON travels with the pane. The temp file is read asynchronously by the spawned
// pane, so it is NOT removed here (mirrors spawnSession's request-JSON hand-off).
// Both launcher answer routes (cache HIT and MISS) go through here; a freshly
// classified answer has no --cached, so its badge only appears once re-cached.
func spawnAnswer(m mux.Mux, selfExe string, req capture.Request, content, title, created string) int {
	f, err := os.CreateTemp("", "aapb-answer-*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: %v\n", err)
		return 1
	}
	if _, err := f.WriteString(content); err != nil {
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
	runCmd := []string{selfExe, "answer", "--request", requestJSON(req), "--content", f.Name()}
	if title != "" {
		// The classify-supplied short label becomes the pager header (overrides the
		// H1/front-matter title, which a prose answer has none of).
		runCmd = append(runCmd, "--title", title)
	}
	if created != "" {
		// A cached-served answer carries the entry's created_at so the pager shows the
		// "cached Nm ago" badge pill (forwarded to the `run` entry's --cached <iso>).
		runCmd = append(runCmd, "--cached", created)
	}
	if cwd != "" {
		runCmd = append(runCmd, "--cwd", cwd)
	}
	dbg("spawnAnswer: cwd=%q answerPath=%q cmd=%q", cwd, f.Name(), runCmd)
	if err := m.SpawnDocked(mux.SpawnOptions{
		Cmd:  runCmd,
		Cwd:  cwd,
		Name: "ai-playbook",
	}); err != nil {
		dbg("spawnAnswer: SpawnDocked FAILED err=%v", err)
		os.Remove(f.Name())
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: spawn answer pane: %v\n", err)
		return 1
	}
	return 0
}

// answerClassify is the cached-answer regenerate seam: the cheap-model triage pass
// the reload re-runs to refresh the prose. It defaults to author.ClassifyRequest;
// tests inject a fake (the closure calls the live model otherwise, which a unit test
// can't drive). Mirrors launch's classifyFunc seam.
var answerClassify classifyFunc = author.ClassifyRequest

// answerRegenFunc builds the cached-ANSWER regenerate closure handed to
// ui.SetAnswerRegen. When the reload pill is clicked it re-runs the cheap classify
// on the ORIGINAL request, re-caches the fresh prose under the SAME (ctx,req) keys
// with kind=answer (best-effort), and streams the prose back so the pager REPLACES
// the stale content. The classify's returned Kind is ignored — this pane is prose,
// and a kind change is a rare edge; we just show the refreshed content.
func answerRegenFunc(req capture.Request) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		cfg, _ := config.Load()
		cls, err := answerClassify(req, author.AuthorOptions{Cfg: cfg})
		if err != nil {
			return nil, err
		}
		content := cls.Content
		// Re-cache the refreshed prose under the original keys (best-effort — a store
		// error must never block showing the fresh answer).
		c := cache.Open()
		ctxH := cache.ContextHash(cache.Request{
			ProjectRoot: req.ProjectRoot,
			CWD:         req.CWD,
			CommandText: req.Command,
			CommandExit: req.Exit,
			Scrollback:  req.Scrollback,
		})
		reqH := cache.RequestHash(req.UserRequest)
		var extras map[string]string
		if cls.Title != "" {
			extras = map[string]string{"title": cls.Title}
		}
		if _, serr := c.Store(ctxH, reqH, "answer", content, extras, requestJSON(req)); serr != nil {
			dbg("answerRegen: re-cache failed: %v", serr)
		}
		return io.NopCloser(strings.NewReader(content)), nil
	}
}

// answerMain is the `ai-playbook answer` subcommand: the docked prose pager for a
// classify "answer" route (spawned by spawnAnswer). It carries the original request
// so the cached pill's reload can re-run the cheap classify in place. It decodes
// --request <json>, reads the prose from --content <file>, wires the cached-answer
// regenerate seam (ui.SetAnswerRegen), then reshapes os.Args to the `run` entry and
// returns ui.Main() — exactly like serveCachedPlaybook reshapes to `run`.
func answerMain() int {
	fs := flag.NewFlagSet("answer", flag.ExitOnError)
	var requestJSONStr string
	fs.StringVar(&requestJSONStr, "request", "", "the capture.Request as JSON (for the reload re-classify)")
	var contentFile string
	fs.StringVar(&contentFile, "content", "", "path to the prose markdown to render")
	var cached string
	fs.StringVar(&cached, "cached", "", "ISO-8601 timestamp: show the 'cached' badge (cache replay)")
	var title string
	fs.StringVar(&title, "title", "", "pager header title")
	var cwd string
	fs.StringVar(&cwd, "cwd", "", "working dir for the pager")
	fs.Parse(os.Args[2:])

	if contentFile == "" {
		fmt.Fprintln(os.Stderr, "ai-playbook answer: --content <file> is required")
		return 2
	}

	// Decode the request so the reload can re-classify it. A decode failure is
	// non-fatal: the pager still renders the prose; only the reload seam is skipped.
	if requestJSONStr != "" {
		if req, err := decodeRequestJSON([]byte(requestJSONStr)); err != nil {
			dbg("answerMain: request decode failed: %v", err)
		} else {
			ui.SetAnswerRegen(answerRegenFunc(req))
		}
	}

	// Reshape os.Args to the `run` entrypoint (os.Args[1]="run", flags from [2:]),
	// exactly like serveCachedPlaybook, and reuse ui.Main().
	argv := []string{os.Args[0], "run"}
	if cached != "" {
		argv = append(argv, "--cached", cached)
	}
	if title != "" {
		argv = append(argv, "--title", title)
	}
	if cwd != "" {
		argv = append(argv, "--cwd", cwd)
	}
	argv = append(argv, contentFile)
	os.Args = argv
	return ui.Main()
}

// runInline is the off-Zellij / explicit-request path (no float, no panes): classify
// the request and route it simply (stage C). command → print the command for the
// user to run; answer → print the prose; escalate → run the session inline (current
// behavior). A classify error escalates (the safe default).
func runInline(req capture.Request) int {
	cfg, _ := config.Load()
	cls, err := author.ClassifyRequest(req, author.AuthorOptions{Cfg: cfg})
	if err != nil {
		dbg("runInline: classify failed (%v); escalating", err)
		cls = author.Classification{Kind: author.KindEscalate}
	}
	switch cls.Kind {
	case author.KindCommand:
		fmt.Printf("Suggested command (review, then run it yourself):\n\n%s\n", cls.Content)
		return 0
	case author.KindAnswer:
		fmt.Println(cls.Content)
		return 0
	default:
		return runSession(req, "")
	}
}

// spawnSession writes the captured Request to a temp JSON file and opens the
// persistent docked pane running `ai-playbook session --request <json>`. The
// launcher then exits — the docked pane is the session. The temp file is NOT
// removed here (the spawned pane reads it asynchronously and removes it itself).
func spawnSession(m mux.Mux, selfExe string, req capture.Request, title string) int {
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
	if title != "" {
		// The classify-supplied short label becomes the docked session's working
		// header (the pager seeds m.title; a finalized-playbook H1 may update it later).
		sessionCmd = append(sessionCmd, "--title", title)
	}
	if dbgPath != "" {
		// Carry the debug-log path into the spawned pane explicitly — the pane
		// inherits the zellij server's env, not ours, so AI_PLAYBOOK_DEBUG_LOG may
		// not reach it.
		sessionCmd = append(sessionCmd, "--debug-log", dbgPath)
	}
	dbg("spawnSession: cwd=%q jsonPath=%q cmd=%q", cwd, f.Name(), sessionCmd)
	if err := m.SpawnDocked(mux.SpawnOptions{
		Cmd:  sessionCmd,
		Cwd:  cwd,
		Name: "ai-playbook",
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
	var requestPath, debugLog, titleFlag string
	fs.StringVar(&requestPath, "request", "", "path to the captured request JSON (written by the launcher)")
	fs.StringVar(&debugLog, "debug-log", "", "append a debug trace to this file (set by the launcher)")
	fs.StringVar(&titleFlag, "title", "", "working pane-header title (the classify-supplied label)")
	fs.Parse(os.Args[2:])
	if debugLog == "" {
		debugLog = os.Getenv("AI_PLAYBOOK_DEBUG_LOG")
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
			UserRequest: os.Getenv("AI_PLAYBOOK_USER_REQUEST"),
		})
	}
	return runSession(req, titleFlag)
}

// runSession is the session BODY (was the inline troubleshoot): route the request
// (triage); on a cache HIT render + drive the cached playbook via the in-process
// `run` path; on a MISS author a fresh playbook with the capable agent, stream it
// into the same render+drive path, and cache it on completion. It owns the shared
// driver + tools backend (openSession) so authoring and the run blocks drive the
// SAME live shell.
func runSession(req capture.Request, title string) int {
	dbgEnv("runSession")
	c := cache.Open()
	noCache := os.Getenv("AI_PLAYBOOK_NO_CACHE") != ""
	d := triage.Route(req, c, noCache)
	dbg("runSession: triage outcome=%v noCache=%v", d.Outcome, noCache)

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
	sessCh := openSessionAsync(req)

	switch d.Outcome {
	case triage.Hit:
		dbg("runSession: serving cached playbook")
		// serveCachedPlaybook OWNS the session: it renders instantly, waits for the
		// background open, and closes the session after ui.Main returns.
		return serveCachedPlaybook(d, req, sessCh, title)
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
// ui.Main via the done latch), so there's no leak.
func openSessionAsync(req capture.Request) <-chan *session {
	ch := make(chan *session, 1)
	go func() { ch <- openSession(req) }()
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

	reengage := &orchestrator.Reengage{
		Req:         req,
		Agent:       sess.authoringAgent(),
		Events:      buildReengageEvents(req, sess),
		Cache:       c,
		RequestJSON: requestJSON(req),
		Metadata:    buildMetadataSeam(sess),
		EnvLookup:   buildEnvLookup(sharedDrv),
	}
	if !d.Disabled && !noCache {
		reengage.CtxHash = d.CtxHash
		reengage.ReqHash = d.ReqHash
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
		return authorPlaybookText(req, d, c, noCache, reengage, cwd, sharedDrv, title)
	}

	// Fan the events into the playbook reader + activity feed; Body() holds the
	// accumulated playbook for the cache once the reader hits EOF.
	reader, activity, fo := agentstream.FanOut(events, closeFn, activityBuffer)
	defer reader.Close()
	defer removeMCP()

	code := ui.RunStream(reader, ui.StreamOptions{
		Harness:  "Claude Code",
		Title:    title,
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

// buildMetadataSeam builds the orchestrator.Reengage.Metadata seam (spec §B):
// CommitPlaybook calls it to classify the FINISHED playbook into description /
// category / tags + per-var rationales. It lives in main (which imports author) so
// the orchestrator stays free of an author import on the commit path. The mapping
// flattens author.Metadata → orchestrator.PlaybookMeta, building EnvNotes
// (name → why) from ImportantEnvVars. A classification failure is returned as an
// error; CommitPlaybook then persists with empty model fields (never fails the
// commit).
func buildMetadataSeam(sess *session) func(doc string) (orchestrator.PlaybookMeta, error) {
	return func(doc string) (orchestrator.PlaybookMeta, error) {
		cfg, _ := config.Load()
		meta, err := author.PlaybookMetadata(doc, author.AuthorOptions{Cfg: cfg})
		if err != nil {
			// Non-fatal: CommitPlaybook persists a metadata-less front matter (name +
			// env + provenance) rather than failing the commit. Log so a classifier
			// outage is visible instead of silently dropping description/tags/category.
			dbg("playbook metadata classification failed; persisting without model fields: %v", err)
			return orchestrator.PlaybookMeta{}, err
		}
		notes := make(map[string]string, len(meta.ImportantEnvVars))
		for _, ev := range meta.ImportantEnvVars {
			if ev.Name != "" {
				notes[ev.Name] = ev.Why
			}
		}
		return orchestrator.PlaybookMeta{
			Description: meta.Description,
			Category:    meta.Category,
			Tags:        meta.Tags,
			EnvNotes:    notes,
		}, nil
	}
}

// buildEnvLookup builds the orchestrator.Reengage.EnvLookup seam (spec §C): the
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
		res := d.Run("env", defaultEnvDumpTimeout)
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

// defaultEnvDumpTimeout bounds the one-shot driver `env` dump for the EnvLookup seam.
const defaultEnvDumpTimeout = 10 * time.Second

// authorPlaybookText is the fallback authoring path: it runs the existing
// io.ReadCloser-based author.Author (the text harness invocation) when the owned
// AuthorEvents stream can't start (harness binary missing / unsupported). It tees
// the produced playbook into a buffer for the cache, exactly as before part 2a.
func authorPlaybookText(req capture.Request, d triage.Decision, c *cache.Cache, noCache bool, reengage *orchestrator.Reengage, cwd string, sharedDrv *driver.Driver, title string) int {
	stream, err := author.Author(req, reengage.Agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: author: %v\n", err)
		return 1
	}
	defer stream.Close()

	var body bytes.Buffer
	code := ui.RunStream(stream, ui.StreamOptions{
		Harness:  "Claude Code",
		Title:    title,
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

// serveCachedPlaybook renders the cached entry through the existing in-process
// `run` path. The entry on disk carries YAML front matter; we strip it to the
// body, write it to a temp file, and reuse ui.Main() (which spins up the driver +
// orchestrator and drives the playbook in-process), passing --cached for the
// header badge and --cwd so runs execute in the request's project root.
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
// async path used to stash via SetReengage/SetDriver/SetAsker.
func reengageReady(d triage.Decision, req capture.Request, sess *session, cwd string) ui.OrchReady {
	if sess == nil {
		return ui.OrchReady{}
	}
	re := &orchestrator.Reengage{
		Req:         req,
		Agent:       sess.authoringAgent(),
		Events:      buildReengageEvents(req, sess),
		Cache:       cache.Open(),
		CtxHash:     d.CtxHash,
		ReqHash:     d.ReqHash,
		RequestJSON: requestJSON(req),
		Metadata:    buildMetadataSeam(sess),
		EnvLookup:   buildEnvLookup(sess.drv),
	}
	return ui.OrchReady{Orch: ui.BuildOrch(sess.drv, re), Asker: sess.asker(cwd)}
}

func serveCachedPlaybook(d triage.Decision, req capture.Request, sessCh <-chan *session, title string) int {
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
	// held captures the session for cleanup after ui.Main returns; it is written
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
	ui.SetPendingReady(readyCh)

	// Stage 4 (spec §C amend-on-rerun): this is a cache HIT — we are SERVING an
	// existing playbook for this context. Stash its body as the served base so a
	// failing step → troubleshoot → confirm/`w`-generate AMENDS this playbook
	// (base=servedBase) instead of starting fresh, and the improved version is
	// re-cached under the SAME CtxHash/ReqHash (populated on the Reengage above) —
	// the served entry is overwritten, never lost. Amend-vs-fresh is naturally scoped
	// by the cache key: a same-context failure serves+amends this entry; a different
	// context is a different cache entry → a cache MISS → authorPlaybook (servedBase
	// stays "" → fresh). The base is the INPUT to the amend; the output is base+fix.
	//
	// Stage 5 (spec §E/§F): cache.Body strips the OUTER (cache) layer, so `body`
	// still begins with the playbook's own front matter. Amend operates on the
	// literate content (H1 + body), not the YAML — the front matter is regenerated
	// at persist — so strip the playbook front matter before stashing the base.
	ui.SetServedBase(strippedAmendBase(body))

	// NB: the request-input-float asker (the `f` keybind), the re-engagement context,
	// and the shared driver are NO LONGER stashed here via SetAsker/SetReengage/
	// SetDriver — they all depend on the still-opening session, so they're folded into
	// the OrchReady the background goroutine delivers on readyCh once the open lands.

	// Reuse the `run` subcommand entrypoint in-process by shaping os.Args the way
	// ui.Main() parses them (os.Args[1]="run", flags from os.Args[2:]).
	argv := []string{os.Args[0], "run"}
	if created != "" {
		argv = append(argv, "--cached", created)
	}
	if cwd != "" {
		argv = append(argv, "--cwd", cwd)
	}
	if title != "" {
		// Carry the classify-supplied label as the served pager's header (overrides the
		// cached playbook's own H1 until/unless the user regenerates).
		argv = append(argv, "--title", title)
	}
	argv = append(argv, tmp)
	os.Args = argv
	code := ui.Main()

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

func dirExists(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
func head(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}
