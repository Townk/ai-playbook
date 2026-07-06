// runcmd.go — the `ai-playbook run` subcommand entrypoint and its argument
// resolution.
//
// `run` accepts a single playbook source, expressed one of three ways:
//
//   - run <slug>            a bare positional ⇒ implied --playbook <slug>
//   - run --playbook <slug> a saved playbook resolved through the store
//   - run --file <path>     a raw markdown file rendered as-is
//
// Exactly one source must be given; zero or more than one is an error.
//
// PORTABILITY (Phase B2a). A stored playbook is rendered AS-IS — there is no
// model adapt-on-run. A project_bound source (store.Meta.ProjectBound, or a
// --file front matter's project_bound) carries portable $PROJECT_ROOT references:
// the run path resolves the heuristic project root, sets it on the run driver via
// ui.Options.ProjectRoot (which exports PROJECT_ROOT=<root>), and opens the driver
// there (Cwd). A non-project_bound source renders with no PROJECT_ROOT. A raw
// --file with no front matter renders as-is.
//
// Internal callers (serveCachedPlaybook, AnswerMain) do NOT go through RunMain:
// they build their own ui.Options and call ui.Run directly.
package launcher

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Townk/ai-playbook/internal/autorun"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/runlog"
	"github.com/Townk/ai-playbook/internal/ui"
	"github.com/Townk/ai-playbook/pkg/playbook"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
	"github.com/Townk/ai-playbook/pkg/store"
)

// storeLoadFn is the store Load seam: resolves a slug to its Meta + body over
// the configured store dirs (storeDirs, storecmd.go). Tests inject a fake so
// the run gate is exercised without a real store.
var storeLoadFn = func(slug string) (store.Meta, string, error) {
	d, err := storeDirs()
	if err != nil {
		return store.Meta{}, "", err
	}
	return d.Load(slug)
}

// storePathForFn is the store PathFor seam: resolves a slug to its file path +
// whether it exists, with no parse. loadDepNode uses this (rather than
// storeLoadFn) because a depends_on chain only needs the raw file to re-parse
// its full front matter — Meta's Env/DependsOn shapes differ from
// frontmatter.FrontMatter's. Tests inject a fake so dependency resolution is
// exercised without a real store.
var storePathForFn = storePathFor

// projectRootFn is the capture.ProjectRoot seam: the heuristic project root a
// project_bound playbook is run in (and exported as $PROJECT_ROOT).
var projectRootFn = capture.ProjectRoot

// autorunRunFn is the autorun.Run seam: the headless (`--auto`) run executes the
// converted block sequence without a driver/viewer. Tests inject a fake so the
// auto branch is exercised without opening a real shell.
var autorunRunFn = autorun.Run

