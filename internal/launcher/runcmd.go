// runcmd.go — the `ai-playbook run` subcommand entrypoint and its argument
// resolution.
//
// `run` accepts a single playbook source, expressed one of three ways:
//
//   - run <slug>            a bare positional ⇒ implied --playbook <slug>
//   - run --playbook <slug> a saved playbook resolved through the store
//   - run --file <path>     a raw markdown file rendered as-is
//
// Exactly one source must be given; zero or more than one is an error. A
// --playbook/slug source resolves the slug to its file via store.PathFor and
// renders it; a --file source renders the file directly. Both shapes drive the
// pager through the existing `run --file <path>` reshape + ui.Main (via the
// uiMainFn seam, shared with storecmd.go, so RunMain's resolution + routing are
// unit-testable without a TTY).
//
// Internal callers (serveCachedPlaybook, AnswerMain) do NOT go through RunMain:
// they reshape os.Args to `run --file <tmp>` and call ui.Main directly, so they
// bypass adapt-on-run (their temp files carry no front matter and render as-is).
package launcher

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/ui"
)

// RunMain is the `ai-playbook run` subcommand: it owns config loading + the
// configured-shell hand-off (ui stays config-agnostic), resolves the run
// argument, and renders the resolved playbook through ui.Main (via uiMainFn).
func RunMain() int {
	// cfg is always non-nil (config.Load returns Default on error). The `run`
	// subcommand opens its own driver, so honor the configured shell — ui stays
	// config-agnostic and receives the selector as DATA via SetShell.
	cfg, _ := config.Load()
	ui.SetShell(cfg.Driver.Shell)

	kind, value, err := resolveRunArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook run: %v\n", err)
		return 2
	}

	var path string
	switch kind {
	case "file":
		// A raw file renders as-is (no front matter → no adapt).
		path = value
	case "playbook":
		// A slug resolves to its store file. Task 9 plugs adapt-on-run in here:
		// TODO(task9): adapt-on-run here before render — resolve the playbook's
		// workdir (ask via the float when empty/missing), run one adapt authoring
		// pass, junk-guard the result, then render the adapted body. For now the
		// slug resolves to its file and renders as-is via the `run --file` path.
		p, ok := pathForFn(value)
		if !ok {
			fmt.Fprintf(os.Stderr, "ai-playbook run: no playbook for slug %q\n", value)
			return 1
		}
		path = p
	}

	// Reshape os.Args to the `run --file <path>` form ui.Main parses, and render.
	saved := os.Args
	os.Args = []string{os.Args[0], "run", "--file", path}
	code := uiMainFn()
	os.Args = saved
	return code
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
func resolveRunArgs(args []string) (kind, value string, err error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var playbook, file string
	fs.StringVar(&playbook, "playbook", "", "slug of a saved playbook to run")
	fs.StringVar(&file, "file", "", "path to a markdown file to run")
	if perr := fs.Parse(args); perr != nil {
		return "", "", perr
	}
	// The stdlib flag package stops at the FIRST non-flag token, so anything after a
	// bare positional (e.g. `run build --file x`) lands here unparsed. Treat any
	// leftover beyond the single positional as a conflict — the source must be
	// unambiguous.
	rest := fs.Args()
	if len(rest) > 1 {
		return "", "", fmt.Errorf("specify exactly one of <slug>, --playbook, or --file")
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
		return "", "", fmt.Errorf("specify a playbook: run <slug> | --playbook <slug> | --file <path>")
	case count > 1:
		return "", "", fmt.Errorf("specify exactly one of <slug>, --playbook, or --file")
	case file != "":
		return "file", file, nil
	case playbook != "":
		return "playbook", playbook, nil
	default:
		return "playbook", positional, nil
	}
}
