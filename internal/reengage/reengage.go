// Package reengage is the AI-layer re-engagement surface split out of the executor
// (ADR-0009 step 2). It owns everything the regenerate / followup / finalplaybook /
// drift-regenerate kinds need to re-invoke the author in-process: the Reengage
// context (original request, injected capable agent, cache keys), the streaming
// fan-out + cache re-store, and the finalized-playbook persistence (CommitPlaybook).
//
// The ui holds an *Engine as a second handle beside the executor: shell actions go
// to internal/orchestrator, re-engagement goes here. This package may import the AI
// layer (author, agentstream, cache) but must NOT import the executor — the one
// executor-owned datum it needs (a drift patch's on-disk target) is injected as a
// function seam (Engine.targetPath), so the dependency direction stays downward.
package reengage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// ErrNotImplemented marks a re-engagement invocation that cannot proceed — no
// re-engagement context is wired (a nil Engine), or the context has neither an
// event producer nor a text-fallback agent. The ui gates on a non-nil Engine
// before calling, so in production this surfaces only for a genuinely unwired
// degraded context.
var ErrNotImplemented = errors.New("reengage: re-engagement not available")

// reengageActivityBuffer bounds the re-engagement fan-out's activity channel —
// enough to absorb a brief ui stall without blocking the event pump (sends are
// drop-if-full). Mirrors the initial authoring's activityBuffer in package main.
const reengageActivityBuffer = 16

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
	// KindReengageDriftRegen re-authors ONE drifted diff block against the current
	// target file; non-structured, returns a unified diff as text (no submit_playbook).
	KindReengageDriftRegen
)

// EventsFunc is the injected event producer for re-engagement: per kind it builds
// the right system prompt + user message (regenerate → standard authoring prompt;
// followup → the failed-output prompt; finalplaybook → the FINAL-PLAYBOOK prompt)
// and runs the OWNED harness invocation (author.RunHarnessEvents), returning a
// normalized agentstream.Event channel + a close/wait func. It is built in the
// launcher (which imports author + carries the session's mcp-config) so this
// package does NOT import author for the event path.
//
// The two payload args generalize the producer so each kind gets what it needs:
//   - base: the base playbook to AMEND (KindReengageFinalPlaybook only; "" → fresh).
//   - change: the change/context — for followup the failed command's output; for
//     finalplaybook the troubleshoot content / fix to fold in.
//     Unused for regenerate.
//
// constraints carries the session's user-rejected-approach reasons (spec
// "refuse-solution" §1): the producer folds them into the built system prompt via
// author.WithConstraints for EVERY kind, so a refused approach cannot resurface in
// a later re-engagement. An empty/nil list leaves the prompt byte-identical.
//
// A nil Events on Reengage selects the legacy text Agent fallback.
type EventsFunc func(kind ReengageKind, base, change string, constraints []string) (<-chan agentstream.Event, func() error, error)

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

// Reengage bundles the re-engagement context. Req is the original captured
// request; Agent is the injected author.Agent (author.HarnessAgent in production,
// a fake in tests) used as the TEXT-path FALLBACK. Events, when set, is the
// normalized event producer (author.RunHarnessEvents) that streams the model's
// live reasoning + tool activity during the re-engagement wait, exactly like the
// initial authoring; it is preferred over Agent. Cache/CtxHash/ReqHash/RequestJSON
// drive regenerate's fresh re-store of the produced playbook (followup/wrapup do
// NOT re-store the main playbook). DataRoot is the data dir for the wrap-up
// solution artifact and the KB append (defaults to cache.DefaultRoot when empty).
type Reengage struct {
	Req    capture.Request
	Agent  author.Agent
	Events EventsFunc
	// DriftRegenOnly marks a re-engagement context that supports ONLY the
	// drift-regenerate action (a `run --file` viewer wired to the harness for that one
	// thing), not a full authoring/troubleshoot session. The viewer keeps its followup
	// and whole-playbook-regenerate affordances OFF for such a context (see
	// canReengageInProc), so a standalone playbook can regenerate a drifted diff without
	// sprouting an authoring-only "try another fix" button.
	DriftRegenOnly bool
	Cache          *cache.Cache
	CtxHash        string
	ReqHash        string
	RequestJSON    string
	DataRoot       string

	// Body, when set, renders the currently-captured structured playbook (live).
	// Re-engagement uses it so the in-viewer stream EOF can show the re-authored
	// playbook from the session's submit_playbook capture, not the streamed text.
	Body func() string

	// Metadata is the injected model-classification seam used by CommitPlaybook to
	// fill the front-matter description/category/tags + per-var env rationales (spec
	// §B). The launcher wires it from author.PlaybookMetadata; tests inject a fake. It is
	// nil-safe: a nil seam (or any error from it) means CommitPlaybook persists with
	// empty model fields — metadata MUST NEVER fail the commit (the playbook must
	// still persist). It is decoupled from the author package via PlaybookMeta.
	Metadata func(doc string) (PlaybookMeta, error)

	// EnvLookup is the injected ground-truth environment seam used by CommitPlaybook
	// to fill (and redact) the front-matter env values (spec §C). The launcher wires it to
	// the driver shell's environment (dumped once, cached in the closure); tests
	// inject a fake map lookup. nil-safe: a nil seam means no env VALUES are captured
	// (referenced vars are simply omitted, since their values are unknown).
	EnvLookup func(name string) (value string, ok bool)

	// StoreDir is the resolved global store directory CommitPlaybook writes playbooks
	// into. Set by the launcher to cfg.GlobalStoreDir() so the writer and the store
	// reader (store.Index) always resolve the same directory. When empty, CommitPlaybook
	// falls back to <dataRoot>/playbooks for back-compat (the pre-Task-4 behaviour).
	// This package does NOT import config — the launcher injects the resolved dir.
	StoreDir string

	// Compact is the over-budget knowledge-compaction hook (ADR-0011 / K4). It fires
	// AFTER CommitPlaybook saves the solution artifact — the wrap-up's durable close —
	// so the facts the wrap-up just `remember`ed are compacted when a KB file now
	// exceeds budget. The launcher wires it to author.CompactOversized (root + budget +
	// the CompactKB call); this package stays config-free. nil-safe: a nil hook is a
	// no-op, and the hook is best-effort (it never fails the commit).
	Compact func()
}

