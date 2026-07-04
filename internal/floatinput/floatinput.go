// Package floatinput is the input-float plumbing shared by the troubleshoot
// LAUNCHER (the request float) and the session's ask tool (the ask float). It
// spawns `ai-playbook input … --out <tmpfile>` in a floating pane via the Mux,
// then polls the out-file for the submitted value.
//
// Why an out-FILE rather than stdout: a pane spawned by `zellij action new-pane`
// runs detached — its stdout is the new pane's tty, not a pipe back to the
// launcher. The float therefore can't hand its answer back over stdout. The
// `input --out <file>` mode (see pkg/dialog) writes the submitted value to a
// file atomically (temp + rename) on submit, and writes NOTHING on cancel. The
// caller here polls for the file's appearance: present → submit (read it),
// timeout with the float gone → cancel.
//
// This replaces the shell's framed-fifo dance (ai-assist-summon's out/in fifos)
// with a simpler one-shot file hand-back. The fifo path in pkg/dialog stays
// for the in-place input→spinner float; this is the request/ask hand-back.
package floatinput

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Townk/ai-playbook/internal/mux"
)

// Float geometry — matches ai-assist-summon: a 57-column pinned/borderless float
// whose height is MEASURED from the rendered widget (fallback when measuring
// fails). inputHeight is the widget's textarea --height (3 rows), passed both to
// the measure run and the live float so they render identically.
const (
	floatCols      = 57
	inputHeight    = 3
	fallbackHeight = 9
)

// Request describes one float-input ask: the widget type and labels, an optional
// prefilled value, and the working dir the float opens in.
type Request struct {
	Type    string // input --type: text|line|confirm|choose|free (free→text)
	Title   string // modal title (--title)
	Prompt  string // description above the input (--prompt)
	Value   string // prefilled value (--value); the request-float prefill
	Cwd     string // working dir the float pane opens in
	Choices []string
	Multi   bool
	History string // text only: --history JSONL path for UP/DOWN recall. Set ONLY
	// by the troubleshoot request float; the ask tool + the `f` amend float leave
	// it empty so they never recall or append (history is opt-in per the spec).
	Thinking bool // text only: pass --thinking so the float, on submit, writes --out
	// but STAYS OPEN animating a wave "Thinking…" state until <out>.done appears
	// (stage C). Only the troubleshoot request float (AskThinking) sets this.
}

// Result is the outcome of a float-input. Submitted is true when the user
// submitted (even an empty string); false means cancelled (Esc) or the float
// vanished without writing an answer.
type Result struct {
	Value     string
	Submitted bool
}

// pollInterval / defaultTimeout bound the out-file poll. The float is a blocking
// modal; a generous timeout lets the user think. A zero Timeout uses the default.
const (
	pollInterval   = 100 * time.Millisecond
	defaultTimeout = 30 * time.Minute
)

// cancelSuffix mirrors dialog.CancelSuffix: a floated `input --out <file>` writes
// <file>.cancel on cancel so the poll learns of a cancel immediately rather than
// waiting out defaultTimeout. The two constants MUST agree (the contract between
// the float and this poller). Kept as a local const to avoid floatinput pulling
// in pkg/dialog's heavy TUI deps.
const cancelSuffix = ".cancel"

// Asker spawns float-inputs. selfExe is the path to THIS ai-playbook binary (the
// float runs `<selfExe> input …`); m is the mux that opens the pane; Timeout
// caps the wait (0 → defaultTimeout). It is a struct so callers (launcher, tools
// backend) construct it once with their resolved selfExe + mux and can be tested
// with a fake mux.
type Asker struct {
	SelfExe string
	Mux     mux.Mux
	Timeout time.Duration
	// poll is the interval override for tests (0 → pollInterval).
	poll time.Duration
}

// Ask spawns the input float for req, waits for the answer (out-file appears) or
// a cancel (timeout with no file), and returns the Result. A spawn error is
// returned; otherwise the bool in Result distinguishes submit from cancel.
func (a Asker) Ask(req Request) (Result, error) {
	dir, err := os.MkdirTemp("", "ai-playbook-float")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "answer")

	cmd := a.buildCmd(req, out)
	if err := a.Mux.SpawnInputFloat(mux.SpawnOptions{
		Cmd:        cmd,
		Cwd:        req.Cwd,
		Floating:   true,
		Name:       "",
		WidthCols:  floatCols,
		HeightRows: a.measureHeight(req),
	}); err != nil {
		return Result{}, err
	}

	return a.poll_(out), nil
}

