// ai-playbook — unified terminal AI-assist / playbook binary.
//
// Subcommands (git-style; the binary self-spawns for floats/panes):
//
//	troubleshoot   AI producer: capture → triage → author a playbook → drive it
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

	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/driver"
	"github.com/Townk/ai-playbook/internal/input"
	"github.com/Townk/ai-playbook/internal/launcher"
	"github.com/Townk/ai-playbook/internal/mcpserver"
	"github.com/Townk/ai-playbook/internal/ui"
)

// version is the binary's version string. It defaults to "dev" for local
// builds; GoReleaser injects the real tag via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("ai-playbook %s\n", version)
		os.Exit(0)
	case "selftest":
		os.Exit(selftest())
	case "troubleshoot":
		os.Exit(launcher.Troubleshoot())
	case "session":
		os.Exit(launcher.SessionMain())
	case "run":
		// The `run` subcommand opens its own driver; honor the configured shell.
		// ui stays config-agnostic — it receives the selector as DATA via SetShell.
		cfg, _ := config.Load()
		ui.SetShell(cfg.Driver.Shell)
		os.Exit(ui.Main())
	case "answer":
		os.Exit(launcher.AnswerMain())
	case "finalize":
		os.Exit(finalize())
	case "mcp":
		os.Exit(mcpMain())
	case "input":
		os.Exit(input.Main())
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "ai-playbook: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ai-playbook {troubleshoot|session [--request <json>]|run <file.md>|answer --request <json> --content <file> [--cached <iso>] [--title <t>] [--cwd <dir>]|finalize [--dry-run] <file.md>|mcp --socket <path>|input|selftest}")
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