// PlaybookMeta is the re-engagement-local mirror of the model's four classification
// fields (spec §A/§B), decoupling this package from the author package on the
// CommitPlaybook path. The launcher maps author.Metadata → PlaybookMeta, building
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

// Engine is the AI-layer re-engagement handle the ui holds beside the executor. It
// wraps a Reengage context and the injected drift-target resolver, and exposes the
// regenerate / followup / finalplaybook / drift-regenerate / commit surface.
type Engine struct {
	re *Reengage
	// targetPath resolves a drift patch's on-disk target file. It is injected from the
	// executor's DriftTargetPath so this package never imports internal/orchestrator.
	// nil when drift-regenerate is not wired (DriftRegen then errors).
	targetPath func(patch string) (string, error)
}

// New builds an Engine over re and the injected drift-target resolver. It returns
// nil when re is nil, so the ui's `engine != nil` gate degrades a context-less
// viewer to inert re-engagement (the pre-split `orch.Reengage == nil` behavior).
// All Engine methods tolerate a nil receiver, returning ErrNotImplemented, so a
// nil Engine is a safe no-op rather than a panic.
func New(re *Reengage, targetPath func(patch string) (string, error)) *Engine {
	if re == nil {
		return nil
	}
	return &Engine{re: re, targetPath: targetPath}
}

// DriftRegenOnly reports whether this context supports ONLY drift-regenerate (a
// `run --file` viewer), so the ui keeps its authoring-grade followup / whole-playbook
// regenerate affordances OFF. A nil Engine reports false.
func (e *Engine) DriftRegenOnly() bool {
	return e != nil && e.re != nil && e.re.DriftRegenOnly
}

// Body returns the live structured-playbook renderer, or nil when none is wired.
// The ui uses it to enter structured render mode so a re-engagement stream's EOF
// shows the captured submit_playbook body, not the streamed text.
func (e *Engine) Body() func() string {
	if e == nil || e.re == nil {
		return nil
	}
	return e.re.Body
}

