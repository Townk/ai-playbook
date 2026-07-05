// Package skillcmd implements the public `skill` verb: it prints or installs
// the embedded playbook-authoring skill (skills.PlaybookAuthoring — the
// harness-agnostic authoring SKILL derived from
// docs/specifications/playbook-authoring.md).
//
//	skill show                          print the SKILL to stdout
//	skill install [--to <dir>] [--force]  install <dir>/playbook-authoring/SKILL.md
//
// install defaults to ~/.claude/skills (the Claude Code personal skills
// directory); --to <dir> targets any other harness's skills root. An existing
// installed file is never overwritten without --force.
package skillcmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Townk/ai-playbook/skills"
)

// userHomeDir resolves the user's home directory for the default install
// target. Seam for tests (production: os.UserHomeDir).
var userHomeDir = os.UserHomeDir

// Main is the `ai-playbook skill <show|install>` entrypoint consumed by the
// cli dispatch table.
func Main() int { return run(os.Args[2:], os.Stdout, os.Stderr) }

// run dispatches the sub-subcommand. A missing or unrecognized subcommand is
// a usage error (exit 2) — the kb verb's convention.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ai-playbook skill: a subcommand is required (show|install)")
		return 2
	}
	switch args[0] {
	case "show":
		return showMain(args[1:], stdout, stderr)
	case "install":
		return installMain(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ai-playbook skill: unknown subcommand %q (want show|install)\n", args[0])
		return 2
	}
}

// showMain implements `skill show`: the embedded SKILL, verbatim, to stdout —
// pipeable anywhere (a pager, another harness's skill importer, a file).
func showMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skill show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "ai-playbook skill show: unexpected argument(s)")
		return 2
	}
	if _, err := stdout.Write(skills.PlaybookAuthoring); err != nil {
		fmt.Fprintf(stderr, "ai-playbook skill show: %v\n", err)
		return 1
	}
	return 0
}

// installMain implements `skill install [--to <dir>] [--force]`: it writes
// the embedded SKILL to <dir>/playbook-authoring/SKILL.md (default <dir>:
// ~/.claude/skills), creating directories as needed, refusing to overwrite an
// existing file without --force, and printing the installed path on success.
func installMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skill install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var to string
	var force bool
	fs.StringVar(&to, "to", "", "install under this skills directory instead of ~/.claude/skills")
	fs.BoolVar(&force, "force", false, "overwrite an already-installed SKILL.md")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "ai-playbook skill install: unexpected argument(s)")
		return 2
	}

	root := to
	if root == "" {
		home, err := userHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "ai-playbook skill install: resolve home directory: %v\n", err)
			return 1
		}
		root = filepath.Join(home, ".claude", "skills")
	}
	target := filepath.Join(root, "playbook-authoring", "SKILL.md")

	if !force {
		if _, err := os.Lstat(target); err == nil {
			fmt.Fprintf(stderr, "ai-playbook skill install: %s already exists — re-run with --force to overwrite\n", target)
			return 1
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(stderr, "ai-playbook skill install: %v\n", err)
		return 1
	}
	if err := os.WriteFile(target, skills.PlaybookAuthoring, 0o644); err != nil {
		fmt.Fprintf(stderr, "ai-playbook skill install: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, target)
	return 0
}
