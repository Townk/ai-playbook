// Package orchestrator is the in-process, typed replacement for the shell
// ai-assist-action-broker. The broker read <kind>␟<id>␟<payload>␞ records off a
// fifo and performed them; here the pager calls typed Go methods directly — no
// fifos, no text framing. Stage 2 wired the run/stop path to the driver (with
// value-passing across blocks) plus copy/play via the Mux. Stage 4c-i wires the
// diff kinds in-process: apply-diff / undo-diff git-apply the patch via the
// driver, and view-diff opens a floating diff viewer via the Float mux. The
// regenerate / followup / wrapup kinds remain modeled but deferred.
package orchestrator

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/frontmatter"
	"github.com/Townk/ai-playbook/internal/mux"
)

// defaultTimeout bounds a single run block (matches the broker's
// AI_PLAYBOOK_RUN_TIMEOUT default of 120s).
const defaultTimeout = 120 * time.Second

// reengageActivityBuffer bounds the re-engagement fan-out's activity channel —
// enough to absorb a brief ui stall without blocking the event pump (sends are
// drop-if-full). Mirrors the initial authoring's activityBuffer in package main.
const reengageActivityBuffer = 16

// ErrNotImplemented marks an action kind that is modeled but deferred to a later
// migration stage.
var ErrNotImplemented = errors.New("orchestrator: action kind not implemented yet")

// Mux is the terminal-multiplexer surface the orchestrator needs. Stage 2 needs
// only clipboard + type-into-origin-pane; diff-float et al. come with later
// stages, so they are not on the interface yet.
type Mux interface {
	// Copy places text on the clipboard (or OSC 52 over SSH).
	Copy(text string) error
	// Play types cmd into the user's origin pane and submits it.
	Play(cmd string) error
}

// Kind enumerates every action the broker handled, typed.
type Kind int

const (
	KindCopy Kind = iota
	KindPlay
	KindRun
	KindStop
	KindViewDiff
	KindApplyDiff
	KindUndoDiff
	KindRegenerate
	KindFollowup
)

// String renders a Kind as its broker record name.
func (k Kind) String() string {
	switch k {
	case KindCopy:
		return "copy"
	case KindPlay:
		return "play"
	case KindRun:
		return "run"
	case KindStop:
		return "stop"
	case KindViewDiff:
		return "view-diff"
	case KindApplyDiff:
		return "apply-diff"
	case KindUndoDiff:
		return "undo-diff"
	case KindRegenerate:
		return "regenerate"
	case KindFollowup:
		return "followup"
	default:
		return "unknown"
	}
}

// Action is the typed form of the broker's 3-field record (kind␟id␟payload).
type Action struct {
	Kind    Kind
	ID      string
	Payload string
}

// Orchestrator performs actions against a live shell Driver and a Mux.
//
// Mux is the small clipboard/play surface (copy/play). Float is the richer
// terminal-multiplexer surface used to open the view-diff float; it is optional —
// when nil, view-diff is a no-op success (the float just doesn't open) rather than
// an error, so a non-zellij environment degrades gracefully.
type Orchestrator struct {
	Drv   *driver.Driver
	Mux   Mux
	Float mux.Mux

	// Reengage carries everything the regenerate / followup / wrapup kinds need to
	// re-invoke the author in-process: the original request, the injected capable
	// agent, and (for regenerate's re-store) the cache + the original decision keys.
	// It is set via WithReengage by the troubleshoot path; nil in tests/callers that
	// don't exercise re-engagement (those kinds then return ErrNotImplemented, the
	// pre-4c-ii behavior).
	Reengage *Reengage
}

// ReengageKind selects which re-engagement prompt the injected Events producer
// builds for a given invocation.
type ReengageKind int

const (
	// KindReengageRegenerate re-authors the original request (standard prompt).
	KindReengageRegenerate ReengageKind = iota
	// KindReengageFollowup builds the "your fix didn't work" prompt from the
	// failed block's captured output.
	KindReengageFollowup
	// KindReengageFinalPlaybook builds the FINAL-PLAYBOOK prompt (author.FinalPlaybookPrompt):
	// fresh when base=="" (distill the resolved troubleshoot into a clean reusable
	// playbook), amend when base!="" (fold the change into the served base playbook).
	KindReengageFinalPlaybook
)

