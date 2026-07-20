// Package capture gathers the bounded origin context for one assist request:
// the last command + exit (atuin), cwd, git project root, pane id (env), and the
// sliced scrollback (via a Mux dump-screen). It mirrors the shell helpers in
// assist-agent-common.zsh (assist::capture_command / capture_scrollback /
// build_request) and ai-assist-summon.
//
// The atuin source and the Mux are injected so assembly + the scrollback slice
// are unit-testable with fakes; the live atuin/zellij calls are thin shims.
package capture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Townk/ai-playbook/internal/mux"
)

// defaultScrollbackLines is the scrollback cap when neither Options.ScrollbackMax
// nor AI_PLAYBOOK_SCROLLBACK_LINES sets one.
const defaultScrollbackLines = 200

// envScrollbackLines resolves the scrollback cap from AI_PLAYBOOK_SCROLLBACK_LINES
// (see docs/configuration.md). Unset, invalid, or non-positive values fall back
// to defaultScrollbackLines.
func envScrollbackLines() int {
	if v := os.Getenv("AI_PLAYBOOK_SCROLLBACK_LINES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultScrollbackLines
}

// Project is the {name, branch} pair the request carries.
type Project struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
}

// Request is the captured, bounded request context. It mirrors the shell's
// request.json shape (the fields the producer front-half populates).
type Request struct {
	Kind        string  // "error" when the last command failed, else "question"
	Command     string  // last command text
	Exit        string  // last command exit (string; "" if absent)
	DurationMs  string  // last command duration (atuin)
	CWD         string  // working directory the command ran in
	ProjectRoot string  // git toplevel of CWD, else CWD
	PaneID      string  // mux pane id (e.g. "terminal_3"), from env
	Scrollback  string  // sliced viewport for the last run of Command
	UserRequest string  // the user's typed request (filled later; empty at capture)
	Project     Project // {name, branch}
}

// LastCommand is the atuin result for the most recent command in this session.
type LastCommand struct {
	Command  string
	Exit     string
	Dir      string
	Duration string
}

// AtuinSource yields the last command for the current session. It is injected so
// capture is testable without a live atuin.
type AtuinSource interface {
	Last() (LastCommand, error)
}

// Options bundle the injected dependencies + environment for a capture.
type Options struct {
	Mux           mux.Mux     // screen dump source (injected; fake in tests)
	Atuin         AtuinSource // last-command source (injected; fake in tests)
	PaneID        string      // mux pane id (e.g. "terminal_3"); from the caller/env
	ScrollbackMax int         // cap; 0 → $AI_PLAYBOOK_SCROLLBACK_LINES, else 200
	GitToplevelFn func(dir string) (string, bool)
	UserRequest   string // optional pre-filled request
}

// Capture gathers the bounded request context. The last command comes from
// opts.Atuin; the cwd defaults to the command's directory (else $PWD); the
// project root is the git toplevel of the cwd; the scrollback is sliced from the
// mux dump-screen anchored on the last run of the command (only for failures —
// matching summon, which captures scrollback only when CAP_KIND == error).
func Capture(opts Options) Request {
	var r Request

	last, _ := opts.Atuin.Last()
	r.Command = last.Command
	r.Exit = last.Exit
	r.DurationMs = last.Duration

	// Kind: a non-zero exit is an error; otherwise a general question.
	if r.Exit != "" && r.Exit != "0" {
		r.Kind = "error"
	} else {
		r.Kind = "question"
	}

	// cwd: the directory the command ran in, else the process cwd.
	r.CWD = last.Dir
	if r.CWD == "" {
		r.CWD, _ = os.Getwd()
	}

	// project root: git toplevel of cwd, else cwd.
	toplevel := opts.GitToplevelFn
	if toplevel == nil {
		toplevel = gitToplevel
	}
	if root, ok := toplevel(r.CWD); ok {
		r.ProjectRoot = root
	} else {
		r.ProjectRoot = r.CWD
	}

	r.PaneID = opts.PaneID
	r.UserRequest = opts.UserRequest

	// project name = basename of cwd; branch = git current branch.
	r.Project.Name = filepath.Base(r.CWD)
	if br, ok := gitBranch(r.CWD); ok {
		r.Project.Branch = br
	}

	// scrollback: only captured for a failed command (mirrors summon's guard).
	if r.Kind == "error" && opts.Mux != nil && r.Command != "" {
		max := opts.ScrollbackMax
		if max <= 0 {
			max = envScrollbackLines()
		}
		dump, err := opts.Mux.DumpScreen(opts.PaneID)
		if err == nil {
			r.Scrollback = SliceScrollback(dump, r.Command, max)
		}
	}
	return r
}

