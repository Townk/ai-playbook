// Package mux is the terminal-multiplexer adapter. It is now CONFIG-DRIVEN: the
// per-action commands are TOML templates (config.Mux) the user can override —
// there is no per-mux Go code. The adapter substitutes placeholders into the
// configured template, splits the result into argv (no shell), and runs it.
//
// The Mux interface is unchanged for existing callers (capture, orchestrator
// view-diff). Map of method → template:
//
//	DumpScreen  → dump-screen          (captures stdout, returns the screen text)
//	SpawnFloat  → open-floating-pane   (fire-and-forget, detached stdio)
//	SpawnDocked → open-docked-pane     (fire-and-forget, detached stdio) [new]
//	TypeInto    → type-into-pane       (the `play` action) [new]
//
// The interface stays injectable so consumers are testable with a fake.
package mux

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Townk/ai-playbook/internal/config"
)

// ErrNotImplemented marks a Mux method modeled but deferred to a later stage.
var ErrNotImplemented = errors.New("mux: not implemented yet")

// SpawnOptions describes a pane/float to open. Fields beyond Cmd are advisory
// hints honored by the configured template; they exist so the interface is
// stable across stages.
type SpawnOptions struct {
	Cmd      []string // command + args to run in the new pane ({cmd})
	Cwd      string   // working dir for the pane ({cwd}/{cwdarg})
	Name     string   // pane title ({name}/{namearg})
	Floating bool     // float vs tiled (advisory; template selects the action)
	Width    int      // requested columns as a PERCENT (0 → template default)
	Height   int      // requested rows as a PERCENT (0 → template default)
	// WidthCols/HeightRows are ABSOLUTE sizes (literal columns/rows). When > 0 they
	// take precedence over the percent Width/Height and {width}/{height} expand to a
	// bare integer (not "<n>%") — used by the input float, which is sized exactly
	// like ai-assist-summon's `--width 57 --height <measured>`.
	WidthCols  int
	HeightRows int
	Direction  string // tiled direction (advisory)
}

// Mux is the terminal-multiplexer surface for the producer.
type Mux interface {
	// DumpScreen returns the current viewport text of pane (a mux-specific pane
	// id, e.g. "terminal_3"; empty means the focused pane).
	DumpScreen(pane string) (string, error)
	// SpawnFloat opens a floating pane running opts.Cmd (e.g. the diff viewer).
	SpawnFloat(opts SpawnOptions) error
	// SpawnInputFloat opens the borderless+pinned, absolute-sized INPUT float (the
	// request/ask widget) running opts.Cmd. Sizing comes from opts.WidthCols /
	// opts.HeightRows (absolute columns/rows), mirroring ai-assist-summon.
	SpawnInputFloat(opts SpawnOptions) error
	// SpawnPane opens a tiled pane running opts.Cmd. Deferred (use SpawnDocked).
	SpawnPane(opts SpawnOptions) error
	// SpawnDocked opens a docked (down-direction) tiled pane running opts.Cmd.
	SpawnDocked(opts SpawnOptions) error
	// TypeInto types text into a pane — the `play` action. pane is advisory
	// (zellij write-chars targets the focused pane); empty means focused.
	TypeInto(pane, text string) error
}

// templated is the config-driven Mux implementation. It holds the resolved mux
// templates and substitutes/execs them per action. Its zero value is unusable;
// build it with FromConfig.
type templated struct {
	tpl config.Mux
}

// FromConfig builds a Mux from the merged config. With the zellij preset's
// templates (config.Default's Mux command templates) this reproduces the previous
// hardcoded zellij invocations exactly, so capture and view-diff are unchanged
// once the multiplexer is opted in via [mux] backend.
func FromConfig(cfg *config.Config) Mux {
	return &templated{tpl: cfg.Mux}
}

// Select picks the right Mux for the merged config. The multiplexer is OFF by
// default: it returns Null() unless the user explicitly opted in with
// [mux] backend = "<name>" (cfg.Mux.Backend != ""). There is no $ZELLIJ
// auto-enable (ADR-0007). When a backend is set it returns the config-driven
// (templated) Mux, so the floating/docked/templated behavior is unchanged for
// callers who opted in.
func Select(cfg *config.Config) Mux {
	if cfg.Mux.Backend == "" {
		return Null()
	}
	return FromConfig(cfg)
}

// Load builds a Mux from the user's merged config (config.Load), falling back to
// the baked-in default profile if the config cannot be loaded. Convenience for
// call sites that just want "the configured mux" without threading a *Config.
func Load() Mux {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Default()
	}
	return Select(cfg)
}

// percent renders n as a "<n>%" size string, empty when n <= 0 (template default).
func percent(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n) + "%"
}

