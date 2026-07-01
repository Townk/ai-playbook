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
// ui.SetProjectRoot (which exports PROJECT_ROOT=<root>), and opens the driver
// there (--cwd). A non-project_bound source renders with no PROJECT_ROOT. A raw
// --file with no front matter renders as-is.
//
// Internal callers (serveCachedPlaybook, AnswerMain) do NOT go through RunMain:
// they reshape os.Args to `run --file <tmp>` and call ui.Main directly.
package launcher

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Townk/ai-playbook/internal/autorun"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/frontmatter"
	"github.com/Townk/ai-playbook/internal/store"
	"github.com/Townk/ai-playbook/internal/ui"
)

// storeLoadFn is the store.Load seam: resolves a slug to its Meta + body. Tests
// inject a fake so the run gate is exercised without a real store.
var storeLoadFn = store.Load

// projectRootFn is the capture.ProjectRoot seam: the heuristic project root a
// project_bound playbook is run in (and exported as $PROJECT_ROOT).
var projectRootFn = capture.ProjectRoot

// setProjectRootFn injects the run driver's PROJECT_ROOT (the heuristic project
// root) for a project_bound playbook. Seam so tests observe it without a viewer.
var setProjectRootFn = ui.SetProjectRoot

// setReengageFn stashes the run-viewer's drift-regen re-engagement context (the harness
// wiring for regenerating a drifted diff). Seam so tests observe the wiring without a viewer.
var setReengageFn = ui.SetReengage

// setAutoRollbackFn stashes the --auto-rollback opt-in for the next viewer. Seam so tests
// observe it without a viewer.
var setAutoRollbackFn = ui.SetAutoRollback

// setAssistedFn stashes the --assisted opt-in for the next viewer. Seam so tests
// observe it without a viewer.
var setAssistedFn = ui.SetAssisted

// autorunRunFn is the autorun.Run seam: the headless (`--auto`) run executes the
// converted block sequence without a driver/viewer. Tests inject a fake so the
// auto branch is exercised without opening a real shell.
var autorunRunFn = autorun.Run

// RunMain is the `ai-playbook run` subcommand: it owns config loading + the
// configured-shell hand-off (ui stays config-agnostic), resolves the run
// argument, and renders the resolved playbook through ui.Main (via uiMainFn). A
// project_bound source sets PROJECT_ROOT on the run driver before it renders.
func RunMain() int {
	// cfg is always non-nil (config.Load returns Default on error). The `run`
	// subcommand opens its own driver, so honor the configured shell — ui stays
	// config-agnostic and receives the selector as DATA via SetShell.
	cfg, _ := config.Load()
	ui.SetShell(cfg.Driver.Shell)

	ra, err := resolveRunArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 2
	}

	if ra.Mode == modeAuto {
		return autoRun(ra) // headless: never opens ui.Main / a driver pane
	}

	setAutoRollbackFn(ra.AutoRollback) // opt-in: auto-fire rollback on a step failure
	if ra.Mode == modeAssisted {
		setAssistedFn(true) // opt-in: GUIDED fullscreen mode rides the same viewer path as default
	}

	switch ra.Kind {
	case "file":
		return runFile(ra.Value)
	case "playbook":
		return runPlaybook(ra.Value)
	}
	return 0
}

// runPlaybook resolves a slug through the store and renders its stored body as-is.
// A project_bound playbook resolves the heuristic project root, exports it as the
// run driver's PROJECT_ROOT (setProjectRootFn), and opens the driver there.
func runPlaybook(slug string) int {
	meta, body, lerr := storeLoadFn(slug)
	if lerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", lerr)
		return 1
	}
	cwd := ""
	if meta.ProjectBound {
		root := projectRootFn()
		setProjectRootFn(root) // the run driver exports PROJECT_ROOT=<root>
		cwd = root
	}
	return renderStored(body, cwd)
}