// RunMain is the `ai-playbook run` subcommand: it owns config loading + the
// configured-shell hand-off (ui stays config-agnostic), resolves the run
// argument, and renders the resolved playbook through ui.Run (via uiRunFn). It
// seeds a ui.Options with the run-level fields (Shell + the auto-rollback/assisted
// opt-ins) and threads it down; runFile/runViewer fill in the source-specific
// fields (File/Cwd/ProjectRoot/Reengage) before the single uiRunFn call.
func RunMain() int {
	// cfg is always non-nil (config.Load returns Default on error). The `run`
	// subcommand opens its own driver, so honor the configured shell — ui stays
	// config-agnostic and receives the selector as DATA via Options.Shell.
	cfg, _ := config.Load()

	ra, err := resolveRunArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 2
	}

	if ra.Mode == modeAuto {
		return autoRun(ra) // headless: autoRun owns the whole depends_on chain itself
	}

	parent, perr := loadParent(ra)
	if perr != nil {
		// Same load failure runFile/runPlaybook would hit moments later — exit 1
		// (not 2) to match their existing, already-tested exit code; 2 is
		// reserved for depends_on structural issues (cycle/dangling) below.
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", perr)
		return 1
	}

	// `--retry` gate ladder — BEFORE the depends_on chain, so a refused retry
	// never runs the deps. journalIdentity is the SAME seam the write path
	// (runFile / autoRun) resolves through, so both `run --retry <slug>` and
	// `run --retry --file <path>` find exactly the journal the non-retry form
	// would write. A passing gate yields the pre-seed (nil on the fresh-run
	// degradation) threaded into the viewer via ui.Options.
	var retrySeed map[string]runlog.BlockRecord
	if ra.Retry {
		storeSlug := ""
		if ra.Kind == "playbook" {
			storeSlug = ra.Value
		}
		jPath, _, jHash := journalIdentity(storeSlug, parent.Path, parent.Raw)
		seed, code, proceed := retryGate(jPath, jHash, playbook.ParseBlocks(parent.Body))
		if !proceed {
			return code
		}
		retrySeed = seed
	}

	if len(parent.FM.DependsOn) > 0 {
		order, issues := resolveChain(parent.FM.DependsOn)
		if len(issues) > 0 {
			printDepIssues(os.Stderr, issues)
			return 2
		}
		// Interactive/assisted parent: run the deps headless first (no
		// --with-env — that flag is --auto only), then dispatch to the viewer
		// for the parent exactly as today.
		if code := runDeps(order, nil, true, cfg.Driver.Shell, os.Stdout); code != 0 {
			return code
		}
	}

	// Seed the viewer Options with the run-level fields: the configured shell (for
	// ui's own-driver open), the auto-rollback opt-in (auto-fire rollback on a step
	// failure), and the assisted opt-in (GUIDED fullscreen rides the same viewer path
	// as default). runFile/runViewer fill in the source-specific fields.
	opts := ui.Options{
		Shell:        cfg.Driver.Shell,
		AutoRollback: ra.AutoRollback,
		Assisted:     ra.Mode == modeAssisted,
		RetrySeed:    retrySeed,
	}

	switch ra.Kind {
	case "file":
		return runFile(ra.Value, "", opts)
	case "playbook":
		return runPlaybook(ra.Value, opts)
	}
	return 0
}

// journalIdentity resolves the run-journal wiring for a playbook run — the
// journal file path (shared data root + kb project key + run key), the
// journaled playbook identity (absolute source path), and the content sha256
// (the retry drift gate). slug is the STORE slug ("" for a raw `--file` run,
// whose key is the sha1 of its absolute path). The project key derives from
// the invocation's heuristic project root (projectRootFn — the same
// derivation the KB path uses), so a later `run --retry` from the same
// project resolves the same journal.
//
// Journals are advisory: any resolution failure prints one stderr note and
// returns an empty journalPath (journaling off), never failing the run.
func journalIdentity(slug, file, raw string) (journalPath, playbookPath, contentHash string) {
	abs, err := filepath.Abs(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: run journal disabled (%v)\n", err)
		return "", "", ""
	}
	key := runlog.RunKey(slug, abs)
	return runlog.Path(cache.DefaultRoot(), projectRootFn(), key), abs, runlog.ContentHash(raw)
}

