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
	// SpawnFloat opens a floating pane running opts.Cmd. Deferred.
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

// SpawnFloat is deferred to a later migration stage.
func (z *Zellij) SpawnFloat(opts SpawnOptions) error { return ErrNotImplemented }

// SpawnPane is deferred to a later migration stage.
func (z *Zellij) SpawnPane(opts SpawnOptions) error { return ErrNotImplemented }