// EventsFunc is the injected event producer for re-engagement: per kind it builds
// the right system prompt + user message (regenerate → standard authoring prompt;
// followup → the failed-output prompt; finalplaybook → the FINAL-PLAYBOOK prompt)
// and runs the OWNED harness invocation (author.RunHarnessEvents), returning a
// normalized agentstream.Event channel + a close/wait func. It is built in main.go
// (which imports author + carries the session's mcp-config) so the orchestrator
// does NOT import author for the event path.
//
// The two payload args generalize the producer so each kind gets what it needs:
//   - base: the base playbook to AMEND (KindReengageFinalPlaybook only; "" → fresh).
//   - change: the change/context — for followup the failed command's output; for
//     finalplaybook the troubleshoot content / fix to fold in.
//     Unused for regenerate.
//
// A nil Events on Reengage selects the legacy text Agent fallback.
type EventsFunc func(kind ReengageKind, base, change string) (<-chan agentstream.Event, func() error, error)

// Reengage bundles the re-engagement context. Req is the original captured
// request; Agent is the injected author.Agent (author.ClaudeAgent in production,
// a fake in tests) used as the TEXT-path FALLBACK. Events, when set, is the
// normalized event producer (author.RunHarnessEvents) that streams the model's
// live reasoning + tool activity during the re-engagement wait, exactly like the
// initial authoring; it is preferred over Agent. Cache/CtxHash/ReqHash/RequestJSON
// drive regenerate's fresh re-store of the produced playbook (followup/wrapup do
// NOT re-store the main playbook). DataRoot is the data dir for the wrap-up
// solution artifact and the KB append (defaults to cache.DefaultRoot when empty).
type Reengage struct {
	Req         capture.Request
	Agent       author.Agent
	Events      EventsFunc
	Cache       *cache.Cache
	CtxHash     string
	ReqHash     string
	RequestJSON string
	DataRoot    string

	// Metadata is the injected model-classification seam used by CommitPlaybook to
	// fill the front-matter description/category/tags + per-var env rationales (spec
	// §B). main.go wires it from author.PlaybookMetadata; tests inject a fake. It is
	// nil-safe: a nil seam (or any error from it) means CommitPlaybook persists with
	// empty model fields — metadata MUST NEVER fail the commit (the playbook must
	// still persist). It is decoupled from the author package via PlaybookMeta.
	Metadata func(doc string) (PlaybookMeta, error)

	// EnvLookup is the injected ground-truth environment seam used by CommitPlaybook
	// to fill (and redact) the front-matter env values (spec §C). main.go wires it to
	// the driver shell's environment (dumped once, cached in the closure); tests
	// inject a fake map lookup. nil-safe: a nil seam means no env VALUES are captured
	// (referenced vars are simply omitted, since their values are unknown).
	EnvLookup func(name string) (value string, ok bool)

	// StoreDir is the resolved global store directory CommitPlaybook writes playbooks
	// into. Set by the launcher to cfg.GlobalStoreDir() so the writer and the store
	// reader (store.Index) always resolve the same directory. When empty, CommitPlaybook
	// falls back to <dataRoot>/playbooks for back-compat (the pre-Task-4 behaviour).
	// The orchestrator does NOT import config — the launcher injects the resolved dir.
	StoreDir string
}

// PlaybookMeta is the orchestrator-local mirror of the model's four classification
// fields (spec §A/§B), decoupling the orchestrator from the author package on the
// CommitPlaybook path. main.go maps author.Metadata → PlaybookMeta, building
// EnvNotes (env-var-name → why) from author.Metadata.ImportantEnvVars.
type PlaybookMeta struct {
	Description  string
	Category     string
	Tags         []string
	ProjectBound bool
	// EnvNotes maps an env-var name to the model's one-line rationale (why). It feeds
	// frontmatter.BuildEnv's notes argument: both a union source of var names and the
	// per-var why recorded in the front matter (never redacted — a rationale).
	EnvNotes map[string]string
}

// dataRoot resolves the data dir for wrap-up side effects: the explicit DataRoot,
// else cache.DefaultRoot (AI_PLAYBOOK_DATA_DIR / XDG), matching the shell's $DATA.
func (re *Reengage) dataRoot() string {
	if re.DataRoot != "" {
		return re.DataRoot
	}
	return cache.DefaultRoot()
}

// StreamMode tells the ui how to splice a re-engagement stream into the rendered
// playbook: Replace clears the rendered content first (regenerate); Append streams
// the new section below the existing playbook (followup, wrapup).
type StreamMode int