// runFile renders a markdown file through the `run --file` viewer. The ORIGINAL file
// is always what ui.Main renders — ui.Main strips any front matter for display AND
// extracts the declared env map for the confirmation gate, so we must NOT pre-strip it
// (an earlier temp-file round-trip did, which silently discarded both the run cwd and
// the env map). A project_bound file resolves its project root (declared project_root
// relative to the heuristic repo root, else the repo root itself), exports it as
// PROJECT_ROOT, and opens there; a plain front-matter file opens in the file's own
// directory; a raw file with no front matter renders as-is in the invocation cwd.
func runFile(file string) int {
	data, rerr := os.ReadFile(file)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", rerr)
		return 1
	}
	fm, _, ok := frontmatter.Parse(string(data))
	if !ok {
		// No front matter → render as-is (cwd derived from the file by ui.Main).
		return runViewer(file, "")
	}
	cwd := ""
	if fm.ProjectBound {
		root := resolveProjectRoot(fm.ProjectRoot)
		setProjectRootFn(root) // the run driver exports PROJECT_ROOT=<root>
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
	return runViewer(file, cwd)
}

// autoRun executes ra headlessly (`run --auto`): it resolves the same source
// (markdown + cwd + PROJECT_ROOT) that runFile/runPlaybook would open a viewer
// on, parses the front-matter-stripped body into blocks with the SAME parser
// the viewer uses (ui.Render), converts them to autorun.Block, and hands the
// sequence to autorunRunFn. No viewer/driver pane is ever opened.
func autoRun(ra runArgs) int {
	cfg, _ := config.Load()

	var body, cwd, slug string
	var fm frontmatter.FrontMatter

	switch ra.Kind {
	case "file":
		data, rerr := os.ReadFile(ra.Value)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", rerr)
			return 1
		}
		var ok bool
		fm, body, ok = frontmatter.Parse(string(data))
		if ok && fm.ProjectBound {
			root := resolveProjectRoot(fm.ProjectRoot)
			os.Setenv("PROJECT_ROOT", root) // mirrors setProjectRootFn's driver export
			cwd = root
		} else if dir := filepath.Dir(ra.Value); dir != "" {
			// Mirrors the `run --file` cwd rule (runFile / ui.Main's --cwd default):
			// blocks run in the playbook file's own directory.
			if abs, aerr := filepath.Abs(dir); aerr == nil {
				cwd = abs
			} else {
				cwd = dir
			}
		}
		base := filepath.Base(ra.Value)
		slug = strings.TrimSuffix(base, filepath.Ext(base))
	case "playbook":
		meta, b, lerr := storeLoadFn(ra.Value)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", lerr)
			return 1
		}
		body = b
		if meta.ProjectBound {
			root := projectRootFn()
			os.Setenv("PROJECT_ROOT", root)
			cwd = root
		}
		slug = ra.Value
	}

	// Pass the front-matter-stripped body — NOT the raw source — so the YAML
	// fence never gets mis-parsed as a code block.
	_, _, uiBlocks := ui.Render(body, 80, nil, "")
	blocks := make([]autorun.Block, 0, len(uiBlocks))
	for _, b := range uiBlocks {
		blocks = append(blocks, autorun.Block{
			ID:       b.ID,
			Command:  b.Payload,
			Needs:    b.Needs,
			Rollback: b.Rollback,
			Static:   b.Static,
			Kind:     kindFromType(b.Type),
		})
	}

	return autorunRunFn(autorun.RunConfig{
		Blocks:       blocks,
		EnvVars:      fm.Env,
		Cwd:          cwd,
		Shell:        cfg.Driver.Shell,
		Slug:         slug,
		AutoRollback: !ra.NoAutoRollback,
	})
}

// kindFromType maps a ui.Block's Type tag to autorun's StepKind: "diff" →
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

// runViewer renders file through the `run --file` viewer (ui.Main via uiMainFn),
// passing --cwd when non-empty so the run driver opens there. It wires the harness for
// drift-regenerate (drift-only re-engagement) so a standalone playbook can regenerate a
// drifted diff block; the viewer keeps its authoring affordances off (DriftRegenOnly).
func runViewer(file, cwd string) int {
	setReengageFn(driftRegenReengage())
	saved := os.Args
	args := []string{os.Args[0], "run", "--file", file}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	os.Args = args
	code := uiMainFn()
	os.Args = saved
	return code
}

