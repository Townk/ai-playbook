// ai-playbook — unified terminal AI-assist / playbook binary.
//
// Subcommands (git-style; the binary self-spawns for floats/panes):
//
//	assist         AI producer: capture → triage → author a playbook → drive it
//	               (troubleshoot is a deprecated alias)
//	create <prompt> author a playbook directly (force-author; no triage/cache serve)
//	run <file.md>  playbook runtime: render + orchestrate a playbook artifact
//	input          the multi-line input widget
//	selftest       drive the user's real shell and report (validates the driver)
//
// Stage 1 ships the driver core + selftest; the rest are stubs filled in by the
// strangler migration (see docs/superpowers/specs/2026-06-24-ai-playbook-unification-design.md).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Townk/ai-playbook/internal/climeta"
	diffpkg "github.com/Townk/ai-playbook/internal/diff"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/internal/launcher"
	"github.com/Townk/ai-playbook/internal/mcpserver"
)

// version is the binary's version string. It defaults to "dev" for local
// builds; GoReleaser injects the real tag via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if text, handled := helpFor(os.Args[1:]); handled {
		fmt.Println(text)
		os.Exit(0)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("ai-playbook %s\n", version)
		os.Exit(0)
	case "selftest":
		os.Exit(selftest())
	case "assist":
		os.Exit(launcher.Assist())
	case "troubleshoot": // deprecated alias → assist (the ZLE trigger is repointed)
		os.Exit(launcher.Assist())
	case "create":
		os.Exit(launcher.CreateMain())
	case "list":
		os.Exit(launcher.ListMain())
	case "search":
		os.Exit(launcher.SearchMain())
	case "show":
		os.Exit(launcher.ShowMain())
	case "edit":
		os.Exit(launcher.EditMain())
	case "session":
		os.Exit(launcher.SessionMain())
	case "run":
		// RunMain owns config loading + the configured-shell hand-off and resolves
		// the --playbook/--file/bare argument before rendering via ui.Main.
		os.Exit(launcher.RunMain())
	case "validate":
		os.Exit(launcher.ValidateMain())
	case "env":
		os.Exit(launcher.EnvMain())
	case "answer":
		os.Exit(launcher.AnswerMain())
	case "finalize":
		os.Exit(finalize())
	case "mcp":
		os.Exit(mcpMain())
	case "diff":
		os.Exit(diffpkg.Main())
	case "input":
		os.Exit(input.Main())
	default:
		fmt.Fprintf(os.Stderr, "ai-playbook: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// helpFor is the pure top-level help-dispatch decision, factored out of
// main() (which calls os.Exit and so cannot itself be unit-tested) so it can
// be tested directly. args is os.Args[1:]. It returns the help text to print
// and whether help was requested at all — callers print text to stdout and
// exit 0 when handled is true, and otherwise proceed with normal dispatch.
//
// Dispatch rules:
//   - no args: handled=false — the true no-args case is main()'s own
//     pre-existing error path (usage to stderr, exit 2), not this function's
//     concern.
//   - a bare "-h"/"--help"/"help": climeta.Overview().
//   - "help <cmd>" or "--help <cmd>": climeta.Help(<cmd>) if <cmd> is known;
//     if unknown, still handled (falls back to Overview()) so it never falls
//     through to normal dispatch.
//   - "<cmd> ...args..." where a bare -h/--help token appears anywhere in
//     args: climeta.Help(<cmd>) — so <cmd>'s own flag.FlagSet never sees it.
//   - anything else: handled=false, normal dispatch proceeds.
func helpFor(args []string) (text string, handled bool) {
	if len(args) == 0 {
		return "", false
	}

	switch args[0] {
	case "-h", "--help", "help":
		if len(args) < 2 {
			return climeta.Overview(), true
		}
		if help, ok := climeta.Help(args[1]); ok {
			return help, true
		}
		return climeta.Overview(), true
	default:
		if wantsHelp(args[1:]) {
			if help, ok := climeta.Help(args[0]); ok {
				return help, true
			}
			return climeta.Overview(), true
		}
		return "", false
	}
}

// wantsHelp reports whether a bare "-h" or "--help" token appears anywhere in
// args. It matches only exact tokens, never a substring of a flag's own name
// or value (e.g. "--help-me-please" does not match).
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func usage() {
	fmt.Fprintln(os.Stderr, climeta.Overview())
}

// mcpMain is the `ai-playbook mcp --socket <path>` subcommand: an MCP stdio
// server (the claude harness adapter) whose tool calls dial the session's tools
// backend at <path>. claude launches this via --mcp-config; it forwards run /
// remember / ask to the unix socket. Blocks until the client disconnects.
func mcpMain() int {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	var socket string
	fs.StringVar(&socket, "socket", "", "path to the session's tools-backend unix socket")
	argv := os.Args[2:]
	_ = fs.Parse(argv) // flag.ExitOnError: Parse never returns a non-nil error
	if socket == "" {
		fmt.Fprintln(os.Stderr, "ai-playbook mcp: --socket <path> is required")
		return 2
	}
	if err := mcpserver.Run(socket); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook mcp: %v\n", err)
		return 1
	}
	return 0
}