// retryGate runs the `--retry` gate ladder (spec Decision 3) against the
// journal at journalPath, in order, each refusal with its own message:
//
//  1. no journal — never run, unreadable path, corrupt file, or journaling
//     unavailable — → message + exit 1 (a corrupt journal is advisory
//     metadata: on the retry READ side it means "no journal");
//  2. the journaled run succeeded → "nothing to resume" + exit 0;
//  3. content-hash mismatch (the playbook's raw bytes changed since the
//     failed run) → refusal + exit 1 (no partial resume of a drifted
//     document);
//  4. an empty pre-seed (no ok blocks, or demotion emptied it) → a note, then
//     fall through to a plain FRESH run (nil seed, proceed=true).
//
// Otherwise it returns the pre-seed to thread into the run (proceed=true)
// after a one-line resume note. contentHash is the CURRENT file's raw-byte
// hash from journalIdentity — the same derivation the journal writer stored,
// so the comparison is like-for-like.
func retryGate(journalPath, contentHash string, blocks []playbook.Block) (map[string]runlog.BlockRecord, int, bool) {
	if journalPath == "" {
		// journalIdentity already printed why journaling is unavailable.
		fmt.Fprintln(os.Stderr, "ai-playbook run: --retry: no run journal available — nothing to resume")
		return nil, 1, false
	}
	run, err := runlog.Load(journalPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "ai-playbook run: --retry: no previous run recorded for this playbook — nothing to resume")
		} else {
			fmt.Fprintf(os.Stderr, "ai-playbook run: --retry: run journal unreadable (%v) — nothing to resume\n", err)
		}
		return nil, 1, false
	}
	if run.Outcome == runlog.OutcomeOK {
		fmt.Fprintln(os.Stderr, "ai-playbook run: last run succeeded — nothing to resume")
		return nil, 0, false
	}
	if run.ContentHash != contentHash {
		fmt.Fprintln(os.Stderr, "ai-playbook run: --retry: playbook changed since the failed run — run fresh (drop --retry)")
		return nil, 1, false
	}
	seed := runlog.RetrySeed(blocks, run)
	if seed.Fresh {
		fmt.Fprintln(os.Stderr, "ai-playbook run: --retry: no prior progress to carry over — running fresh")
		return nil, 0, true
	}
	// StartID can be "" on the degenerate resumable journal (killed between
	// the last block's record and Finalize, every forward block ok, no verify
	// block to anchor the resume) — omit the "resuming at" clause rather than
	// print `resuming at ""`.
	note := fmt.Sprintf("ai-playbook run: %d block(s) done in the previous run", len(seed.PreSeeded))
	if seed.StartID != "" {
		note = fmt.Sprintf("ai-playbook run: resuming at %q — %d block(s) done in the previous run", seed.StartID, len(seed.PreSeeded))
	}
	if len(seed.Demoted) > 0 {
		note += fmt.Sprintf("; re-running %s (outputs are not retained across sessions)", strings.Join(seed.Demoted, ", "))
	}
	fmt.Fprintln(os.Stderr, note)
	return seed.PreSeeded, 0, true
}

// runPlaybook resolves slug to its stored file path and delegates to runFile — the
// SAME `run --file` viewer path a raw file takes, so `run <slug>` and
// `run --file <that file>` are provably one code path. This matters because
// store.Load's returned body is front-matter-stripped (for the store's own
// listing/display use); rendering THAT (as an earlier version of this function
// did, via a since-removed temp-file round-trip) silently dropped the env: map
// (disabling the confirmation gate and description subtitle) and the declared
// project_root for every stored run. runFile re-reads + re-parses meta.Path
// itself, so store.Meta.ProjectBound is not consulted here — the file's OWN
// front matter decides, exactly like `run --file` would.
func runPlaybook(slug string, opts ui.Options) int {
	meta, _, lerr := storeLoadFn(slug)
	if lerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", lerr)
		return 1
	}
	// The slug is threaded so the run journal keys on the stored playbook's
	// stable identity (runlog.RunKey) instead of its file location.
	return runFile(meta.Path, slug, opts)
}

// runFile renders a markdown file through the `run --file` viewer. The ORIGINAL file
// is always what ui.Run renders — ui.Run strips any front matter for display AND
// extracts the declared env map for the confirmation gate, so we must NOT pre-strip it
// (an earlier temp-file round-trip did, which silently discarded both the run cwd and
// the env map). A project_bound file resolves its project root (declared project_root
// relative to the heuristic repo root, else the repo root itself), exports it as
// PROJECT_ROOT, and opens there; a plain front-matter file opens in the file's own
// directory; a raw file with no front matter renders as-is in the invocation cwd.
//
// slug is the store slug when the run came through `run <slug>` (runPlaybook),
// "" for a raw `run --file` — it only feeds the run journal's key.
func runFile(file, slug string, opts ui.Options) int {
	data, rerr := os.ReadFile(file)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", rerr)
		return 1
	}
	// Run-journal wiring (advisory): the launcher resolves the journal path +
	// playbook identity; ui receives them as data (empty path = journaling off).
	opts.JournalPath, opts.JournalPlaybookPath, opts.JournalContentHash = journalIdentity(slug, file, string(data))
	fm, _, ok := frontmatter.Parse(string(data))
	if !ok {
		// No front matter → render as-is (cwd derived from the file by ui.Run).
		return runViewer(file, "", opts)
	}
	cwd := ""
	if fm.ProjectBound {
		root := resolveProjectRoot(fm.ProjectRoot)
		opts.ProjectRoot = root // the run driver exports PROJECT_ROOT=<root>
		cwd = root
	} else if dir := filepath.Dir(file); dir != "" {
		// The `run --file` cwd rule: blocks run in the playbook file's own directory
		// so the body's relative paths resolve against it.
		if abs, aerr := filepath.Abs(dir); aerr == nil {
			cwd = abs
		} else {
			cwd = dir
		}
	}
	return runViewer(file, cwd, opts)
}