// SliceScrollback ports the awk slice in assist::capture_scrollback. It anchors
// on the most recent line containing cmd that ACTUALLY HAS output after it
// (i.e. is not the very last line), then takes from that anchor to the line
// before the trailing prompt. With no anchor it returns the last max lines.
//
// Faithful to the awk:
//
//	start = last match index with m[i] < NR
//	if start>0: end = NR-1; if end<start: end = NR
//	else:       start = max(NR-max+1, 1); end = NR
//	if end-start+1 > max: start = end-max+1
//	print lines[start..end]
func SliceScrollback(dump, cmd string, max int) string {
	lines := splitLines(dump)
	nr := len(lines)
	if nr == 0 {
		return ""
	}

	// 1-based match positions where the line contains cmd.
	var matches []int
	for i, ln := range lines {
		if strings.Contains(ln, cmd) {
			matches = append(matches, i+1) // 1-based to mirror NR
		}
	}

	start := 0
	for i := len(matches) - 1; i >= 0; i-- {
		if matches[i] < nr {
			start = matches[i]
			break
		}
	}

	var end int
	if start > 0 {
		end = nr - 1
		if end < start {
			end = nr
		}
	} else {
		start = nr - max + 1
		if start < 1 {
			start = 1
		}
		end = nr
	}
	if end-start+1 > max {
		start = end - max + 1
	}

	// Collect 1-based [start, end] → 0-based slice; awk's print joins with '\n'
	// and appends a trailing newline per line. The shell then captures via
	// print -r -- "$out" (no extra processing). We return lines joined by '\n'
	// with NO trailing newline (the standalone awk output had a trailing \n per
	// line, but the value is then carried in a shell var; callers that need the
	// raw form can add it). For hashing fidelity the cache normalizer is applied
	// downstream, which trims trailing whitespace anyway.
	out := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, lines[i-1])
	}
	return strings.Join(out, "\n")
}

// splitLines splits dump into lines the way awk records them: on '\n', with a
// single trailing newline NOT producing a final empty record.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// ProjectRoot resolves the current project root for the project-local store.
// Priority:
//  1. $AI_PLAYBOOK_PROJECT_ROOT (explicit override)
//  2. git toplevel of the current working directory
//  3. the current working directory itself
func ProjectRoot() string {
	if v := os.Getenv("AI_PLAYBOOK_PROJECT_ROOT"); v != "" {
		return v
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if root, ok := gitToplevel(cwd); ok {
		return root
	}
	return cwd
}

// ── Live shims (thin; not unit-tested against a live system) ────────────────

// gitToplevel runs `git -C <dir> rev-parse --show-toplevel`.
func gitToplevel(dir string) (string, bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	return root, true
}

// gitBranch runs `git -C <dir> rev-parse --abbrev-ref HEAD`.
func gitBranch(dir string) (string, bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", false
	}
	br := strings.TrimSpace(string(out))
	if br == "" {
		return "", false
	}
	return br, true
}

// Atuin is the live atuin shim. It runs `atuin history list --session` and takes
// the last row, mirroring assist::capture_command. Bin defaults to $ATUIN_BIN or
// "atuin".
type Atuin struct {
	Bin string
}

// NewAtuin returns the live atuin source.
func NewAtuin() *Atuin {
	bin := os.Getenv("ATUIN_BIN")
	if bin == "" {
		bin = "atuin"
	}
	return &Atuin{Bin: bin}
}

// Last returns the most recent command in the CURRENT atuin session. Using
// `history list --session` (not `history last`, which is global) so a phantom
// command from another pane never surfaces and a fresh tab yields nothing.
func (a *Atuin) Last() (LastCommand, error) {
	cmd := exec.Command(a.Bin, "history", "list", "--session",
		"--format", "{command}\t{exit}\t{directory}\t{duration}")
	out, err := cmd.Output()
	if err != nil {
		return LastCommand{}, err
	}
	return parseAtuinRows(string(out)), nil
}

// parseAtuinRows takes the multi-line atuin output and parses the LAST non-empty
// row into a LastCommand. It mirrors the shell's tab-field split and the
// single-field guard (CAP_CMD == CAP_EXIT → empty exit).
func parseAtuinRows(out string) LastCommand {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Most recent non-empty row whose command is NOT one of our own trigger
	// invocations — so a manually-typed `ai-playbook …` (or a re-trigger) doesn't
	// become "the failed command"; we look back to what the user actually ran.
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		fields := strings.SplitN(lines[i], "\t", 4)
		if isOwnTrigger(fields[0]) {
			continue
		}
		var lc LastCommand
		lc.Command = fields[0]
		if len(fields) > 1 {
			lc.Exit = fields[1]
		}
		if len(fields) > 2 {
			lc.Dir = fields[2]
		}
		if len(fields) > 3 {
			lc.Duration = fields[3]
		}
		// single-field row guard: command echoed into exit slot.
		if lc.Command == lc.Exit {
			lc.Exit = ""
		}
		return lc
	}
	return LastCommand{}
}

// triggerBinaries lists the binary names (bare or as the final path segment)
// that constitute this tool's own invocation. Both ai-playbook and its apb
// short-name alias (cmd/apb) dispatch through the same cli.Run, so both must
// be recognized here — a future rename has one edit site.
var triggerBinaries = []string{"ai-playbook", "apb"}

// isOwnTrigger reports whether a command line is one of ai-playbook's own
// invocations (the trigger), which must be skipped when finding the failed command.
func isOwnTrigger(cmd string) bool {
	f := strings.Fields(strings.TrimSpace(cmd))
	if len(f) == 0 {
		return false
	}
	for _, bin := range triggerBinaries {
		if f[0] == bin || strings.HasSuffix(f[0], "/"+bin) {
			return true
		}
	}
	return false
}

// ExitInt returns the request's exit as an int (and ok=false if not numeric).
func (r Request) ExitInt() (int, bool) {
	n, err := strconv.Atoi(r.Exit)
	if err != nil {
		return 0, false
	}
	return n, true
}