const (
	// ModeReplace resets the rendered playbook and streams a fresh one (regenerate).
	ModeReplace StreamMode = iota
	// ModeAppend streams a new section below the existing playbook (followup/wrapup).
	ModeAppend
)

// New builds an Orchestrator over the given driver and mux. The Float mux (for
// view-diff) is set separately via WithFloat so existing two-arg callers/tests
// keep compiling.
func New(d *driver.Driver, m Mux) *Orchestrator {
	return &Orchestrator{Drv: d, Mux: m}
}

// WithFloat sets the terminal-multiplexer surface used to open the view-diff
// floating pane and returns the orchestrator (chainable). Optional — leaving it
// nil makes view-diff a graceful no-op.
func (o *Orchestrator) WithFloat(f mux.Mux) *Orchestrator {
	o.Float = f
	return o
}

// WithReengage sets the re-engagement context (request + agent + cache keys) used
// by the regenerate / followup / wrapup kinds and returns the orchestrator
// (chainable). Optional — leaving it nil keeps those kinds returning
// ErrNotImplemented.
func (o *Orchestrator) WithReengage(re *Reengage) *Orchestrator {
	o.Reengage = re
	return o
}

// Do performs one action. For KindRun it returns the command Result; for every
// other kind the Result is zero. A deferred kind returns ErrNotImplemented.
func (o *Orchestrator) Do(a Action) (driver.Result, error) {
	switch a.Kind {
	case KindRun:
		// Execute the block in the shell, value-passing APB_OUT_<id>/LAST_* so a
		// later block can reference this one's output.
		return o.Drv.RunID(a.ID, a.Payload, defaultTimeout), nil
	case KindStop:
		// Interrupt the running block by killing its foreground process group.
		o.Drv.Stop()
		return driver.Result{}, nil
	case KindCopy:
		// Clipboard (or OSC 52 over SSH).
		return driver.Result{}, o.Mux.Copy(a.Payload)
	case KindPlay:
		// Type the block into the user's origin pane and run it.
		return driver.Result{}, o.Mux.Play(a.Payload)

	case KindViewDiff:
		// Open the patch side-by-side in a floating diff pane (fire-and-forget).
		return driver.Result{}, o.viewDiff(a.ID, a.Payload)
	case KindApplyDiff:
		// git-apply the patch in the session shell; Exit 0 → applied.
		return o.applyDiff(a.Payload, false), nil
	case KindUndoDiff:
		// git-apply --reverse the patch (apply⇄undo toggle); Exit 0 → reverted.
		return o.applyDiff(a.Payload, true), nil

	// ---- re-engagement kinds (stage 4c-ii) ----
	// These re-invoke the author and yield a NEW stream that must SWAP the ui's
	// rendered playbook — that doesn't fit Do's (Result, error) shape, so the ui
	// drives them through the dedicated Regenerate/Followup methods (which
	// return io.ReadCloser + a StreamMode) instead of Do. Reaching them here means
	// the caller used the wrong seam; surface ErrNotImplemented rather than
	// silently doing nothing.
	case KindRegenerate, KindFollowup:
		return driver.Result{}, ErrNotImplemented
	default:
		return driver.Result{}, ErrNotImplemented
	}
}