// AskThinking spawns the request float in --thinking mode (stage C): on submit the
// float WRITES the value to --out but STAYS OPEN animating the wave "Thinking…"
// state; the caller classifies + routes, then CLOSES the float by writing
// <out>.done (the returned out path + dialog.DoneSuffix). It returns the Result and
// the out-file path (for the .done marker).
//
// Unlike Ask, AskThinking does NOT remove the float's temp dir: the float keeps
// polling for <out>.done after this returns, so removing the dir here would race
// the float's poll (it could miss the marker and only exit on its 60s backstop).
// The two tiny files (the answer + the .done marker) are left for the OS /tmp reap,
// matching the codebase's "leave the temp for the async consumer" pattern
// (spawnSession likewise leaves its request JSON for the docked pane).
func (a Asker) AskThinking(req Request) (Result, string, error) {
	req.Thinking = true
	dir, err := os.MkdirTemp("", "ai-playbook-float")
	if err != nil {
		return Result{}, "", err
	}
	out := filepath.Join(dir, "answer")

	cmd := a.buildCmd(req, out)
	if err := a.Mux.SpawnInputFloat(mux.SpawnOptions{
		Cmd:        cmd,
		Cwd:        req.Cwd,
		Floating:   true,
		Name:       "",
		WidthCols:  floatCols,
		HeightRows: a.measureHeight(req),
	}); err != nil {
		os.RemoveAll(dir)
		return Result{}, "", err
	}

	return a.poll_(out), out, nil
}

// measureHeight runs `<selfExe> input --type <t> --measure --width 57 …` to get
// the exact rendered pane height (no TTY), mirroring ai-assist-summon's measure
// step. It parses a bare integer from stdout; any failure (run error or
// non-integer output) falls back to fallbackHeight (9), exactly like the shell's
// `[[ "$measured_h" == <-> ]] || measured_h=9`.
func (a Asker) measureHeight(req Request) int {
	typ := req.Type
	if typ == "" || typ == "free" {
		typ = "text"
	}
	args := []string{
		"input", "--type", typ, "--measure",
		"--width", strconv.Itoa(floatCols),
		"--height", strconv.Itoa(inputHeight),
	}
	if req.Title != "" {
		args = append(args, "--title", req.Title)
	}
	if req.Prompt != "" {
		args = append(args, "--prompt", req.Prompt)
	}
	if req.Value != "" {
		args = append(args, "--value", req.Value)
	}
	if typ == "choose" {
		args = append(args, req.Choices...)
	}
	cmd := exec.Command(a.SelfExe, args...)
	cmd.Stdin = nil
	cmd.Stderr = nil
	outb, err := cmd.Output()
	if err != nil {
		return fallbackHeight
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(outb)))
	if err != nil || n <= 0 {
		return fallbackHeight
	}
	return n
}

// buildCmd assembles the `<selfExe> input …` argv the float runs. free maps to
// the text widget (a multi-line free-form ask). choose appends the options as
// positionals (input reads them from fs.Args()).
func (a Asker) buildCmd(req Request, out string) []string {
	typ := req.Type
	if typ == "" || typ == "free" {
		typ = "text"
	}
	cmd := []string{a.SelfExe, "input", "--type", typ, "--out", out, "--height", strconv.Itoa(inputHeight)}
	if req.Title != "" {
		cmd = append(cmd, "--title", req.Title)
	}
	if req.Prompt != "" {
		cmd = append(cmd, "--prompt", req.Prompt)
	}
	if req.Value != "" {
		cmd = append(cmd, "--value", req.Value)
	}
	if req.Multi {
		cmd = append(cmd, "--multi")
	}
	// History is opt-in and text-only: the request float sets it, the ask/`f`
	// floats leave it empty (no recall, no append).
	if req.History != "" && typ == "text" {
		cmd = append(cmd, "--history", req.History)
	}
	// --thinking is text-only: on submit the float stays open animating until the
	// launcher writes <out>.done (stage C). Only AskThinking sets req.Thinking.
	if req.Thinking && typ == "text" {
		cmd = append(cmd, "--thinking")
	}
	if typ == "choose" {
		cmd = append(cmd, req.Choices...)
	}
	return cmd
}

// poll_ waits for the out-file to appear (submit) or the timeout to elapse
// (cancel). It reads the value on appearance. The trailing underscore avoids the
// `poll` field name; the behavior is the documented poll loop.
func (a Asker) poll_(out string) Result {
	interval := a.poll
	if interval <= 0 {
		interval = pollInterval
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	cancel := out + cancelSuffix
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil {
			return Result{Value: string(b), Submitted: true}
		}
		// The float wrote the cancel marker → the user dismissed it; stop waiting.
		if _, err := os.Stat(cancel); err == nil {
			return Result{Submitted: false}
		}
		time.Sleep(interval)
	}
	return Result{Submitted: false}
}
