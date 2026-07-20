// Package docscmd implements the `completion` and `man` verbs: self-serve
// installation of the zsh completions and man pages for users who installed
// via `go install` (which ships neither). Content is rendered at RUNTIME from
// the same registries docgen generates the release artifacts from (climeta for
// ai-playbook/apb, askcli for ask), so the installed docs always match the
// running binary — no embedded copies to go stale.
//
// Both verbs mirror the `skill` verb's shape: <show|install|uninstall> with
// `--to <dir>` / `--force`. Install refuses to overwrite existing files
// without --force; uninstall removes exactly the files install writes and is
// idempotent (missing files are fine) — the contract a package-manager
// post-(un)install hook needs.
package docscmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Townk/ai-playbook/internal/askcli"
	"github.com/Townk/ai-playbook/internal/climeta"
)

// userHomeDir is the home-dir seam (production: os.UserHomeDir), mirroring
// skillcmd's test seam.
var userHomeDir = os.UserHomeDir

// CompletionMain is the `ai-playbook completion <show|install|uninstall>`
// entrypoint consumed by the cli dispatch table.
func CompletionMain() int { return runCompletion(os.Args[2:], os.Stdout, os.Stderr) }

// ManMain is the `ai-playbook man <install|uninstall>` entrypoint consumed by
// the cli dispatch table.
func ManMain() int { return runMan(os.Args[2:], os.Stdout, os.Stderr) }

// dataHome resolves the XDG data home (the default install roots live under
// it): $XDG_DATA_HOME, else <home>/.local/share.
func dataHome() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return x, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// completionFiles renders the completion scripts from the live registries:
// _ai-playbook covers both the ai-playbook and apb spellings, _ask the ask
// binary — the same two files the release archives ship.
func completionFiles() map[string]string {
	return map[string]string{
		"_ai-playbook": climeta.Zsh(),
		"_ask":         askcli.Zsh(),
	}
}

// manFiles renders every man page from the live registries: the ai-playbook
// overview, one page per documented command, and ask's single page — the same
// set the release archives ship.
func manFiles() map[string]string {
	files := map[string]string{
		"ai-playbook.1": climeta.ManOverview(),
		"ask.1":         askcli.Man(),
	}
	for _, cmd := range climeta.DocumentedCommands() {
		files[fmt.Sprintf("ai-playbook-%s.1", cmd.Name)] = climeta.Man(cmd)
	}
	return files
}

// runCompletion dispatches the completion sub-subcommand. A missing or
// unrecognized subcommand is a usage error (exit 2).
func runCompletion(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ai-playbook completion: a subcommand is required (show|install|uninstall)")
		return 2
	}
	defaultDir := func() (string, error) {
		d, err := dataHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, "zsh", "site-functions"), nil
	}
	switch args[0] {
	case "show":
		fs := flag.NewFlagSet("completion show", flag.ContinueOnError)
		fs.SetOutput(stderr)
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() > 0 {
			fmt.Fprintln(stderr, "ai-playbook completion show: unexpected argument(s)")
			return 2
		}
		fmt.Fprint(stdout, climeta.Zsh())
		return 0
	case "install":
		return installFiles("completion", args[1:], defaultDir, completionFiles(), stdout, stderr)
	case "uninstall":
		return uninstallFiles("completion", args[1:], defaultDir, completionFiles(), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ai-playbook completion: unknown subcommand %q (want show|install|uninstall)\n", args[0])
		return 2
	}
}

// runMan dispatches the man sub-subcommand. A missing or unrecognized
// subcommand is a usage error (exit 2).
func runMan(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ai-playbook man: a subcommand is required (install|uninstall)")
		return 2
	}
	defaultDir := func() (string, error) {
		d, err := dataHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, "man", "man1"), nil
	}
	switch args[0] {
	case "install":
		return installFiles("man", args[1:], defaultDir, manFiles(), stdout, stderr)
	case "uninstall":
		return uninstallFiles("man", args[1:], defaultDir, manFiles(), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ai-playbook man: unknown subcommand %q (want install|uninstall)\n", args[0])
		return 2
	}
}

// resolveDir parses the shared --to/--force flags and resolves the target
// directory (--to wins; else defaultDir).
func resolveDir(verb string, args []string, defaultDir func() (string, error), stderr io.Writer) (dir string, force bool, code int) {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var to string
	fs.StringVar(&to, "to", "", "target directory")
	fs.BoolVar(&force, "force", false, "overwrite existing files")
	if err := fs.Parse(args); err != nil {
		return "", false, 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "ai-playbook %s: unexpected argument(s)\n", verb)
		return "", false, 2
	}
	dir = to
	if dir == "" {
		d, err := defaultDir()
		if err != nil {
			fmt.Fprintf(stderr, "ai-playbook %s: cannot resolve the default directory: %v\n", verb, err)
			return "", false, 1
		}
		dir = d
	}
	return dir, force, 0
}

// sortedNames returns files' keys sorted, for deterministic output and errors.
func sortedNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// installFiles writes every rendered file into dir (created as needed). An
// existing target is never overwritten without --force — the check runs over
// ALL files first so a partial install can't half-overwrite.
func installFiles(kind string, args []string, defaultDir func() (string, error), files map[string]string, stdout, stderr io.Writer) int {
	verb := kind + " install"
	dir, force, code := resolveDir(verb, args, defaultDir, stderr)
	if code != 0 {
		return code
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "ai-playbook %s: %v\n", verb, err)
		return 1
	}
	names := sortedNames(files)
	if !force {
		for _, name := range names {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				fmt.Fprintf(stderr, "ai-playbook %s: %s already exists (use --force to overwrite)\n", verb, filepath.Join(dir, name))
				return 1
			}
		}
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(files[name]), 0o644); err != nil {
			fmt.Fprintf(stderr, "ai-playbook %s: %v\n", verb, err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "installed %d %s file(s) → %s\n", len(names), kind, dir)
	return 0
}

// uninstallFiles removes exactly the files installFiles writes. Idempotent:
// files already absent are skipped silently (a post-uninstall hook may run
// after a partial or repeated removal), and the directory itself is left in
// place.
func uninstallFiles(kind string, args []string, defaultDir func() (string, error), files map[string]string, stdout, stderr io.Writer) int {
	verb := kind + " uninstall"
	dir, _, code := resolveDir(verb, args, defaultDir, stderr)
	if code != 0 {
		return code
	}
	removed := 0
	for _, name := range sortedNames(files) {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(stderr, "ai-playbook %s: %v\n", verb, err)
			return 1
		}
		removed++
	}
	fmt.Fprintf(stdout, "removed %d %s file(s) from %s\n", removed, kind, dir)
	return 0
}