// autoRun executes ra headlessly (`run --auto`): it resolves the same source
// (markdown + cwd + PROJECT_ROOT) that runFile/runPlaybook would open a viewer
// on — via loadParent, so the parent's FULL front matter (env + depends_on) is
// available — parses the front-matter-stripped body into blocks with the SAME
// canonical parser the viewer uses (playbook.ParseBlocks), converts them to
// autorun.Block, and hands the sequence to autorunRunFn. No viewer/driver pane
// is ever opened.
//
// When the parent declares no depends_on, this is exactly today's
// single-playbook run (one autorunRunFn call, its own undeclared-override
// warning, SuppressUndeclaredWarning: false). When it does, autoRun owns the
// WHOLE chain: it resolves every dependency (resolveChain), emits ONE
// union-warning for the parent's + every dependency's declared vars against
// ra.EnvOverrides (so a --with-env key only a dependency declares is never
// flagged), runs the dependencies headless in order via runDeps (aborting on
// the first failure), and finally runs the parent itself, headless and
// suppressed.
func autoRun(ra runArgs) int {
	cfg, _ := config.Load()

	parent, perr := loadParent(ra)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", perr)
		return 1
	}
	slug := parentSlug(ra)

	// Run-journal wiring for the PARENT run — the same identity the viewer
	// path (runFile) resolves, so `run` and `run --auto` share one journal.
	// The journal key uses the STORE slug only ("" for a raw file, whose key
	// is its path sha1) — parentSlug's filename-derived slug is a display/log
	// name, not a stable identity. Dependency runs (runDeps) stay unjournaled:
	// each dep is its own playbook and R1 journals the requested run.
	storeSlug := ""
	if ra.Kind == "playbook" {
		storeSlug = ra.Value
	}
	jPath, jPlaybook, jHash := journalIdentity(storeSlug, parent.Path, parent.Raw)

	// `--auto --retry`: the same gate ladder as the viewer path, resolved
	// through the same journal identity — a passing gate's pre-seed makes the
	// headless loop skip the previously-ok steps and resume at the first
	// non-ok one. Gated BEFORE the depends_on chain so a refusal runs nothing.
	var retrySeed map[string]runlog.BlockRecord
	if ra.Retry {
		seed, code, proceed := retryGate(jPath, jHash, playbook.ParseBlocks(parent.Body))
		if !proceed {
			return code
		}
		retrySeed = seed
	}

	if len(parent.FM.DependsOn) == 0 {
		// Pass the front-matter-stripped body — NOT the raw source — so the
		// YAML fence never gets mis-parsed as a code block.
		if parent.FM.ProjectBound {
			os.Setenv("PROJECT_ROOT", parent.Cwd) // mirrors Options.ProjectRoot's driver export
		}
		return autorunRunFn(autorun.RunConfig{
			Blocks:              blocksFor(parent.Body),
			EnvVars:             parent.FM.Env,
			Cwd:                 parent.Cwd,
			Shell:               cfg.Driver.Shell,
			Slug:                slug,
			AutoRollback:        !ra.NoAutoRollback,
			EnvOverrides:        ra.EnvOverrides,
			JournalPath:         jPath,
			JournalPlaybookPath: jPlaybook,
			JournalContentHash:  jHash,
			RetrySeed:           retrySeed,
		})
	}

	order, issues := resolveChain(parent.FM.DependsOn)
	if len(issues) > 0 {
		printDepIssues(os.Stderr, issues)
		return 2
	}

	union := unionDeclared(parent.FM, order) // parent + deps declared vars
	autorun.WarnUndeclared(os.Stdout, union, ra.EnvOverrides)

	if code := runDeps(order, ra.EnvOverrides, !ra.NoAutoRollback, cfg.Driver.Shell, os.Stdout); code != 0 {
		return code
	}

	if parent.FM.ProjectBound {
		os.Setenv("PROJECT_ROOT", parent.Cwd)
	}
	return autorunRunFn(autorun.RunConfig{
		Blocks:                    blocksFor(parent.Body),
		EnvVars:                   parent.FM.Env,
		EnvOverrides:              ra.EnvOverrides,
		Cwd:                       parent.Cwd,
		Shell:                     cfg.Driver.Shell,
		Slug:                      slug,
		AutoRollback:              !ra.NoAutoRollback,
		SuppressUndeclaredWarning: true,
		Out:                       os.Stdout,
		JournalPath:               jPath,
		JournalPlaybookPath:       jPlaybook,
		JournalContentHash:        jHash,
		RetrySeed:                 retrySeed,
	})
}

