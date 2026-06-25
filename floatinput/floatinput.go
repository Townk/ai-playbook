// Package floatinput is the input-float plumbing shared by the troubleshoot
// LAUNCHER (the request float) and the session's ask tool (the ask float). It
// spawns `ai-playbook input … --out <tmpfile>` in a floating pane via the Mux,
// then polls the out-file for the submitted value.
//
// Why an out-FILE rather than stdout: a pane spawned by `zellij action new-pane`
// runs detached — its stdout is the new pane's tty, not a pipe back to the
// launcher. The float therefore can't hand its answer back over stdout. The
// `input --out <file>` mode (see package input) writes the submitted value to a
// file atomically (temp + rename) on submit, and writes NOTHING on cancel. The
// caller here polls for the file's appearance: present → submit (read it),
// timeout with the float gone → cancel.
//
// This replaces the shell's framed-fifo dance (ai-assist-summon's out/in fifos)
// with a simpler one-shot file hand-back. The fifo path in package input stays
// for the in-place input→spinner float; this is the request/ask hand-back.
package floatinput

import (
	"os"
	"path/filepath"
	"time"

	"ai-playbook/mux"
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

// cancelSuffix mirrors input.CancelSuffix: a floated `input --out <file>` writes
// <file>.cancel on cancel so the poll learns of a cancel immediately rather than
// waiting out defaultTimeout. The two constants MUST agree (the contract between
// the float and this poller). Kept as a local const to avoid floatinput pulling
// in the input package's heavy TUI deps.
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
	if err := a.Mux.SpawnFloat(mux.SpawnOptions{
		Cmd:      cmd,
		Cwd:      req.Cwd,
		Floating: true,
	}); err != nil {
		return Result{}, err
	}

	return a.poll_(out), nil
}

// buildCmd assembles the `<selfExe> input …` argv the float runs. free maps to
// the text widget (a multi-line free-form ask). choose appends the options as
// positionals (input reads them from fs.Args()).
func (a Asker) buildCmd(req Request, out string) []string {
	typ := req.Type
	if typ == "" || typ == "free" {
		typ = "text"
	}
	cmd := []string{a.SelfExe, "input", "--type", typ, "--out", out}
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
