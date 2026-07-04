// source.go — the shared "single playbook source" argument resolution used by
// `run`, `env`, and `validate`: each accepts exactly one of {bare positional,
// --playbook (run only), --file}, and zero or more than one is a usage error.
package launcher

import (
	"flag"
	"fmt"
)

// resolveSource resolves a subcommand's single playbook source from args.
// It registers --file (and, when allowPlaybookFlag, --playbook) on fs, parses
// args, and returns ("file"|"playbook", value, nil) — or a usage error. The
// caller registers any of its OWN flags on fs before calling resolveSource (flag
// registration order doesn't matter; only Parse must run once, which
// resolveSource itself does).
//
// cmdName names the subcommand in the usage-error text ("specify a playbook:
// <cmdName> <slug> | ..."). allowPlaybookFlag selects run's three-way wording
// (<slug>, --playbook, or --file) vs env/validate's two-way wording (<slug> or
// --file) — the three commands' error TEXTS differ by more than the command
// name, so wording is NOT unified, only the command name is parameterized.
//
// A slug supplied both as a positional and via --playbook/--file counts as two
// sources (an error), so the caller's intent is never ambiguous.
func resolveSource(fs *flag.FlagSet, args []string, cmdName string, allowPlaybookFlag bool) (kind, value string, err error) {
	var file string
	fs.StringVar(&file, "file", "", "path to a markdown file")
	var playbookFlag string
	if allowPlaybookFlag {
		fs.StringVar(&playbookFlag, "playbook", "", "slug of a saved playbook to run")
	}
	if perr := fs.Parse(args); perr != nil {
		return "", "", perr
	}

	oneOf := "<slug> or --file"
	if allowPlaybookFlag {
		oneOf = "<slug>, --playbook, or --file"
	}

	// The stdlib flag package stops at the FIRST non-flag token, so anything after
	// a bare positional (e.g. `run build --file x`) lands here unparsed. Treat any
	// leftover beyond the single positional as a conflict — the source must be
	// unambiguous.
	rest := fs.Args()
	if len(rest) > 1 {
		return "", "", fmt.Errorf("specify exactly one of %s", oneOf)
	}
	positional := ""
	if len(rest) == 1 {
		positional = rest[0]
	}

	sources := []string{file, positional}
	if allowPlaybookFlag {
		sources = []string{playbookFlag, file, positional}
	}
	count := 0
	for _, s := range sources {
		if s != "" {
			count++
		}
	}
	switch {
	case count == 0:
		if allowPlaybookFlag {
			return "", "", fmt.Errorf("specify a playbook: %s <slug> | --playbook <slug> | --file <path>", cmdName)
		}
		return "", "", fmt.Errorf("specify a playbook: %s <slug> | --file <path>", cmdName)
	case count > 1:
		return "", "", fmt.Errorf("specify exactly one of %s", oneOf)
	}

	switch {
	case file != "":
		return "file", file, nil
	case allowPlaybookFlag && playbookFlag != "":
		return "playbook", playbookFlag, nil
	default:
		return "playbook", positional, nil
	}
}