// DumpScreen runs the dump-screen template and returns stdout. Mirrors
// assist::capture_scrollback's dump (viewport, NOT --full). A failed dump
// returns the error so the caller can fall back to an empty capture.
func (t *templated) DumpScreen(pane string) (string, error) {
	argv := t.tpl.Substitute(t.tpl.DumpScreen, config.Subst{Pane: pane})
	if len(argv) == 0 {
		return "", errors.New("mux: dump-screen template is empty")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// spawn runs a fire-and-forget pane template (float or docked). Per the broker's
// best-effort pattern, the spawned process's stdio is detached so a chatty/failed
// spawn can never corrupt the docked UI pane. A spawn error is returned but is
// non-fatal to callers.
func (t *templated) spawn(template string, opts SpawnOptions) error {
	if len(opts.Cmd) == 0 {
		return errors.New("mux: spawn needs a command")
	}
	// Absolute (WidthCols/HeightRows) wins over the percent Width/Height: the input
	// float emits bare integers (57 / measured), the diff float keeps percents.
	width, height := defaultPercent(opts.Width, 90), defaultPercent(opts.Height, 90)
	if opts.WidthCols > 0 {
		width = strconv.Itoa(opts.WidthCols)
	}
	if opts.HeightRows > 0 {
		height = strconv.Itoa(opts.HeightRows)
	}
	argv := t.tpl.Substitute(template, config.Subst{
		Cmd:    opts.Cmd,
		Cwd:    opts.Cwd,
		Name:   opts.Name,
		Width:  width,
		Height: height,
	})
	if len(argv) == 0 {
		return errors.New("mux: spawn template is empty")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	// Detach stdout/stdin so the spawn cannot write into our pane; capture stderr
	// into a buffer (not the pane) so a failed spawn can explain itself in the
	// returned error without corrupting the UI.
	cmd.Stdout = nil
	cmd.Stdin = nil
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if msg := bytes.TrimSpace(errb.Bytes()); len(msg) > 0 {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// defaultPercent renders n as a percent, substituting def when n <= 0 — so the
// float/docked templates get the broker's literal 90% default when unset.
func defaultPercent(n, def int) string {
	if n <= 0 {
		return percent(def)
	}
	return percent(n)
}

// SpawnFloat opens a floating pane via the open-floating-pane template.
func (t *templated) SpawnFloat(opts SpawnOptions) error {
	return t.spawn(t.tpl.OpenFloatingPane, opts)
}

// SpawnInputFloat opens the borderless+pinned, absolute-sized input float via the
// open-input-float template (the request/ask widget). Falls back to the plain
// floating-pane template if open-input-float is unconfigured (empty after merge),
// so an operator who only overrode open-floating-pane still gets a float.
func (t *templated) SpawnInputFloat(opts SpawnOptions) error {
	tpl := t.tpl.OpenInputFloat
	if tpl == "" {
		tpl = t.tpl.OpenFloatingPane
	}
	return t.spawn(tpl, opts)
}

// SpawnDocked opens a docked (down) pane via the open-docked-pane template.
func (t *templated) SpawnDocked(opts SpawnOptions) error {
	return t.spawn(t.tpl.OpenDockedPane, opts)
}

// SpawnPane is deferred; callers should use SpawnDocked or SpawnFloat.
func (t *templated) SpawnPane(opts SpawnOptions) error { return ErrNotImplemented }

// TypeInto types text into a pane via the type-into-pane template — the `play`
// action. Best-effort: stdio is detached. When pane is non-empty the default
// template targets it explicitly (`--pane-id {pane}`), so the write is
// focus-independent. When pane is empty (off-zellij / inline) the pane-id flag is
// stripped so we never emit a stray empty `--pane-id`, falling back to a write
// into the FOCUSED pane.
func (t *templated) TypeInto(pane, text string) error {
	argv := t.typeIntoArgv(pane, text)
	if len(argv) == 0 {
		return errors.New("mux: type-into-pane template is empty")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	return cmd.Run()
}

// typeIntoArgv resolves the type-into-pane template to argv. When pane is empty,
// the pane-id flag+placeholder pair is stripped so the resolved argv has no
// dangling, valueless pane flag — a focused write. The strip list covers the
// zellij forms (`--pane-id`, `-p`) and tmux's `-t` (a user template like
// `tmux send-keys -t {pane} -l {text}` must not become `send-keys -t ”`).
func (t *templated) typeIntoArgv(pane, text string) []string {
	tpl := t.tpl.TypeIntoPane
	if pane == "" {
		tpl = strings.NewReplacer(
			"--pane-id {pane}", "",
			"--pane-id={pane}", "",
			"-p {pane}", "",
			"-t {pane}", "",
			"-t={pane}", "",
		).Replace(tpl)
	}
	return t.tpl.Substitute(tpl, config.Subst{Pane: pane, Text: text})
}