// streamOne is the shared scaffold behind Regenerate / FinalPlaybook / Followup:
// try the EVENT path (owned harness invocation streaming live reasoning + tool
// activity), and on a producer/start error fall through to the TEXT Agent path.
//
// restore, when non-nil, is the best-effort cache re-store fired on stream close:
// the event path wraps the reader in a closeHook over the fan-out's authoritative
// Body(); the text path tees the reader via storeOnClose. Only regenerate re-stores
// (followup/finalplaybook pass nil). textFn is the author call for the fallback.
func (e *Engine) streamOne(kind ReengageKind, base, change string, mode StreamMode, constraints []string, restore func(body string), textFn func() (io.ReadCloser, error)) (io.ReadCloser, <-chan string, StreamMode, error) {
	re := e.re

	// EVENT PATH (preferred): the owned harness invocation streams the model's live
	// reasoning + tool activity during the wait, exactly like the initial authoring.
	if re.Events != nil {
		events, closeFn, err := re.Events(kind, base, change, constraints)
		if err == nil {
			reader, activity, fan := agentstream.FanOut(events, closeFn, reengageActivityBuffer)
			if restore != nil {
				reader = newCloseHook(reader, func() { restore(fan.Body()) })
			}
			return reader, activity, mode, nil
		}
		// Fall through to the text path on a producer/start error.
	}

	// TEXT PATH (fallback): no live activity line, the pre-2b behavior.
	if re.Agent == nil {
		return nil, nil, mode, ErrNotImplemented
	}
	stream, err := textFn()
	if err != nil {
		return nil, nil, mode, err
	}
	if restore != nil {
		stream = newStoreOnClose(stream, restore)
	}
	return stream, nil, mode, nil
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
func (e *Engine) Regenerate(constraints []string) (io.ReadCloser, <-chan string, StreamMode, error) {
	if e == nil {
		return nil, nil, ModeReplace, ErrNotImplemented
	}
	re := e.re

	// restore re-stores the fresh playbook on completion (best-effort), matching
	// the shell's regenerate re-store. followup/wrapup do NOT re-store the main
	// playbook. Wired only when the cache + keys are present (unkeyed original entry
	// → nil restore → no re-store on either path).
	var restore func(body string)
	if re.Cache != nil && re.CtxHash != "" && re.ReqHash != "" {
		restore = func(body string) {
			if strings.TrimSpace(body) == "" {
				return
			}
			_, _ = re.Cache.Store(re.CtxHash, re.ReqHash, "playbook", body, nil, re.RequestJSON)
		}
	}

	return e.streamOne(KindReengageRegenerate, "", "", ModeReplace, constraints, restore, func() (io.ReadCloser, error) {
		return author.Author(re.Req, re.Agent)
	})
}

// FinalPlaybook generates the clean, reusable FINAL-PLAYBOOK (spec §B) and returns
// its stream in ModeReplace — the ui clears the rendered troubleshoot and streams
// the playbook in, as if `run <file>.md`. base selects the mode: "" → FRESH (distill
// the resolved troubleshoot), non-empty → AMEND (fold the change into the base). The
// change arg carries the troubleshoot content (fresh) or the requested change (amend)
// to integrate. Stage 2 is GENERATE-ONLY: this method does NOT save or cache the
// result (no restore-on-close); persistence (save + cache-replace) is CommitPlaybook.
func (e *Engine) FinalPlaybook(base, change string, constraints []string) (io.ReadCloser, <-chan string, StreamMode, error) {
	if e == nil {
		return nil, nil, ModeReplace, ErrNotImplemented
	}
	re := e.re
	return e.streamOne(KindReengageFinalPlaybook, base, change, ModeReplace, constraints, nil, func() (io.ReadCloser, error) {
		return author.FinalPlaybookText(re.Req, base, change, re.Agent)
	})
}

// Followup re-engages the agent with the "your fix didn't work" prompt built from
// the original request + the failed command's captured output, and returns the
// revised-fix stream (ModeAppend — the ui appends the new section below the
// existing playbook). It does NOT re-store the main playbook (matching
// ai-assist-followup, which streams without persisting an artifact).
func (e *Engine) Followup(failedOutput string, constraints []string) (io.ReadCloser, <-chan string, StreamMode, error) {
	if e == nil {
		return nil, nil, ModeAppend, ErrNotImplemented
	}
	re := e.re
	return e.streamOne(KindReengageFollowup, "", failedOutput, ModeAppend, constraints, nil, func() (io.ReadCloser, error) {
		return author.Followup(re.Req, failedOutput, re.Agent)
	})
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
//  2. File save: it writes body to <StoreDir|DataRoot/playbooks>/<slug>.md, where
//     <slug> is derived from the `# Playbook — <title>` heading (sanitized), falling
//     back to the context hash (then "playbook") when no title is present. The
//     directory is created. The saved path is returned so the ui can confirm it.
//
// Front matter (spec §C/§E): the saved + cached asset is `FM + body`, not the bare
// body. The front matter is ASSEMBLED here, never authored into the live draft —
// `name`/`slug`/`created`/`project_root`/`request`/`env` are programmatic; only
// description/category/tags + per-var rationales come from the injected Metadata
// seam. Both seams (Metadata, EnvLookup) are nil-safe and best-effort: a missing or
// erroring Metadata yields empty model fields, a missing EnvLookup yields no env
// values — neither EVER fails the commit (the playbook must always persist).
//
// `depends_on:` is the one field with no regenerating seam: it is purely
// human-authored, so a rebuild-from-scratch would otherwise silently drop it on
// every re-commit. Before writing, if a file already exists at the resolved save
// path (i.e. this is a re-commit of the same title, not a first-time save), its
// `depends_on:` is read back and carried onto the freshly assembled front matter.
//
// An empty body or a missing Reengage context is an error (nothing to commit).
func (e *Engine) CommitPlaybook(body string) (string, error) {
	if e == nil {
		return "", ErrNotImplemented
	}
	if strings.TrimSpace(body) == "" {
		return "", errors.New("reengage: cannot commit an empty playbook")
	}
	re := e.re

	// Strip any preamble prose above the playbook's H1 title so the SAVED + CACHED
	// asset begins at the heading. Idempotent: a body already starting at the H1 is
	// unchanged; a body with no H1 is left untouched.
	body = stripPreamble(body)

	// Assemble the §C/§E front matter. `fm` is completed below with the ONE field
	// that has no regenerating seam (DependsOn: purely human-authored) before it is
	// prepended to the body.
	fm := re.buildFrontMatter(body)

	// Resolve the .md save path under the resolved store dir / <slug>.md.
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

	// Carry forward `depends_on:` from the file this commit is about to overwrite,
	// if one exists — it is the only purely human-authored front-matter field, and
	// buildFrontMatter (rebuilding from scratch) has no way to know about it. A
	// missing prior file is NOT an error: it just means this is a genuinely
	// first-time commit, so DependsOn stays nil.
	if raw, err := os.ReadFile(path); err == nil {
		old, _, _ := frontmatter.Parse(string(raw))
		fm.DependsOn = old.DependsOn
	}

	// `full` is what we persist (file + cache) instead of the bare body.
	full := frontmatter.Prepend(fm, body)

	// (1) Cache-REPLACE — best-effort, skipped when keys/cache absent (no entry to
	// replace). Mirrors Regenerate's restore: same keys + kind + request sidecar.
	if re.Cache != nil && re.CtxHash != "" && re.ReqHash != "" {
		_, _ = re.Cache.Store(re.CtxHash, re.ReqHash, "playbook", full, nil, re.RequestJSON)
	}

	// (2) Save the .md file (FM + body) at the resolved path.
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		return "", err
	}

	// (3) Over-budget knowledge compaction (spec "Fill and compact"): the wrap-up's
	// solution-artifact is now durable, so fire the injected hook to compact any KB
	// file the wrap-up's `remember` fill pushed over budget. Best-effort and nil-safe
	// — it manages its own failures/guards and never fails the commit.
	if re.Compact != nil {
		re.Compact()
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
	// on a portabilized body. Declare it explicitly for project_bound playbooks so the
	// host knows to set it at run time.
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

// DriftRegen asks the model for a fresh unified diff for a drifted patch, against the
// CURRENT target file, and returns it as text. It does NOT touch the structured/lastPB
// capture path. The empty string with a nil error means the model produced nothing.
//
// constraints carries the session's user-rejected-approach reasons (refuse-solution
// spec §1: injected into ALL FOUR re-engagement kinds — the drift-regen button in the
// authoring viewer is reachable after refusals, so this kind must carry them too).
// A nil/empty list leaves the prompt byte-identical.
func (e *Engine) DriftRegen(patch string, constraints []string) (string, error) {
	if e == nil {
		return "", errors.New("regenerate unavailable")
	}
	re := e.re
	if re.Events == nil || e.targetPath == nil {
		return "", errors.New("regenerate unavailable")
	}
	abs, err := e.targetPath(patch)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	events, closeFn, err := re.Events(KindReengageDriftRegen, string(content), patch, constraints)
	if err != nil {
		return "", err
	}
	reader, _, fan := agentstream.FanOut(events, closeFn, 0)
	if _, err := io.ReadAll(reader); err != nil { // drain to EOF
		return "", err
	}
	return stripCodeFence(fan.Body()), nil
}

// playbookTitle matches the literate-config playbook heading `# Playbook — <title>`
// (the em-dash the FINAL-PLAYBOOK prompt mandates), capturing <title>. A plain
// `# <title>` (no "Playbook —" prefix) is matched by the fallback in playbookSlug.
var playbookTitle = regexp.MustCompile(`(?m)^#\s+Playbook\s+—\s+(.+?)\s*$`)

// firstHeading matches the first markdown H1 `# <title>` as a fallback title source.
var firstHeading = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

// slugNonWord collapses any run of non-alphanumeric characters to a single dash.
var slugNonWord = regexp.MustCompile(`[^a-z0-9]+`)

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

// stripCodeFence removes a single wrapping ```... / ``` pair if the body is fenced
// (the drift-regen prompt forbids fences, but models sometimes add them).
func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	lines := strings.Split(t, "\n")
	if len(lines) < 2 {
		return s
	}
	// drop the opening ```[lang] line
	lines = lines[1:]
	// drop a trailing ``` line if present
	if strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
