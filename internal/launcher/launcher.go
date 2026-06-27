// Package launcher holds the assist/session orchestration extracted from the
// ai-playbook command's package main (golang-standards "thin main"). It owns the
// launcher (Troubleshoot), the docked session pane (SessionMain), and the prose
// answer pager (AnswerMain) — the three subcommand entrypoints cmd/ai-playbook
// dispatches into — plus the cache-serve, authoring, and request-JSON plumbing
// behind them. This is a mechanical extraction: behavior is preserved exactly.
package launcher

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/floatinput"
	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/triage"
	"github.com/Townk/ai-playbook/internal/ui"
)

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
// mux (no zellij) there is no float/pane to spawn; the launcher renders the inline
// input box for an interactive request (inlineInput) or the explicit-progress
// classify (explicitProgress) — both run in the current pane, so headless and SSH
// contexts still work.
// launcherRoute reports whether the troubleshoot path should use the float/pane
// topology: true when a real multiplexer is present AND no explicit request was
// given on the CLI (so the user must be prompted via the float). When the mux is
// null (no multiplexer) or an explicit request is already known, the caller uses
// the inline fallback path instead.
func launcherRoute(m mux.Mux, cliRequest string) bool {
	return cliRequest == "" && !mux.IsNull(m)
}

func Troubleshoot() int {
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

	// With a real multiplexer and no explicit request: ask via the input float, then
	// spawn the docked session pane. Without a multiplexer (null mux) or when the
	// request is already known, run the session inline in the current pane — so
	// headless and SSH contexts still work.
	if launcherRoute(m, cliRequest) {
		selfExe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook troubleshoot: cannot resolve self: %v\n", err)
			return 1
		}
		return launch(m, selfExe, req, author.ClassifyRequest)
	}

	// Null-mux UX (no multiplexer): inline input box for an interactive request,
	// or the plain runInline for an explicit request (Task 3 replaces the explicit
	// branch with explicitProgress). The mux-present paths (float launch above;
	// real-mux+explicit runInline below) are unchanged.
	if mux.IsNull(m) {
		if cliRequest == "" {
			return inlineInput(req, m)
		}
		return explicitProgress(req, m)
	}

	// Real mux + explicit request: unchanged inline classify+route.
	return runInline(req, m)
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
func AnswerMain() int {
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
	_ = fs.Parse(os.Args[2:]) // flag.ExitOnError: Parse never returns a non-nil error

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

// runInlineClassify is runInline's classify seam: tests inject a fake classifyFunc
// to drive each route without calling the live model. Mirrors answerClassify.
var runInlineClassify classifyFunc = author.ClassifyRequest

// runInlineSessionFn is runInline's session seam: tests override this to assert the
// escalate branch without starting a live driver.
var runInlineSessionFn = runSession

// runInline is the null-mux / explicit-request path (no float, no panes): classify
// the request and route it simply (stage C). command → print the command for the
// user to run; answer → print the prose; escalate → run the full session inline
// (ui.Main full-screen TUI). A classify error escalates (the safe default).
// m is threaded through so the inline session uses the same mux decision the
// launcher already made (never re-loads it).
func runInline(req capture.Request, m mux.Mux) int {
	cfg, _ := config.Load()
	cls, err := runInlineClassify(req, author.AuthorOptions{Cfg: cfg})
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
		return runInlineSessionFn(req, "", m)
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