// renderStored writes body to a temp file and runs it via the `run --file` viewer
// (no adapt, no banner). cwd (the project root for a project_bound run, else "")
// is passed as --cwd so the driver opens there.
func renderStored(body, cwd string) int {
	f, err := writeTempMarkdown("playbook", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 1
	}
	saved := os.Args
	args := []string{os.Args[0], "run", "--file", f}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	os.Args = args
	code := uiMainFn()
	os.Args = saved
	return code
}

// writeTempMarkdown writes content to a temp *.md file and returns its path. The
// file is left for the OS /tmp reap — ui.Main reads it after this returns.
func writeTempMarkdown(tag, content string) (string, error) {
	f, err := os.CreateTemp("", "ai-playbook-"+tag+"-*.md")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, werr := f.WriteString(content); werr != nil {
		f.Close()
		os.Remove(name)
		return "", werr
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(name)
		return "", cerr
	}
	return name, nil
}

// runMode selects between the default (interactive viewer), headless (--auto),
// and GUIDED-fullscreen (--assisted) run paths. modeAssisted rides the SAME
// viewer path as modeDefault (runFile/runPlaybook) — only the plumbing (the
// setAssistedFn opt-in) differs; the assisted UI behavior itself is Plan 2's
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
	AutoRollback   bool // existing default-viewer --auto-rollback opt-in
	NoAutoRollback bool // --no-auto-rollback (valid only with --auto)
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
// modes), and with --no-auto-rollback (that flag is --auto-only).
func resolveRunArgs(args []string) (runArgs, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var playbook, file string
	var auto, autoMode, noAutoRollback, assisted bool
	fs.StringVar(&playbook, "playbook", "", "slug of a saved playbook to run")
	fs.StringVar(&file, "file", "", "path to a markdown file to run")
	fs.BoolVar(&auto, "auto-rollback", false, "on a step failure, automatically roll back applied steps (else a manual button)")
	fs.BoolVar(&autoMode, "auto", false, "run headless: execute every block in order with no viewer/driver pane")
	fs.BoolVar(&noAutoRollback, "no-auto-rollback", false, "with --auto, do not roll back applied steps on a failure")
	fs.BoolVar(&assisted, "assisted", false, "run GUIDED fullscreen: step-by-step confirmation in the same viewer/driver pane")
	if perr := fs.Parse(args); perr != nil {
		return runArgs{}, perr
	}
	// The stdlib flag package stops at the FIRST non-flag token, so anything after a
	// bare positional (e.g. `run build --file x`) lands here unparsed. Treat any
	// leftover beyond the single positional as a conflict — the source must be
	// unambiguous.
	rest := fs.Args()
	if len(rest) > 1 {
		return runArgs{}, fmt.Errorf("specify exactly one of <slug>, --playbook, or --file")
	}
	positional := ""
	if len(rest) == 1 {
		positional = rest[0]
	}

	count := 0
	for _, s := range []string{playbook, file, positional} {
		if s != "" {
			count++
		}
	}
	switch {
	case count == 0:
		return runArgs{}, fmt.Errorf("specify a playbook: run <slug> | --playbook <slug> | --file <path>")
	case count > 1:
		return runArgs{}, fmt.Errorf("specify exactly one of <slug>, --playbook, or --file")
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
	if assisted && noAutoRollback {
		return runArgs{}, fmt.Errorf("--no-auto-rollback is only valid with --auto")
	}

	ra := runArgs{AutoRollback: auto, NoAutoRollback: noAutoRollback}
	switch {
	case autoMode:
		ra.Mode = modeAuto
	case assisted:
		ra.Mode = modeAssisted
	}
	switch {
	case file != "":
		ra.Kind, ra.Value = "file", file
	case playbook != "":
		ra.Kind, ra.Value = "playbook", playbook
	default:
		ra.Kind, ra.Value = "playbook", positional
	}
	return ra, nil
}