// selftest drives the user's REAL shell (unaltered) and reports — the live
// counterpart to the package's deterministic tests.
func selftest() int {
	say := func(f string, a ...any) { fmt.Printf("selftest> "+f+"\n", a...) }
	fails := 0
	chk := func(name string, ok bool, detail string) {
		if ok {
			say("  PASS — %s", name)
		} else {
			say("  FAIL — %s (%s)", name, detail)
			fails++
		}
	}

	// selftest is intentionally pinned to the zsh default (Shell:"") — its checks
	// below are zsh-specific (`print -r --`, mise auto-env on cd) and validate the
	// zsh fidelity path, not the configurable runtime. It is not part of the
	// assist/escalate flow, so cfg.Driver.Shell does not apply here.
	d, err := driver.Open(driver.Options{})
	if err != nil {
		say("FATAL: %v", err)
		return 1
	}
	defer d.Close()
	say("driver up: real zsh -il, unaltered")

	have := func(name string) bool { return d.Run("command -v "+name+" >/dev/null 2>&1", 5*time.Second).Exit == 0 }
	home, _ := os.UserHomeDir()

	// interactive env
	if app := filepath.Join(home, "Projects/platforms/android/SampleApp1"); dirExists(app) {
		r := d.Run("builtin cd -- "+app+"; gg build 2>&1", 30*time.Second)
		say("  'gg build' → exit=%d out=%q", r.Exit, head(r.Out, 70))
		chk("gg resolves (not command-not-found)", !strings.Contains(r.Out, "not found"), r.Out)
	}

	// auto-env on cd
	if have("mise") {
		dir, _ := os.MkdirTemp("", "selftest-mise")
		defer os.RemoveAll(dir)
		// Best-effort fixture write; a failure surfaces as the chk below missing SELFTEST_MISE.
		_ = os.WriteFile(filepath.Join(dir, "mise.toml"), []byte("[env]\nSELFTEST_MISE = \"mise-works\"\n"), 0644)
		d.Run("mise trust "+dir+" 2>/dev/null || true", 10*time.Second)
		d.Run("builtin cd -- "+dir, 10*time.Second)
		r := d.Run("print -r -- ${SELFTEST_MISE:-MISSING}", 10*time.Second)
		chk("mise [env] on cd", r.Out == "mise-works", r.Out)
		d.Run("builtin cd -- /tmp", 5*time.Second)
	} else {
		say("  (mise not installed — skipping auto-env check)")
	}

	// capture, persistence, kill
	r := d.Run("print -r -- o; print -ru2 -- e; (exit 7)", 10*time.Second)
	chk("stdout/stderr/exit", r.Out == "o" && r.Err == "e" && r.Exit == 7, fmt.Sprintf("%+v", r))
	d.Run("builtin cd -- /tmp", 5*time.Second)
	chk("cd persists", d.Run("pwd", 5*time.Second).Out == "/tmp", "")
	chk("timeout kills + survives", d.Run("sleep 30", 2*time.Second).TimedOut && d.Run("echo alive", 5*time.Second).Out == "alive", "")

	say("")
	if fails == 0 {
		say("RESULT: ALL PASS")
		return 0
	}
	say("RESULT: %d FAILED", fails)
	return 1
}
func dirExists(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
func head(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}