// loadParent resolves ra's single playbook source (file or store slug) to a
// depNode carrying its FULL front matter (env + depends_on), body, and cwd —
// the same resolution runFile/runPlaybook/autoRun's single-playbook path use,
// factored out so the depends_on chain (resolveChain) can see the parent's
// declared dependencies before dispatch.
//
// "file": read + parse; cwd mirrors runFile's rule (a project_bound file's
// resolved project_root, else the file's own directory). "playbook": resolve
// existence via storeLoadFn (mapping its error, e.g. an unknown slug) then
// re-read + parse meta.Path directly — storeLoadFn's Meta does not carry the
// frontmatter.FrontMatter shape (its Env/DependsOn fields differ), so the full
// front matter is only available by re-parsing the file. Slug is set for a
// store playbook; a raw file has no store slug (parentSlug derives one from
// the filename for run-config purposes instead).
func loadParent(ra runArgs) (depNode, error) {
	switch ra.Kind {
	case "file":
		data, rerr := os.ReadFile(ra.Value)
		if rerr != nil {
			return depNode{}, rerr
		}
		fm, body, ok := frontmatter.Parse(string(data))
		cwd := ""
		if ok && fm.ProjectBound {
			cwd = resolveProjectRoot(fm.ProjectRoot)
		} else if dir := filepath.Dir(ra.Value); dir != "" {
			// Mirrors the `run --file` cwd rule (runFile / ui.Run's Cwd
			// default): blocks run in the playbook file's own directory.
			if abs, aerr := filepath.Abs(dir); aerr == nil {
				cwd = abs
			} else {
				cwd = dir
			}
		}
		return depNode{FM: fm, Body: body, Cwd: cwd, Path: ra.Value, Raw: string(data)}, nil
	case "playbook":
		meta, _, lerr := storeLoadFn(ra.Value)
		if lerr != nil {
			return depNode{}, lerr
		}
		data, rerr := os.ReadFile(meta.Path)
		if rerr != nil {
			return depNode{}, rerr
		}
		fm, body, _ := frontmatter.Parse(string(data))
		cwd := ""
		if fm.ProjectBound {
			cwd = resolveProjectRoot(fm.ProjectRoot)
		}
		return depNode{Slug: ra.Value, FM: fm, Body: body, Cwd: cwd, Path: meta.Path, Raw: string(data)}, nil
	}
	return depNode{}, fmt.Errorf("unsupported run source kind %q", ra.Kind)
}