// Regenerate re-authors the ORIGINAL request with the cache bypassed and returns
// the fresh playbook stream (ModeReplace — the ui resets the rendered content and
// streams the new playbook). It re-stores the fresh playbook so a later identical
// request hits the refreshed entry, matching ai-assist-regenerate
// (AI_PLAYBOOK_NO_CACHE=1 for the lookup, then `cache store` the new body).
//
// Because the body is consumed by the ui (rendered incrementally), the re-store
// tees the stream into a buffer and persists it when the stream is fully read and
// closed — the same tee-on-completion pattern authorPlaybook uses. Re-store is
// best-effort and only fires when the cache + keys are present (it is skipped when
// the original entry was unkeyed).
func (o *Orchestrator) Regenerate() (io.ReadCloser, <-chan string, StreamMode, error) {
	if o.Reengage == nil {
		return nil, nil, ModeReplace, ErrNotImplemented
	}
	re := o.Reengage

	// restore re-stores the fresh playbook on completion (best-effort), matching
	// the shell's regenerate re-store. followup/wrapup do NOT re-store the main
	// playbook. Skipped when the cache + keys are absent (unkeyed original entry).
	restore := func(body string) {
		if re.Cache == nil || re.CtxHash == "" || re.ReqHash == "" {
			return
		}
		if strings.TrimSpace(body) == "" {
			return
		}
		_, _ = re.Cache.Store(re.CtxHash, re.ReqHash, "playbook", body, nil, re.RequestJSON)
	}

	// EVENT PATH (preferred): the owned harness invocation streams the model's live
	// reasoning + tool activity during the wait, exactly like the initial authoring.
	if re.Events != nil {
		events, closeFn, err := re.Events(KindReengageRegenerate, "", "")
		if err == nil {
			reader, activity, fan := agentstream.FanOut(events, closeFn, reengageActivityBuffer)
			reader = newCloseHook(reader, func() { restore(fan.Body()) })
			return reader, activity, ModeReplace, nil
		}
		// Fall through to the text path on a producer/start error.
	}

	// TEXT PATH (fallback): no live activity line, the pre-2b behavior.
	if re.Agent == nil {
		return nil, nil, ModeReplace, ErrNotImplemented
	}
	stream, err := author.Author(re.Req, re.Agent)
	if err != nil {
		return nil, nil, ModeReplace, err
	}
	if re.Cache != nil && re.CtxHash != "" && re.ReqHash != "" {
		stream = newStoreOnClose(stream, restore)
	}
	return stream, nil, ModeReplace, nil
}

// FinalPlaybook generates the clean, reusable FINAL-PLAYBOOK (spec §B) and returns
// its stream in ModeReplace — the ui clears the rendered troubleshoot and streams
// the playbook in, as if `run <file>.md`. base selects the mode: "" → FRESH (distill
// the resolved troubleshoot), non-empty → AMEND (fold the change into the base). The
// change arg carries the troubleshoot content (fresh) or the requested change (amend)
// to integrate. Stage 2 is GENERATE-ONLY: this method does NOT save or cache the
// result (no restore-on-close); persistence (save + cache-replace) is stage 3.
func (o *Orchestrator) FinalPlaybook(base, change string) (io.ReadCloser, <-chan string, StreamMode, error) {
	if o.Reengage == nil {
		return nil, nil, ModeReplace, ErrNotImplemented
	}
	re := o.Reengage

	// EVENT PATH (preferred): stream the model's live reasoning + tool activity.
	if re.Events != nil {
		events, closeFn, err := re.Events(KindReengageFinalPlaybook, base, change)
		if err == nil {
			reader, activity, _ := agentstream.FanOut(events, closeFn, reengageActivityBuffer)
			return reader, activity, ModeReplace, nil
		}
		// Fall through to the text path on a producer/start error.
	}

	// TEXT PATH (fallback): the FinalPlaybook prompt over the legacy text Agent.
	if re.Agent == nil {
		return nil, nil, ModeReplace, ErrNotImplemented
	}
	stream, err := author.FinalPlaybookText(re.Req, base, change, re.Agent)
	if err != nil {
		return nil, nil, ModeReplace, err
	}
	return stream, nil, ModeReplace, nil
}

