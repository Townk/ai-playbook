// createcmd.go — the `create <prompt>` subcommand: author a playbook DIRECTLY
// from a prompt, bypassing triage entirely (no classify, no cache-hit serve, no
// cached/regenerate badge). It is the deliberate counterpart to `assist`, which
// triages; `create` always does the work.
//
// Force-author reuses the SAME authoring machinery assist's escalate route uses
// (openSession + authorPlaybook): the playbook streams into the pager via
// ui.RunStream and persists through Reengage{StoreDir: cfg.GlobalStoreDir()}
// (the store file) plus the cache entry — so a later `assist` for the same
// context can hit it. create itself NEVER serves a cache hit.
//
// Package-level seams (overridden in tests so the core runs without a live
// TUI/harness): createAuthorFn (the force-author step), captureFn (the in-process
// context capture), and searchFn (shared with storecmd.go, for the
// similar-playbooks banner).
package launcher

import (
	"fmt"
	"os"
	"strings"

	"github.com/Townk/ai-playbook/internal/askbridge"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/triage"
)

// captureFn is the context-capture seam: production wires capture.Capture; tests
// inject a canned capture.Request so CreateMain runs without shelling out.
var captureFn = capture.Capture

// createAuthorFn is the force-author seam: it opens the shared session and
// authors a fresh playbook through authorPlaybook (the same path assist's
// escalate uses) — NO triage.Route, NO classify, NO cache-hit serve, NO --cached
// (so the pager badge stays off). Tests inject a fake to drive createPlaybook
// without a live TUI/harness. Returns the ui exit code.
var createAuthorFn = realCreateAuthor

// CreateMain is the `ai-playbook create <prompt> [--template <t>]` subcommand.
// An empty prompt is a usage error (exit 2). --template is RESERVED: it parses,
// prints a one-line note to stderr, and is otherwise a no-op in Phase 1.
func CreateMain() int {
	dbgInit(os.Getenv("AI_PLAYBOOK_DEBUG_LOG"))

	prompt, template, err := parseCreateArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook create: %v\n", err)
		return 2
	}
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "ai-playbook create: <prompt> is required")
		return 2
	}
	if template != "" {
		fmt.Fprintln(os.Stderr, "note: --template is reserved (not yet implemented)")
	}

	m := mux.Load()
	paneID := ""
	if p := os.Getenv("ZELLIJ_PANE_ID"); p != "" {
		paneID = "terminal_" + p
	}
	// Build the capture the same standalone way SessionMain does, with the prompt
	// as the user request.
	req := captureFn(capture.Options{
		Mux:         m,
		Atuin:       capture.NewAtuin(),
		PaneID:      paneID,
		UserRequest: prompt,
	})
	return createPlaybook(req, m)
}

// parseCreateArgs splits the create args into the prompt (all non-flag words,
// joined) and an optional --template value (positionally robust: --template may
// appear before or after the prompt words, as `--template <v>` or
// `--template=<v>`). A trailing `--template` with no value is an error.
func parseCreateArgs(args []string) (prompt, template string, err error) {
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--template":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("--template requires a value")
			}
			template = args[i+1]
			i++
		case strings.HasPrefix(a, "--template="):
			template = strings.TrimPrefix(a, "--template=")
		default:
			words = append(words, a)
		}
	}
	return strings.TrimSpace(strings.Join(words, " ")), template, nil
}

// createPlaybook is the testable create core: emit the similar-playbooks banner
// (informational), then FORCE-AUTHOR. It deliberately consults NEITHER
// triage.Route NOR the classify — the only sinks are the banner (searchFn) and
// the force-author seam (createAuthorFn), so create can never serve a cache hit
// or show the cached badge.
func createPlaybook(req capture.Request, m mux.Mux) int {
	emitSimilarBanner(req.UserRequest)
	return createAuthorFn(req, m)
}

// emitSimilarBanner runs a store search on the prompt and, when it finds
// matches, prints a one-line "similar playbooks already exist: <name>, <name>"
// to stderr. Informational only — authoring proceeds regardless, and a search
// error is swallowed (never blocks create).
func emitSimilarBanner(prompt string) {
	matches, err := searchFn(prompt)
	if err != nil || len(matches) == 0 {
		return
	}
	names := make([]string, 0, len(matches))
	for _, mt := range matches {
		n := mt.Name
		if n == "" {
			n = mt.Slug
		}
		names = append(names, n)
	}
	fmt.Fprintf(os.Stderr, "similar playbooks already exist: %s\n", strings.Join(names, ", "))
}

// createDecision builds the cache decision the force-author path persists under.
// It computes the SAME (ctxHash, reqHash) keys assist's triage would — but
// WITHOUT calling triage.Route, so create never looks up or serves a cache hit.
// Outcome is always Escalate and Path is never set; the keys exist only so
// authorPlaybook stores the freshly authored playbook (letting a later `assist`
// for the same context hit it). The cache-disable guard mirrors triage.Route:
// an unreliable key (a failure with empty scrollback) is never stored.
func createDecision(req capture.Request) triage.Decision {
	if req.Exit != "" && req.Exit != "0" && strings.TrimSpace(req.Scrollback) == "" {
		return triage.Decision{Outcome: triage.Escalate, Disabled: true, Reason: "create: failure with empty scrollback — cache disabled"}
	}
	cr := cache.Request{
		ProjectRoot: req.ProjectRoot,
		CWD:         req.CWD,
		CommandText: req.Command,
		CommandExit: req.Exit,
		Scrollback:  req.Scrollback,
	}
	return triage.Decision{
		Outcome: triage.Escalate,
		CtxHash: cache.ContextHash(cr),
		ReqHash: cache.RequestHash(req.UserRequest),
		Reason:  "create: force-author",
	}
}

// realCreateAuthor is the production force-author step: open the shared session
// (so the run blocks drive the user's real shell, exactly like assist's escalate)
// and author a fresh playbook with a force-author decision. Unlike assist/escalate
// (which stream the build into the fullscreen viewer), create shows INLINE PROGRESS
// while authoring (createAuthorWithProgress) and only then opens the viewer with the
// COMPLETE playbook. It persists through Reengage{StoreDir: cfg.GlobalStoreDir()} +
// the cache entry, and never passes --cached (no badge).
func realCreateAuthor(req capture.Request, m mux.Mux) int {
	c := cache.Open()
	noCache := os.Getenv("AI_PLAYBOOK_NO_CACHE") != ""
	d := createDecision(req)

	cfg, _ := config.Load()
	shell := cfg.Driver.Shell

	// No-mux ask bridge: with no multiplexer there is no float to host the agent's
	// `ask`, so route asks to the inline ask box during authoring (and the in-viewer
	// overlay during the driving phase). nil when a real mux is present (the float).
	var bridge *askbridge.Bridge
	if mux.IsNull(m) {
		bridge = askbridge.New()
	}
	sess := openSession(req, m, bridge, shell)
	if sess != nil {
		defer sess.close()
	}
	return createAuthorWithProgress(req, d, c, noCache, sess, cfg)
}
