// Package mux is the terminal-multiplexer adapter: a pluggable interface with a
// zellij implementation. It is the "detect don't list" seam from the design —
// the producer's capture step needs only DumpScreen now; float/pane spawn are
// defined for the later stages but return ErrNotImplemented until wired.
//
// The interface is injectable so capture (and any other consumer) is testable
// with a fake that returns canned screen dumps.
package mux

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
)

// ErrNotImplemented marks a Mux method modeled but deferred to a later stage.
var ErrNotImplemented = errors.New("mux: not implemented yet")

// SpawnOptions describes a pane/float to open. Fields beyond Cmd are advisory
// hints the impl may honor; they exist so the interface is stable across stages.
type SpawnOptions struct {
	Cmd       []string // command + args to run in the new pane
	Cwd       string   // working dir for the pane
	Name      string   // pane title
	Floating  bool     // float vs tiled
	Width     int      // requested columns (impl may ignore)
	Height    int      // requested rows (impl may ignore)
	Direction string   // tiled direction (e.g. "right")
}

// Mux is the terminal-multiplexer surface for the producer. DumpScreen is the
// only method needed in stage 4a; the spawn methods are part of the contract but
// return ErrNotImplemented for now (wired in a later stage).
type Mux interface {
	// DumpScreen returns the current viewport text of pane (a mux-specific pane
	// id, e.g. "terminal_3"; empty means the focused pane).
	DumpScreen(pane string) (string, error)
	// SpawnFloat opens a floating pane running opts.Cmd (e.g. the diff viewer).
	SpawnFloat(opts SpawnOptions) error
	// SpawnPane opens a tiled pane running opts.Cmd. Deferred.
	SpawnPane(opts SpawnOptions) error
}

// Zellij is the zellij implementation of Mux. The binary path is resolved once;
// an empty Bin falls back to "zellij" on PATH.
type Zellij struct {
	Bin string
}

// NewZellij returns a Zellij adapter, resolving the binary like the shell's
// assist::zellij_bin: $ZELLIJ_BIN, else "zellij" on PATH, else a few well-known
// install locations.
func NewZellij() *Zellij {
	return &Zellij{Bin: resolveZellijBin()}
}

func resolveZellijBin() string {
	if v := os.Getenv("ZELLIJ_BIN"); v != "" {
		if isExec(v) {
			return v
		}
	}
	if p, err := exec.LookPath("zellij"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, cand := range []string{
		"/opt/homebrew/bin/zellij",
		"/usr/local/bin/zellij",
		"/snap/bin/zellij",
		home + "/.local/bin/zellij",
	} {
		if isExec(cand) {
			return cand
		}
	}
	return "zellij"
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// DumpScreen runs `zellij action dump-screen [-p <pane>]` and returns stdout.
// Mirrors assist::capture_scrollback's dump (viewport, NOT --full). A failed
// dump returns the error so the caller can fall back to an empty capture.
func (z *Zellij) DumpScreen(pane string) (string, error) {
	args := []string{"action", "dump-screen"}
	if pane != "" {
		args = append(args, "-p", pane)
	}
	cmd := exec.Command(z.Bin, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// floatArgs builds the `zellij action new-pane --floating …` argument vector for
// opts, ported from the shell broker's broker::open_diff invocation:
//
//	zellij action new-pane --floating --width 90% --height 90% --close-on-exit \
//	  --cwd <root> --name <name> -- <cmd...>
//
// Width/Height default to 90% (the broker's literal) when opts leaves them 0;
// --cwd and --name are emitted only when set. opts.Cmd follows the `--` separator
// so its own flags are never parsed by zellij. Exposed (lower-case, package-local)
// so a test can assert the constructed command without a real zellij.
func (z *Zellij) floatArgs(opts SpawnOptions) []string {
	width := "90%"
	if opts.Width > 0 {
		width = itoaPercent(opts.Width)
	}
	height := "90%"
	if opts.Height > 0 {
		height = itoaPercent(opts.Height)
	}
	args := []string{"action", "new-pane", "--floating",
		"--width", width, "--height", height, "--close-on-exit"}
	if opts.Cwd != "" {
		args = append(args, "--cwd", opts.Cwd)
	}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	args = append(args, "--")
	args = append(args, opts.Cmd...)
	return args
}

// SpawnFloat opens a floating zellij pane running opts.Cmd, mirroring the shell
// broker's broker::open_diff. Per the broker's best-effort pattern, the spawned
// `zellij action` process's own stdout/stderr are redirected to /dev/null so a
// chatty/failed spawn can never corrupt the docked UI pane (the broker used
// `2>/dev/null || true`). A spawn error is returned but is non-fatal to callers.
func (z *Zellij) SpawnFloat(opts SpawnOptions) error {
	if len(opts.Cmd) == 0 {
		return errors.New("mux: SpawnFloat needs a command")
	}
	cmd := exec.Command(z.Bin, z.floatArgs(opts)...)
	// Detach the float-spawn's stdio so it cannot write into our pane.
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	return cmd.Run()
}

// SpawnPane is deferred to a later migration stage.
func (z *Zellij) SpawnPane(opts SpawnOptions) error { return ErrNotImplemented }

// itoaPercent renders n as a "<n>%" zellij size string.
func itoaPercent(n int) string { return strconv.Itoa(n) + "%" }