// CommitPlaybook persists a finalized playbook (spec §E — the `w` commit action),
// taking the displayed draft body and making it THE saved asset. It performs the
// two durable side effects:
//
//  1. Cache-REPLACE: it re-stores this request's cache entry (same context/request
//     keys, kind "playbook") with body, exactly as Regenerate's re-store does — so a
//     future identical context → cache HIT serves the clean final playbook. This is
//     skipped gracefully (no error) when the cache or the decision keys are absent
//     (an unkeyed/cache-disabled request): there is no entry to replace, but the file
//     save below still runs so the asset is never lost.
//  2. File save: it writes body to <DataRoot>/playbooks/<slug>.md, where <slug> is
//     derived from the `# Playbook — <title>` heading (sanitized), falling back to the
//     context hash (then "playbook") when no title is present. The directory is
//     created. The saved path is returned so the ui can confirm it to the user.
//
// Front matter (spec §C/§E): the saved + cached asset is `FM + body`, not the bare
// body. The front matter is ASSEMBLED here, never authored into the live draft —
// `name`/`slug`/`created`/`project_root`/`request`/`env` are programmatic; only
// description/category/tags + per-var rationales come from the injected Metadata
// seam. Both seams (Metadata, EnvLookup) are nil-safe and best-effort: a missing or
// erroring Metadata yields empty model fields, a missing EnvLookup yields no env
// values — neither EVER fails the commit (the playbook must always persist).
//
// KB remember is DEFERRED (spec §E note): the final playbook + cache entry + saved
// file are the durable assets; a KB fact is a later refinement and must not block the
// commit. An empty body or a missing Reengage context is an error (nothing to commit).
func (o *Orchestrator) CommitPlaybook(body string) (string, error) {
	if o.Reengage == nil {
		return "", ErrNotImplemented
	}
	if strings.TrimSpace(body) == "" {
		return "", errors.New("orchestrator: cannot commit an empty playbook")
	}
	re := o.Reengage

	// Strip any preamble prose above the playbook's H1 title so the SAVED + CACHED
	// asset begins at the heading. Idempotent: a body already starting at the H1 is
	// unchanged; a body with no H1 is left untouched.
	body = stripPreamble(body)

	// Assemble the §C/§E front matter and prepend it to the body. `full` is what we
	// persist (file + cache) instead of the bare body.
	full := frontmatter.Prepend(re.buildFrontMatter(body), body)

	// (1) Cache-REPLACE — best-effort, skipped when keys/cache absent (no entry to
	// replace). Mirrors Regenerate's restore: same keys + kind + request sidecar.
	// The stored body now leads with the playbook FM; cache.Store wraps it in the
	// cache's OWN technical FM, so the entry has two ---…--- layers (spec §F).
	if re.Cache != nil && re.CtxHash != "" && re.ReqHash != "" {
		_, _ = re.Cache.Store(re.CtxHash, re.ReqHash, "playbook", full, nil, re.RequestJSON)
	}

	// (2) Save the .md file under the resolved store dir / <slug>.md (FM + body).
	// StoreDir is injected by the launcher (cfg.GlobalStoreDir()); empty → back-compat
	// fallback to <dataRoot>/playbooks so unmodified callers/tests are unaffected.
	dir := re.StoreDir
	if dir == "" {
		dir = filepath.Join(re.dataRoot(), "playbooks")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	slug := playbookSlug(body, re.CtxHash)
	path := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// buildFrontMatter assembles the playbook front matter (spec §C/§E) for body: the
// programmatic name (the H1) + provenance (created/project_root/request) we already
// hold, the model's classification fields (description/category/tags + per-var why)
// via the injected Metadata seam, and the redacted env map via frontmatter.BuildEnv
// over the injected EnvLookup. Both seams are nil-safe and best-effort: a nil/erroring
// Metadata leaves the model fields empty; a nil EnvLookup captures no env values.
func (re *Reengage) buildFrontMatter(body string) frontmatter.FrontMatter {
	// name: the playbook's H1 title (the model authored it; we just read it),
	// matching playbookSlug's title source.
	title := PlaybookName(body)

	// Model classification (best-effort): on a nil seam OR any error, continue with
	// empty model fields — NEVER fail the commit over metadata.
	var meta PlaybookMeta
	if re.Metadata != nil {
		if m, err := re.Metadata(body); err == nil {
			meta = m
		}
	}

	// env (spec §C): union of body-referenced vars and the model's importantEnvVars
	// (EnvNotes keys), values filled + redacted from the injected ground-truth lookup.
	// A nil lookup → an always-miss lookup so referenced vars are simply omitted.
	lookup := re.EnvLookup
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	// home anchors home-dir → "~" normalization for portability (shared playbooks).
	// An os.UserHomeDir error yields "" → no normalization (still safe).
	home, _ := os.UserHomeDir()
	env := frontmatter.BuildEnv(frontmatter.ScanEnvRefs(body), meta.EnvNotes, lookup, home)

	// PROJECT_ROOT is not in the shell, so BuildEnv cannot discover it via ScanEnvRefs
	// on a portabilized body (it appears as a literal $PROJECT_ROOT, which IS a ref, but
	// the lookup will miss it since it's not in the host shell). Declare it explicitly for
	// project_bound playbooks so the host knows to set it at run time.
	if meta.ProjectBound {
		if env == nil {
			env = map[string]frontmatter.EnvValue{}
		}
		if _, ok := env["PROJECT_ROOT"]; !ok {
			env["PROJECT_ROOT"] = frontmatter.EnvValue{Why: "the project directory; the host sets it to your project root at run"}
		}
	}

	return frontmatter.FrontMatter{
		Name:         title,
		Description:  meta.Description,
		Category:     meta.Category,
		Tags:         meta.Tags,
		Env:          env,
		Created:      time.Now().Format("2006-01-02"),
		ProjectRoot:  frontmatter.NormalizeHome(re.Req.ProjectRoot, home),
		ProjectBound: meta.ProjectBound,
		Request:      re.Req.UserRequest,
	}
}

// playbookTitle matches the literate-config playbook heading `# Playbook — <title>`
// (the em-dash the FINAL-PLAYBOOK prompt mandates), capturing <title>. A plain
// `# <title>` (no "Playbook —" prefix) is matched by the fallback in playbookSlug.
var playbookTitle = regexp.MustCompile(`(?m)^#\s+Playbook\s+—\s+(.+?)\s*$`)

// PlaybookName derives the front-matter `name` from a playbook body, the SAME way
// the live commit path does: the title in the `# Playbook — <title>` heading, else
// the first markdown H1 `# <title>`, else "". Exported so the `finalize` subcommand
// reuses the exact name-derivation the commit path uses (no second implementation
// to drift). A body with no H1 yields "".
func PlaybookName(body string) string {
	if m := playbookTitle.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	if m := firstHeading.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

// StripPreamble removes any prose ABOVE the first markdown H1 so a finalized
// playbook begins at its title; exported wrapper over stripPreamble for the
// `finalize` subcommand (same logic the commit path applies before assembling the
// front matter). Idempotent; a body with no H1 is returned unchanged.
func StripPreamble(body string) string { return stripPreamble(body) }

// firstHeading matches the first markdown H1 `# <title>` as a fallback title source.
var firstHeading = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

// slugNonWord collapses any run of non-alphanumeric characters to a single dash.
var slugNonWord = regexp.MustCompile(`[^a-z0-9]+`)

// stripPreamble removes any prose ABOVE the first markdown H1 (`# <title>`) so a
// finalized playbook begins at its title. With no H1 the body is returned
// unchanged (a transcript, not a playbook). Idempotent: a body already starting
// at the H1 is unchanged.
//
// Limitation: the scan is a simple first-`^# ` match and does not skip `#` inside
// fenced code blocks; a finalized playbook leads with its H1 before any fence, so
// in practice the title is matched first.
func stripPreamble(body string) string {
	if loc := firstHeading.FindStringIndex(body); loc != nil {
		return body[loc[0]:]
	}
	return body
}

// playbookSlug derives a filesystem-safe slug from the playbook body: the title in
// the `# Playbook — <title>` heading (else the first H1), lowercased with non-word
// runs collapsed to dashes and the ends trimmed. Falls back to the context hash, then
// "playbook", when no usable title is present — so a file is always written.
func playbookSlug(body, ctxHash string) string {
	title := ""
	if m := playbookTitle.FindStringSubmatch(body); m != nil {
		title = m[1]
	} else if m := firstHeading.FindStringSubmatch(body); m != nil {
		title = m[1]
	}
	slug := slugNonWord.ReplaceAllString(strings.ToLower(title), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		if ctxHash != "" {
			return ctxHash
		}
		return "playbook"
	}
	return slug
}

// Followup re-engages the agent with the "your fix didn't work" prompt built from
// the original request + the failed command's captured output, and returns the
// revised-fix stream (ModeAppend — the ui appends the new section below the
// existing playbook). It does NOT re-store the main playbook (matching
// ai-assist-followup, which streams without persisting an artifact).
func (o *Orchestrator) Followup(failedOutput string) (io.ReadCloser, <-chan string, StreamMode, error) {
	if o.Reengage == nil {
		return nil, nil, ModeAppend, ErrNotImplemented
	}
	re := o.Reengage

	// EVENT PATH (preferred): stream the model's live reasoning + tool activity.
	if re.Events != nil {
		events, closeFn, err := re.Events(KindReengageFollowup, "", failedOutput)
		if err == nil {
			reader, activity, _ := agentstream.FanOut(events, closeFn, reengageActivityBuffer)
			return reader, activity, ModeAppend, nil
		}
		// Fall through to the text path on a producer/start error.
	}

	// TEXT PATH (fallback).
	if re.Agent == nil {
		return nil, nil, ModeAppend, ErrNotImplemented
	}
	stream, err := author.Followup(re.Req, failedOutput, re.Agent)
	if err != nil {
		return nil, nil, ModeAppend, err
	}
	return stream, nil, ModeAppend, nil
}

// applyTimeout bounds a `git apply` run (small, local — far under the run default).
const applyTimeout = 30 * time.Second

// applyDiff writes the unified diff to a temp patch file and runs `git apply` in
// the session shell (via the driver, so it executes in the session's cwd/env),
// ported from the broker's broker::git_apply. reverse adds --reverse (the undo
// half of the apply⇄undo toggle). The flags mirror the broker exactly:
//
//	--recount          infer hunk line counts from the body (agent-authored diffs
//	                   reliably miscount the @@ headers; the body is correct).
//	--ignore-whitespace forgive context-line whitespace drift.
//
// The returned driver.Result is the verdict: Exit 0 = applied/reverted; a
// non-zero Exit with stderr = failure feedback the ui surfaces. The patch file is
// removed after the run.
func (o *Orchestrator) applyDiff(diff string, reverse bool) driver.Result {
	patch, err := writePatch(diff)
	if err != nil {
		return driver.Result{Exit: -1, Err: err.Error()}
	}
	defer os.Remove(patch)
	cmd := "git apply --recount --ignore-whitespace "
	if reverse {
		cmd += "--reverse "
	}
	cmd += "-- " + shquote(patch)
	return o.Drv.Run(cmd, applyTimeout)
}

// viewDiff writes the patch to a temp file and opens it in a floating viewer pane
// (hunk → delta → less, like the broker's broker::open_diff). Fire-and-forget:
// the float is best-effort, so a nil Float mux or a spawn error is non-fatal.
// The patch file is intentionally NOT removed — the floating viewer reads it
// asynchronously after this returns (the OS reclaims temp files; the broker left
// them too).
func (o *Orchestrator) viewDiff(id, diff string) error {
	if o.Float == nil {
		return nil // no mux wired → graceful no-op (the float just doesn't open)
	}
	patch, err := writePatch(diff)
	if err != nil {
		return err
	}
	name := "diff:" + id
	cwd := o.projectRoot()
	return o.Float.SpawnFloat(mux.SpawnOptions{
		Cmd:      diffViewerCmd(patch),
		Cwd:      cwd,
		Name:     name,
		Floating: true,
		Width:    90,
		Height:   90,
	})
}

// projectRoot anchors the float pane's cwd, mirroring the broker's
// ${AI_PLAYBOOK_PROJECT_ROOT:-$PWD}: the driver's session cwd, else
// $AI_PLAYBOOK_PROJECT_ROOT, else "" (the mux falls back to its own default).
func (o *Orchestrator) projectRoot() string {
	if o.Drv != nil {
		if c := o.Drv.Cwd(); c != "" {
			return c
		}
	}
	return os.Getenv("AI_PLAYBOOK_PROJECT_ROOT")
}

// writePatch writes diff to a temp patch file with a guaranteed trailing newline
// (git apply rejects a patch without one — the broker appends one too) and
// returns its path.
func writePatch(diff string) (string, error) {
	f, err := os.CreateTemp("", "ai-playbook-apply-*.patch")
	if err != nil {
		return "", err
	}
	name := f.Name()
	body := diff
	if len(body) == 0 || body[len(body)-1] != '\n' {
		body += "\n"
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// diffViewerCmd resolves the diff viewer command for patch, porting the broker's
// preference: hunk (split mode) → delta (side-by-side) → less. hunk is overridable
// via $AI_PLAYBOOK_HUNK_BIN (for tests, as in the broker).
func diffViewerCmd(patch string) []string {
	if h := hunkBin(); h != "" {
		return []string{h, "patch", "--mode", "split", patch}
	}
	if d := lookViewer("delta"); d != "" {
		return []string{d, "--side-by-side", "--paging=always", patch}
	}
	return []string{"less", patch}
}

// hunkBin resolves the hunk binary: $AI_PLAYBOOK_HUNK_BIN, else hunk on PATH, else
// well-known install dirs, else "" (not installed).
func hunkBin() string {
	if v := os.Getenv("AI_PLAYBOOK_HUNK_BIN"); v != "" {
		return v
	}
	return lookViewer("hunk")
}

// lookViewer resolves name on PATH, else a couple of well-known install dirs,
// returning "" when not found.
func lookViewer(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, cand := range []string{
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return cand
		}
	}
	return ""
}

// shquote single-quotes s for safe inclusion in a shell command line (the driver
// runs cmd through zsh). Matches driver.shquote semantics.
func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