// parentSlug derives the Slug a run-config uses for the root/parent playbook:
// the store slug for a "playbook" source, else (a raw "file" source, which has
// no store slug) the file's base name with its extension stripped — mirrors
// autoRun's pre-depends_on slug derivation exactly, so the no-depends_on path
// is unaffected.
func parentSlug(ra runArgs) string {
	if ra.Kind == "file" {
		base := filepath.Base(ra.Value)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return ra.Value
}

// blocksFor parses a playbook body into blocks and converts them to autorun.Block,
// the headless-run representation (shared by --auto and the depends_on runner). It
// uses playbook.ParseBlocks — the single canonical parser (ADR-0009 step 1) — so
// the headless run enumerates exactly the blocks the viewer would, without paying
// for a full styled render.
func blocksFor(body string) []autorun.Block {
	pbBlocks := playbook.ParseBlocks(body)
	blocks := make([]autorun.Block, 0, len(pbBlocks))
	for _, b := range pbBlocks {
		blocks = append(blocks, autorun.Block{
			ID:       b.ID,
			Command:  b.Payload,
			Lang:     b.Lang,
			Needs:    b.Needs,
			From:     b.From,
			Rollback: b.Rollback,
			Static:   b.Static,
			Timeout:  b.Timeout,
			Kind:     kindFromType(b.Type),
		})
	}
	return blocks
}

// kindFromType maps a playbook.Block's Type tag to autorun's StepKind: "diff" →
// KindApplyDiff, "create" → KindCreateFile; everything else ("shell", "run",
// "static") → KindRun (a static block is excluded from execution by its Static
// flag / autorun.Sequence, not by its Kind).
func kindFromType(t string) autorun.StepKind {
	switch t {
	case "diff":
		return autorun.KindApplyDiff
	case "create":
		return autorun.KindCreateFile
	default:
		return autorun.KindRun
	}
}

// resolveProjectRoot resolves a project_bound playbook's root. An explicit
// front-matter project_root is resolved relative to the heuristic repo root
// (absolute values are used verbatim); an empty project_root falls back to the
// heuristic root itself.
func resolveProjectRoot(declared string) string {
	if declared == "" {
		return projectRootFn()
	}
	if filepath.IsAbs(declared) {
		return declared
	}
	return filepath.Join(projectRootFn(), declared)
}

// runViewer renders file through the `run --file` viewer (ui.Run via uiRunFn),
// setting Cwd so the run driver opens there. It wires the harness for
// drift-regenerate (drift-only re-engagement) so a standalone playbook can regenerate a
// drifted diff block; the viewer keeps its authoring affordances off (DriftRegenOnly).
// opts carries the run-level fields (Shell + auto-rollback/assisted) seeded by RunMain
// and, for a project_bound source, ProjectRoot set by runFile.
func runViewer(file, cwd string, opts ui.Options) int {
	opts.File = file
	opts.Cwd = cwd
	// Thread the resolved project root (set by runFile for a project_bound
	// source, "" otherwise) so drift regen recalls the project knowledge set too.
	opts.Reengage = driftRegenReengage(opts.ProjectRoot)
	return uiRunFn(opts)
}

// runMode selects between the default (interactive viewer), headless (--auto),
// and GUIDED-fullscreen (--assisted) run paths. modeAssisted rides the SAME
// viewer path as modeDefault (runFile/runPlaybook) — only the plumbing (the
// Options.Assisted opt-in) differs; the assisted UI behavior itself is Plan 2's
// later tasks.
type runMode int

const (
	modeDefault runMode = iota
	modeAuto
	modeAssisted
)

// runArgs is the resolved `run` invocation: the single playbook source (Kind +
// Value) plus the run-mode/rollback opt-ins.
type runArgs struct {
	Kind, Value    string // "file"|"playbook", the path/slug
	Mode           runMode
	AutoRollback   bool              // existing default-viewer --auto-rollback opt-in
	NoAutoRollback bool              // --no-auto-rollback (valid only with --auto)
	Retry          bool              // --retry: resume the last failed run from its journal (composes with every mode)
	EnvOverrides   map[string]string // --with-env values (valid only with --auto)
}

// parseWithEnv resolves a --with-env flag value into a name→value map. A value
// whose first non-space rune is '{' is parsed as inline JSON; otherwise it is a
// path to a JSON file. The JSON must be an object of string→string. Malformed
// JSON, a non-string value, or an unreadable file is an error (the caller maps
// it to the exit-2 usage path).
func parseWithEnv(raw string) (map[string]string, error) {
	data := []byte(raw)
	if !strings.HasPrefix(strings.TrimLeft(raw, " \t\r\n"), "{") {
		b, err := os.ReadFile(raw)
		if err != nil {
			return nil, fmt.Errorf("--with-env: %v", err)
		}
		data = b
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("--with-env: invalid JSON: %v", err)
	}
	return m, nil
}

// resolveRunArgs resolves the single playbook source from the `run` arguments.
// Exactly one of {bare positional, --playbook, --file} must be present:
//
//   - --file <path>      → ("file", path)
//   - --playbook <slug>  → ("playbook", slug)
//   - a bare positional  → ("playbook", slug)  (implied --playbook)
//
// Zero sources or more than one is an error. When a slug is supplied both as a
// positional and via --playbook (or --file) it counts as two sources → an error,
// so the caller's intent is never ambiguous.
//
// --auto switches to the headless run mode (Mode: modeAuto); --no-auto-rollback
// is only meaningful there (an error otherwise), and --auto-rollback (the
// default-viewer opt-in) is mutually exclusive with --auto. --assisted switches
// to the GUIDED-fullscreen run mode (Mode: modeAssisted); it is mutually
// exclusive with --auto (headless and GUIDED-fullscreen are incompatible run
// modes) and with --auto-rollback (assisted mode owns post-failure flow via
// its own manual "Roll back" button; auto-rollback would fire out from under
// it). --no-auto-rollback being --auto-only is covered by the noAutoRollback
// && !autoMode check below, which assisted's --auto exclusion above already
// makes reachable-consistent.
func resolveRunArgs(args []string) (runArgs, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var withEnv string
	var auto, autoMode, noAutoRollback, assisted, retry bool
	fs.BoolVar(&auto, "auto-rollback", false, "on a step failure, automatically roll back applied steps (else a manual button)")
	fs.BoolVar(&autoMode, "auto", false, "run headless: execute every block in order with no viewer/driver pane")
	fs.BoolVar(&noAutoRollback, "no-auto-rollback", false, "with --auto, do not roll back applied steps on a failure")
	fs.BoolVar(&assisted, "assisted", false, "run GUIDED fullscreen: step-by-step confirmation in the same viewer/driver pane")
	fs.BoolVar(&retry, "retry", false, "resume the last failed run from its journal: blocks that succeeded are pre-seeded; execution resumes at the first failed/unrun block")
	fs.StringVar(&withEnv, "with-env", "", "with --auto, supply env var values as inline JSON or a JSON file path")
	kind, value, serr := resolveSource(fs, args, "run", true)
	if serr != nil {
		return runArgs{}, serr
	}

	if noAutoRollback && !autoMode {
		return runArgs{}, fmt.Errorf("--no-auto-rollback is only valid with --auto")
	}
	if autoMode && auto {
		return runArgs{}, fmt.Errorf("--auto and --auto-rollback are mutually exclusive (auto mode rolls back by default; use --no-auto-rollback to opt out)")
	}
	if assisted && autoMode {
		return runArgs{}, fmt.Errorf("--assisted and --auto are mutually exclusive")
	}
	if assisted && auto {
		return runArgs{}, fmt.Errorf("--assisted and --auto-rollback are mutually exclusive")
	}
	if withEnv != "" && !autoMode {
		return runArgs{}, fmt.Errorf("--with-env is only valid with --auto")
	}

	ra := runArgs{Kind: kind, Value: value, AutoRollback: auto, NoAutoRollback: noAutoRollback, Retry: retry}
	switch {
	case autoMode:
		ra.Mode = modeAuto
	case assisted:
		ra.Mode = modeAssisted
	}
	if withEnv != "" {
		overrides, perr := parseWithEnv(withEnv)
		if perr != nil {
			return runArgs{}, perr
		}
		ra.EnvOverrides = overrides
	}
	return ra, nil
}
