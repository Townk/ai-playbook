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

	kind, value, autoRollback, err := resolveRunArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 2
	}
	setAutoRollbackFn(autoRollback) // opt-in: auto-fire rollback on a step failure

	switch kind {
	case "file":
		return runFile(value)
	case "playbook":
		return runPlaybook(value)
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
func resolveRunArgs(args []string) (kind, value string, autoRollback bool, err error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var playbook, file string
	var auto bool
	fs.StringVar(&playbook, "playbook", "", "slug of a saved playbook to run")
	fs.StringVar(&file, "file", "", "path to a markdown file to run")
	fs.BoolVar(&auto, "auto-rollback", false, "on a step failure, automatically roll back applied steps (else a manual button)")
	if perr := fs.Parse(args); perr != nil {
		return "", "", false, perr
	}
	// The stdlib flag package stops at the FIRST non-flag token, so anything after a
	// bare positional (e.g. `run build --file x`) lands here unparsed. Treat any
	// leftover beyond the single positional as a conflict — the source must be
	// unambiguous.
	rest := fs.Args()
	if len(rest) > 1 {
		return "", "", false, fmt.Errorf("specify exactly one of <slug>, --playbook, or --file")
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
		return "", "", false, fmt.Errorf("specify a playbook: run <slug> | --playbook <slug> | --file <path>")
	case count > 1:
		return "", "", false, fmt.Errorf("specify exactly one of <slug>, --playbook, or --file")
	case file != "":
		return "file", file, auto, nil
	case playbook != "":
		return "playbook", playbook, auto, nil
	default:
		return "playbook", positional, auto, nil
	}
}
